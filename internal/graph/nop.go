package graph

import "context"

// NopStore is a no-op GraphStore that always returns an empty slice.
// Use this when graph expansion is not configured.
type NopStore struct{}

// Ensure NopStore implements GraphStore at compile time.
var _ GraphStore = NopStore{}

func (NopStore) ExpandRelated(_ context.Context, _ []ChunkID, _ int) ([]ChunkID, error) {
	return nil, nil
}
