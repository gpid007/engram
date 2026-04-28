//go:build !crossenc

package rerank

import (
	"context"
	"errors"
	"time"
)

// ErrCrossEncNotBuilt is returned when the binary was not built with the crossenc tag.
var ErrCrossEncNotBuilt = errors.New("cross-encoder not available: rebuild with -tags crossenc")

// CrossEncoderReranker is a stub that satisfies the Reranker interface when
// the crossenc build tag is absent.
type CrossEncoderReranker struct{}

// NewCrossEncoderReranker always returns ErrCrossEncNotBuilt in stub builds.
func NewCrossEncoderReranker(modelPath string, timeout time.Duration) (*CrossEncoderReranker, error) {
	return nil, ErrCrossEncNotBuilt
}

// Rerank always returns ErrCrossEncNotBuilt in stub builds.
func (c *CrossEncoderReranker) Rerank(_ context.Context, _ string, candidates []Candidate) ([]Candidate, error) {
	return nil, ErrCrossEncNotBuilt
}
