// Package embed provides the Embedder interface and Ollama implementation.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Embedder produces vectors for text inputs.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dim() int
}

// Config holds configuration for the OllamaEmbedder.
type Config struct {
	BaseURL string        // e.g. "http://ollama:11434"
	Model   string        // e.g. "nomic-embed-text"
	Dim     int           // expected vector dimension e.g. 768
	Batch   int           // max texts per HTTP request, e.g. 32
	Timeout time.Duration // per-request timeout
	Retries int           // max retries per batch (3)
}

// circuitState represents the state of the circuit breaker.
type circuitState int

const (
	circuitClosed   circuitState = iota // normal operation
	circuitOpen                         // blocking all requests
	circuitHalfOpen                     // allowing one probe
)

const (
	failureThreshold = 10
	openDuration     = 30 * time.Second
	maxBackoff       = 5 * time.Second
	baseBackoff      = 100 * time.Millisecond
)

// OllamaEmbedder calls Ollama's /api/embed endpoint to produce embeddings.
type OllamaEmbedder struct {
	cfg     Config
	client  *http.Client
	nowFunc func() time.Time

	mu           sync.Mutex
	state        circuitState
	failures     int
	openedAt     time.Time
}

// NewOllamaEmbedder constructs an OllamaEmbedder with the given config.
func NewOllamaEmbedder(cfg Config) *OllamaEmbedder {
	return &OllamaEmbedder{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		nowFunc: time.Now,
	}
}

// Dim returns the expected embedding dimension.
func (o *OllamaEmbedder) Dim() int {
	return o.cfg.Dim
}

// Embed embeds the given texts by splitting into batches and calling Ollama.
func (o *OllamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := o.checkCircuit(); err != nil {
		return nil, err
	}

	batchSize := o.cfg.Batch
	if batchSize <= 0 {
		batchSize = 32
	}

	var results [][]float32
	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[i:end]

		embeddings, err := o.embedBatch(ctx, batch)
		if err != nil {
			o.recordFailure()
			return nil, err
		}
		o.recordSuccess()
		results = append(results, embeddings...)
	}
	return results, nil
}

// embedBatch sends one batch with retry+exponential backoff.
func (o *OllamaEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	retries := o.cfg.Retries
	if retries <= 0 {
		retries = 1
	}

	var lastErr error
	for attempt := 0; attempt < retries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * baseBackoff // 100ms, 200ms, 400ms, …
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		embeddings, retry, err := o.doRequest(ctx, texts)
		if err == nil {
			return embeddings, nil
		}
		lastErr = err
		if !retry {
			return nil, lastErr
		}
	}
	return nil, lastErr
}

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// doRequest performs a single HTTP request. Returns (embeddings, shouldRetry, error).
func (o *OllamaEmbedder) doRequest(ctx context.Context, texts []string) ([][]float32, bool, error) {
	body, err := json.Marshal(embedRequest{Model: o.cfg.Model, Input: texts})
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.cfg.BaseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, false, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		// Network error — retry
		return nil, true, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		return nil, true, fmt.Errorf("server error: HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		return nil, false, fmt.Errorf("client error: HTTP %d", resp.StatusCode)
	}

	var result embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, true, fmt.Errorf("decode response: %w", err)
	}
	return result.Embeddings, false, nil
}

// checkCircuit checks the circuit breaker state and returns an error if open.
func (o *OllamaEmbedder) checkCircuit() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	switch o.state {
	case circuitOpen:
		if o.nowFunc().Sub(o.openedAt) >= openDuration {
			// Transition to half-open to allow one probe
			o.state = circuitHalfOpen
			return nil
		}
		return errors.New("embed: circuit breaker open")
	case circuitHalfOpen:
		// Allow the probe through (state stays half-open until recordSuccess/Failure)
		return nil
	default:
		return nil
	}
}

// recordSuccess resets the failure counter and closes the circuit.
func (o *OllamaEmbedder) recordSuccess() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.failures = 0
	o.state = circuitClosed
}

// recordFailure increments the failure counter and may open the circuit.
func (o *OllamaEmbedder) recordFailure() {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.failures++
	if o.state == circuitHalfOpen || o.failures >= failureThreshold {
		o.state = circuitOpen
		o.openedAt = o.nowFunc()
	}
}
