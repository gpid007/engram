// Package rerank provides the Reranker interface and implementations.
package rerank

import (
	"context"
	"errors"
)

// ErrNonRetryable indicates a permanent failure (e.g. HTTP 4xx).
var ErrNonRetryable = errors.New("non-retryable rerank error")

// ErrRetryable indicates a transient failure (e.g. HTTP 5xx).
var ErrRetryable = errors.New("retryable rerank error")

// Candidate is a document to be reranked.
type Candidate struct {
	ChunkID  string
	MemoryID string
	Content  string
	Score    float64 // fused score from RRF
}

// Reranker reorders candidates by relevance to a query.
type Reranker interface {
	Rerank(ctx context.Context, query string, candidates []Candidate) ([]Candidate, error)
}
