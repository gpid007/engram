// Package cli provides the HTTP client and command handlers for the engram CLI subcommands.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a thin HTTP client for the Engram API.
type Client struct {
	BaseURL    string
	UserID     string
	HTTPClient *http.Client
}

// NewClient creates a new API client.
func NewClient(baseURL, userID string, timeout time.Duration) *Client {
	return &Client{
		BaseURL:    baseURL,
		UserID:     userID,
		HTTPClient: &http.Client{Timeout: timeout},
	}
}

// StoreRequest is the request body for POST /v1/memories.
type StoreRequest struct {
	UserID   string        `json:"user_id"`
	Content  string        `json:"content"`
	Source   string        `json:"source"`
	Metadata StoreMetadata `json:"metadata"`
}

// StoreMetadata holds tags for a memory.
type StoreMetadata struct {
	Tags []string `json:"tags"`
}

// StoreResponse is the response from POST /v1/memories.
type StoreResponse struct {
	MemoryID      string `json:"MemoryID"`
	ChunksStored  int    `json:"ChunksStored"`
	ChunksDeduped int    `json:"ChunksDeduped"`
	Stored        bool   `json:"Stored"`
}

// RetrieveRequest is the request body for POST /v1/retrieve.
type RetrieveRequest struct {
	UserID string `json:"user_id"`
	Query  string `json:"query"`
	K      int    `json:"k"`
	Rerank bool   `json:"rerank"`
}

// RetrieveResult is one result from retrieval.
type RetrieveResult struct {
	MemoryID  string  `json:"memory_id"`
	ChunkID   string  `json:"chunk_id"`
	Content   string  `json:"content"`
	Score     float64 `json:"score"`
	Source    string  `json:"source"`
	CreatedAt string  `json:"created_at"`
}

// RetrieveStats holds retrieval timing stats.
type RetrieveStats struct {
	TotalMS int `json:"total_ms"`
}

// RetrieveResponse is the response from POST /v1/retrieve.
type RetrieveResponse struct {
	Results []RetrieveResult `json:"results"`
	Stats   RetrieveStats    `json:"stats"`
}

// UserStateResponse is the response from GET /v1/users/{id}/state.
type UserStateResponse struct {
	MemoryCount int      `json:"memory_count"`
	ChunkCount  int      `json:"chunk_count"`
	FirstMemory string   `json:"first_memory"`
	LastMemory  string   `json:"last_memory"`
	TopSources  []string `json:"top_sources"`
}

// Store stores a single fact in Engram.
func (c *Client) Store(ctx context.Context, content, source string, tags []string) (StoreResponse, error) {
	req := StoreRequest{
		UserID:   c.UserID,
		Content:  content,
		Source:   source,
		Metadata: StoreMetadata{Tags: tags},
	}
	var resp StoreResponse
	err := c.post(ctx, "/v1/memories", req, &resp)
	return resp, err
}

// Retrieve retrieves memories matching a query.
func (c *Client) Retrieve(ctx context.Context, query string, k int, rerank bool) (RetrieveResponse, error) {
	req := RetrieveRequest{
		UserID: c.UserID,
		Query:  query,
		K:      k,
		Rerank: rerank,
	}
	var resp RetrieveResponse
	err := c.post(ctx, "/v1/retrieve", req, &resp)
	return resp, err
}

// State returns aggregate stats for the user.
func (c *Client) State(ctx context.Context) (UserStateResponse, error) {
	url := fmt.Sprintf("%s/v1/users/%s/state", c.BaseURL, c.UserID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return UserStateResponse{}, err
	}
	httpResp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return UserStateResponse{}, fmt.Errorf("state request: %w", err)
	}
	defer httpResp.Body.Close()
	body, _ := io.ReadAll(httpResp.Body)
	if httpResp.StatusCode != http.StatusOK {
		return UserStateResponse{}, fmt.Errorf("state: status %d: %s", httpResp.StatusCode, body)
	}
	var resp UserStateResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return UserStateResponse{}, fmt.Errorf("state decode: %w", err)
	}
	return resp, nil
}

// post is a helper for JSON POST requests.
func (c *Client) post(ctx context.Context, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := c.BaseURL + path
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s: status %d: %s", path, resp.StatusCode, respBody)
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("POST %s decode: %w", path, err)
	}
	return nil
}
