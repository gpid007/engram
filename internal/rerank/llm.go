package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// LLMConfig holds configuration for the LLM-based reranker.
type LLMConfig struct {
	BaseURL string        // e.g. "http://ollama:11434"
	Model   string        // e.g. "llama3.2:1b"
	Timeout time.Duration // HTTP client timeout; defaults to 1500ms
}

// LLMReranker reranks candidates by asking an Ollama-compatible LLM to score them.
type LLMReranker struct {
	cfg    LLMConfig
	client *http.Client
}

// NewLLMReranker creates a new LLMReranker with the given config.
func NewLLMReranker(cfg LLMConfig) *LLMReranker {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 1500 * time.Millisecond
	}
	return &LLMReranker{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}
}

// ollamaChatRequest is the JSON body sent to /api/chat.
type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	Messages []ollamaMessage `json:"messages"`
}

// ollamaMessage is a single message in the chat.
type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ollamaChatResponse is the JSON body returned by /api/chat.
type ollamaChatResponse struct {
	Message ollamaMessage `json:"message"`
}

// Rerank asks the LLM to score each candidate and returns them sorted by score descending.
// On any failure (network, timeout, parse error, wrong array length) it returns the
// candidates in their original order with no error (degraded gracefully).
func (r *LLMReranker) Rerank(ctx context.Context, query string, candidates []Candidate) ([]Candidate, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}

	prompt := buildPrompt(query, candidates)

	scores, err := r.fetchScores(ctx, prompt, len(candidates))
	if err != nil {
		log.Printf("llm reranker: falling back to fused order: %v", err)
		return candidates, nil
	}

	// Apply scores and sort descending.
	type indexed struct {
		candidate Candidate
		score     float64
	}
	ranked := make([]indexed, len(candidates))
	for i, c := range candidates {
		ranked[i] = indexed{candidate: c, score: scores[i]}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})

	result := make([]Candidate, len(candidates))
	for i, r := range ranked {
		result[i] = r.candidate
	}
	return result, nil
}

// buildPrompt constructs the relevance-scoring prompt.
func buildPrompt(query string, candidates []Candidate) string {
	var sb strings.Builder
	sb.WriteString("You are a relevance scoring assistant. Score each document's relevance to the query on a scale of 0-10.\n\n")
	fmt.Fprintf(&sb, "Query: %s\n\n", query)
	sb.WriteString("Documents:\n")
	for i, c := range candidates {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, c.Content)
	}
	sb.WriteString("\nRespond with ONLY a JSON array of scores in document order, e.g.: [8, 3, 7]\nDo not include any other text.")
	return sb.String()
}

// fetchScores sends the prompt to the Ollama /api/chat endpoint and parses the response.
// Returns an error only for hard failures; parse/length errors are surfaced as errFallback.
var errFallback = fmt.Errorf("fallback to original order")

func (r *LLMReranker) fetchScores(ctx context.Context, prompt string, n int) ([]float64, error) {
	reqBody := ollamaChatRequest{
		Model:  r.cfg.Model,
		Stream: false,
		Messages: []ollamaMessage{
			{Role: "user", Content: prompt},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.BaseURL+"/api/chat", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		// context cancelled, timeout, network error → degrade gracefully
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var chatResp ollamaChatResponse
	if err := json.Unmarshal(data, &chatResp); err != nil {
		log.Printf("llm reranker: failed to unmarshal chat response: %v", err)
		return nil, errFallback
	}

	scores, err := parseScores(chatResp.Message.Content, n)
	if err != nil {
		log.Printf("llm reranker: score parse error: %v", err)
		return nil, errFallback
	}
	return scores, nil
}

// parseScores extracts a float64 slice from the LLM text response.
// It strips markdown code fences, trims whitespace, and clamps values to [0,10].
func parseScores(content string, n int) ([]float64, error) {
	text := strings.TrimSpace(content)

	// Strip markdown code fences: ```json ... ``` or ``` ... ```
	if strings.HasPrefix(text, "```") {
		// Remove opening fence line
		if idx := strings.Index(text, "\n"); idx != -1 {
			text = text[idx+1:]
		}
		// Remove closing fence
		if idx := strings.LastIndex(text, "```"); idx != -1 {
			text = text[:idx]
		}
		text = strings.TrimSpace(text)
	}

	var raw []json.Number
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, fmt.Errorf("parse scores array: %w", err)
	}

	if len(raw) != n {
		return nil, fmt.Errorf("expected %d scores, got %d", n, len(raw))
	}

	scores := make([]float64, n)
	for i, v := range raw {
		f, err := v.Float64()
		if err != nil {
			return nil, fmt.Errorf("score[%d] not a number: %w", i, err)
		}
		// Clamp to [0, 10]
		if f < 0 {
			f = 0
		} else if f > 10 {
			f = 10
		}
		scores[i] = f
	}
	return scores, nil
}
