package graph

import (
	"context"
	"testing"
	"time"
)

// recordingStore implements GraphStore and records every call. Used for
// verifying that the Ingestor and Retriever invoke the expected methods.
type recordingStore struct {
	Chunks         []ChunkNode
	Sequentials    [][]SequentialEdge
	Similars       []SimilarEdge
	ExpandCalls    int
	ExpandOptsLast ExpandOptions
	ExpandResult   []ChunkID
	Closed         bool
}

func (r *recordingStore) ExpandRelated(ctx context.Context, ids []ChunkID, depth int) ([]ChunkID, error) {
	return r.ExpandRelatedWithOptions(ctx, ids, depth, ExpandOptions{})
}

func (r *recordingStore) ExpandRelatedWithOptions(_ context.Context, _ []ChunkID, _ int, opts ExpandOptions) ([]ChunkID, error) {
	r.ExpandCalls++
	r.ExpandOptsLast = opts
	return r.ExpandResult, nil
}

func (r *recordingStore) WriteChunkAndOf(_ context.Context, n ChunkNode) error {
	r.Chunks = append(r.Chunks, n)
	return nil
}

func (r *recordingStore) WriteSequentialEdges(_ context.Context, edges []SequentialEdge) error {
	r.Sequentials = append(r.Sequentials, edges)
	return nil
}

func (r *recordingStore) WriteSimilarEdge(_ context.Context, e SimilarEdge) error {
	r.Similars = append(r.Similars, e)
	return nil
}

func (r *recordingStore) Close(_ context.Context) error {
	r.Closed = true
	return nil
}

var _ GraphStore = (*recordingStore)(nil)

func TestNopStore_AllMethodsReturnNoError(t *testing.T) {
	var s NopStore
	ctx := context.Background()

	ids, err := s.ExpandRelated(ctx, []ChunkID{"a"}, 1)
	if err != nil || len(ids) != 0 {
		t.Fatalf("ExpandRelated: got (%v, %v), want (nil, nil)", ids, err)
	}

	ids, err = s.ExpandRelatedWithOptions(ctx, []ChunkID{"a"}, 1, ExpandOptions{UserID: "u", MaxExpand: 5})
	if err != nil || len(ids) != 0 {
		t.Fatalf("ExpandRelatedWithOptions: got (%v, %v), want (nil, nil)", ids, err)
	}

	if err := s.WriteChunkAndOf(ctx, ChunkNode{ID: "c1", UserID: "u", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("WriteChunkAndOf: %v", err)
	}
	if err := s.WriteSequentialEdges(ctx, []SequentialEdge{{PrevID: "a", NextID: "b", UserID: "u"}}); err != nil {
		t.Fatalf("WriteSequentialEdges: %v", err)
	}
	if err := s.WriteSimilarEdge(ctx, SimilarEdge{AID: "a", BID: "b", UserID: "u", Score: 0.9}); err != nil {
		t.Fatalf("WriteSimilarEdge: %v", err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestRecordingStore_ImplementsGraphStore(t *testing.T) {
	var _ GraphStore = (*recordingStore)(nil)
}
