package graph

import "context"

// NopStore is a no-op GraphStore that always returns an empty slice and
// silently accepts writes. Use this when graph expansion is not configured.
type NopStore struct{}

// Ensure NopStore implements GraphStore at compile time.
var _ GraphStore = NopStore{}

func (NopStore) ExpandRelated(_ context.Context, _ []ChunkID, _ int) ([]ChunkID, error) {
	return nil, nil
}

func (NopStore) ExpandRelatedWithOptions(_ context.Context, _ []ChunkID, _ int, _ ExpandOptions) ([]ChunkID, error) {
	return nil, nil
}

func (NopStore) WriteChunkAndOf(_ context.Context, _ ChunkNode) error {
	return nil
}

func (NopStore) WriteSequentialEdges(_ context.Context, _ []SequentialEdge) error {
	return nil
}

func (NopStore) WriteSimilarEdge(_ context.Context, _ SimilarEdge) error {
	return nil
}

func (NopStore) Close(_ context.Context) error {
	return nil
}
