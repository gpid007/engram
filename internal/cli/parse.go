package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ParsedFact is a single structured memory fact extracted from raw text.
type ParsedFact struct {
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
	Source  string   `json:"source"`
}

// String returns a human-readable representation of the fact.
func (f ParsedFact) String() string {
	return fmt.Sprintf("%s [%s]", f.Content, strings.Join(f.Tags, ", "))
}

// Parser converts raw text into structured facts.
// Implement this interface to swap in different parsing backends.
type Parser interface {
	Parse(ctx context.Context, raw string) ([]ParsedFact, error)
}

// systemPrompt instructs the local model how to structure facts.
const systemPrompt = `You are a memory structuring assistant. Given raw text, extract distinct facts and return ONLY a JSON array of objects. Each object must have:
  "content": a single concise fact (one sentence max)
  "tags": array of lowercase strings classifying the fact. Use these conventions:
    - person facts: ["people", "person", "<name>"]
    - relationships: ["people", "relationship", "<name1>", "<name2>"]
    - project/work: ["people", "project", "<name>", "<project>"]
    - preferences: ["preferences", "<category>", "<value>"]
    - architecture decisions: ["architecture", "decisions"]
    - workarounds: ["workarounds", "<tech>"]
  "source": always "conversation"
Split compound statements into separate facts. Never combine unrelated facts into one object.
Return ONLY the JSON array, no other text, no markdown, no explanation.`

// ollamaChatRequest is the request body for the Ollama chat API.
type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Format   string          `json:"format"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatResponse struct {
	Message ollamaMessage `json:"message"`
}

// OllamaParser uses a local Ollama model to parse raw text into structured facts.
type OllamaParser struct {
	BaseURL string
	Model   string
	Timeout time.Duration
	UserID  string
	client  *http.Client
}

// NewOllamaParser creates a new OllamaParser.
func NewOllamaParser(baseURL, model, userID string, timeout time.Duration) *OllamaParser {
	return &OllamaParser{
		BaseURL: baseURL,
		Model:   model,
		Timeout: timeout,
		UserID:  userID,
		client:  &http.Client{Timeout: timeout},
	}
}

// Parse calls the Ollama chat API and parses the response into []ParsedFact.
// On any error it falls back to NopParser behaviour rather than failing hard.
func (p *OllamaParser) Parse(ctx context.Context, raw string) ([]ParsedFact, error) {
	reqBody := ollamaChatRequest{
		Model: p.Model,
		Messages: []ollamaMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: raw},
		},
		Stream: false,
		Format: "json",
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return nopParse(raw), nil
	}
	url := strings.TrimRight(p.BaseURL, "/") + "/api/chat"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nopParse(raw), nil
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		slog.Warn("ollama parse failed; using raw text as single fact", "err", err)
		return nopParse(raw), nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		slog.Warn("ollama parse non-200; using raw text as single fact", "status", resp.StatusCode)
		return nopParse(raw), nil
	}
	var chatResp ollamaChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		slog.Warn("ollama response decode failed; using raw text", "err", err)
		return nopParse(raw), nil
	}
	var facts []ParsedFact
	if err := json.Unmarshal([]byte(chatResp.Message.Content), &facts); err != nil {
		slog.Warn("fact JSON decode failed; using raw text", "err", err, "content", chatResp.Message.Content)
		return nopParse(raw), nil
	}
	// Validate and ensure source is set.
	out := make([]ParsedFact, 0, len(facts))
	for _, f := range facts {
		if strings.TrimSpace(f.Content) == "" || len(f.Tags) == 0 {
			continue
		}
		if f.Source == "" {
			f.Source = "conversation"
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nopParse(raw), nil
	}
	return out, nil
}

// NopParser returns the raw text as a single untagged fact. Useful for testing
// or when Ollama is unavailable.
type NopParser struct{}

// Parse implements Parser for NopParser.
func (NopParser) Parse(_ context.Context, raw string) ([]ParsedFact, error) {
	return nopParse(raw), nil
}

// nopParse is the shared fallback: one fact, raw content, untagged.
func nopParse(raw string) []ParsedFact {
	return []ParsedFact{{Content: raw, Tags: []string{"untagged"}, Source: "conversation"}}
}

// Compile-time interface checks.
var _ Parser = (*OllamaParser)(nil)
var _ Parser = NopParser{}

// SplitParagraphs splits text into paragraphs for sequential local-model parsing.
// Rules:
//   - Split on blank lines (two or more consecutive newlines)
//   - Start a new paragraph when a line begins with # (markdown heading)
//   - Trim whitespace from each paragraph
//   - Drop paragraphs shorter than 20 chars (noise)
//   - If a paragraph exceeds 2000 chars, split on ". " sentence boundary
func SplitParagraphs(text string) []string {
	// First split on blank lines.
	raw := strings.Split(text, "\n\n")
	var sections []string
	for _, block := range raw {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		// Sub-split on markdown headings (lines starting with #).
		lines := strings.Split(block, "\n")
		var current strings.Builder
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "#") && current.Len() > 0 {
				if s := strings.TrimSpace(current.String()); len(s) >= 20 {
					sections = append(sections, s)
				}
				current.Reset()
			}
			current.WriteString(line)
			current.WriteByte('\n')
		}
		if s := strings.TrimSpace(current.String()); len(s) >= 20 {
			sections = append(sections, s)
		}
	}

	// Split oversized paragraphs on sentence boundaries.
	var out []string
	for _, s := range sections {
		if len(s) <= 2000 {
			out = append(out, s)
			continue
		}
		sentences := strings.Split(s, ". ")
		var chunk strings.Builder
		for _, sent := range sentences {
			if chunk.Len()+len(sent) > 2000 && chunk.Len() > 0 {
				out = append(out, strings.TrimSpace(chunk.String()))
				chunk.Reset()
			}
			chunk.WriteString(sent)
			chunk.WriteString(". ")
		}
		if s := strings.TrimSpace(chunk.String()); len(s) >= 20 {
			out = append(out, s)
		}
	}
	return out
}

// ReadFileText reads a text file and returns its content.
// Only .txt and .md files are supported.
func ReadFileText(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".txt" && ext != ".md" {
		return "", fmt.Errorf("unsupported file type %q: only .txt and .md are supported", ext)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file %q: %w", path, err)
	}
	return string(b), nil
}
