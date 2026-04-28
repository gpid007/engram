package graph

import (
	"context"
	"testing"
)

// Compile-time interface check.
var _ GraphStore = NopStore{}

func TestNopStore(t *testing.T) {
	var s NopStore
	got, err := s.ExpandRelated(context.Background(), []ChunkID{"a", "b"}, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}
