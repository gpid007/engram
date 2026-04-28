package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

// RemoteConfig configures the remote reranker.
type RemoteConfig struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

// RemoteReranker calls a Cohere-style rerank API.
type RemoteReranker struct {
	cfg    RemoteConfig
	client *http.Client
}

// NewRemoteReranker creates a RemoteReranker. If cfg.BaseURL is empty the
// reranker is disabled and Rerank returns candidates unchanged.
func NewRemoteReranker(cfg RemoteConfig) *RemoteReranker {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	return &RemoteReranker{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
	}
}

type rerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type rerankResponseItem struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

type rerankResponse struct {
	Results []rerankResponseItem `json:"results"`
}

// Rerank reorders candidates by relevance score from the remote API.
// Returns candidates unchanged if BaseURL is empty.
func (r *RemoteReranker) Rerank(ctx context.Context, query string, candidates []Candidate) ([]Candidate, error) {
	if r.cfg.BaseURL == "" {
		return candidates, nil
	}
	if len(candidates) == 0 {
		return []Candidate{}, nil
	}

	docs := make([]string, len(candidates))
	for i, c := range candidates {
		docs[i] = c.Content
	}

	body, err := json.Marshal(rerankRequest{
		Model:     r.cfg.Model,
		Query:     query,
		Documents: docs,
	})
	if err != nil {
		return nil, fmt.Errorf("rerank marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.BaseURL+"/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("rerank request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.cfg.APIKey)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrRetryable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return nil, fmt.Errorf("%w: HTTP %d", ErrNonRetryable, resp.StatusCode)
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: HTTP %d", ErrRetryable, resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("rerank read body: %w", err)
	}

	var result rerankResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("rerank unmarshal: %w", err)
	}

	type scored struct {
		candidate Candidate
		score     float64
	}
	scored_list := make([]scored, 0, len(result.Results))
	for _, item := range result.Results {
		if item.Index < 0 || item.Index >= len(candidates) {
			continue
		}
		scored_list = append(scored_list, scored{
			candidate: candidates[item.Index],
			score:     item.RelevanceScore,
		})
	}
	sort.SliceStable(scored_list, func(i, j int) bool {
		return scored_list[i].score > scored_list[j].score
	})

	out := make([]Candidate, len(scored_list))
	for i, s := range scored_list {
		out[i] = s.candidate
	}
	return out, nil
}
