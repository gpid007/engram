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
// Note: the API returns PascalCase JSON keys.
type RetrieveResult struct {
	MemoryID  string  `json:"MemoryID"`
	ChunkID   string  `json:"ChunkID"`
	Content   string  `json:"Content"`
	Score     float64 `json:"Score"`
	Source    string  `json:"Source"`
	CreatedAt string  `json:"CreatedAt"`
}

// RetrieveStats holds retrieval timing stats.
type RetrieveStats struct {
	TotalMs int `json:"TotalMs"`
}

// RetrieveResponse is the response from POST /v1/retrieve.
// Note: the API returns PascalCase JSON keys.
type RetrieveResponse struct {
	Results []RetrieveResult `json:"Results"`
	Stats   RetrieveStats    `json:"Stats"`
}

// UserStateResponse is the response from GET /v1/users/{id}/state.
// Note: the API returns PascalCase JSON keys.
type UserStateResponse struct {
	MemoryCount int      `json:"MemoryCount"`
	ChunkCount  int      `json:"ChunkCount"`
	FirstMemory string   `json:"FirstMemory"`
	LastMemory  string   `json:"LastMemory"`
	TopSources  []string `json:"TopSources"`
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

// DeleteMemory deletes a memory by ID via DELETE /v1/memories/:id.
// Returns nil on 204, a descriptive error on 404 or other failures.
func (c *Client) DeleteMemory(ctx context.Context, memoryID string) error {
	url := fmt.Sprintf("%s/v1/memories/%s", c.BaseURL, memoryID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("DELETE /v1/memories/%s: %w", memoryID, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("memory not found: %s", memoryID)
	default:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("DELETE /v1/memories/%s: status %d: %s", memoryID, resp.StatusCode, body)
	}
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
