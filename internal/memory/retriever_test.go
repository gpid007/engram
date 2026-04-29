package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gregdhill/engram/internal/rerank"
	"github.com/gregdhill/engram/internal/store/postgres"
	"github.com/gregdhill/engram/internal/store/qdrant"
)

// --- retriever-specific mocks ---

type rmockMeta struct {
	bm25Res    []postgres.BM25Result
	bm25Err    error
	bm25Sleep  time.Duration
	gotUserID  string
	bm25Calls  int
}

func (m *rmockMeta) SaveMemory(context.Context, *postgres.Memory) error    { return nil }
func (m *rmockMeta) SaveChunks(context.Context, []*postgres.Chunk) error   { return nil }
func (m *rmockMeta) SearchBM25(_ context.Context, userID, _ string, _ int) ([]postgres.BM25Result, error) {
	m.bm25Calls++
	m.gotUserID = userID
	if m.bm25Sleep > 0 {
		time.Sleep(m.bm25Sleep)
	}
	return m.bm25Res, m.bm25Err
}
func (m *rmockMeta) GetUserState(context.Context, string) (*postgres.UserState, error) {
	return nil, nil
}
func (m *rmockMeta) EnqueuePending(context.Context, string) error                 { return nil }
func (m *rmockMeta) DrainPending(context.Context, int) ([]*postgres.PendingVector, error) {
	return nil, nil
}
func (m *rmockMeta) DeletePending(context.Context, string) error                       { return nil }
func (m *rmockMeta) GetChunksByIDs(_ context.Context, _ []string) ([]*postgres.Chunk, error) { return nil, nil }
func (m *rmockMeta) DeleteMemory(_ context.Context, _ string) error                    { return nil }
func (m *rmockMeta) Close() error                                                      { return nil }

type rmockVec struct {
	res       []qdrant.SearchResult
	err       error
	sleep     time.Duration
	gotUserID string
	calls     int
}

func (v *rmockVec) EnsureCollection(context.Context, uint64) error { return nil }
func (v *rmockVec) Upsert(context.Context, []qdrant.Point) error   { return nil }
func (v *rmockVec) Search(_ context.Context, _ []float32, _ uint64, userID string) ([]qdrant.SearchResult, error) {
	v.calls++
	v.gotUserID = userID
	if v.sleep > 0 {
		time.Sleep(v.sleep)
	}
	return v.res, v.err
}
func (v *rmockVec) DeleteByMemoryID(_ context.Context, _ string) error { return nil }
func (v *rmockVec) Close() error                                       { return nil }

type rmockEmbedder struct {
	dim   int
	sleep time.Duration
	err   error
}

func (e *rmockEmbedder) Dim() int { return e.dim }

func (e *rmockEmbedder) embed(_ context.Context, texts []string) ([][]float32, error) {
	if e.sleep > 0 {
		time.Sleep(e.sleep)
	}
	if e.err != nil {
		return nil, e.err
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		v := make([]float32, e.dim)
		for j := range v {
			v[j] = 0.1
		}
		out[i] = v
	}
	return out, nil
}

func (e *rmockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return e.embed(ctx, texts)
}

func (e *rmockEmbedder) EmbedQuery(ctx context.Context, texts []string) ([][]float32, error) {
	return e.embed(ctx, texts)
}

type rmockReranker struct {
	called bool
	err    error
	// reverseOrder reverses the candidate list when true.
	reverseOrder bool
}

func (r *rmockReranker) Rerank(_ context.Context, _ string, cands []rerank.Candidate) ([]rerank.Candidate, error) {
	r.called = true
	if r.err != nil {
		return nil, r.err
	}
	if !r.reverseOrder {
		return cands, nil
	}
	out := make([]rerank.Candidate, len(cands))
	for i, c := range cands {
		out[len(cands)-1-i] = c
	}
	return out, nil
}

// --- helpers ---

func newTestRetriever(meta postgres.MetaStore, vec qdrant.VectorStore, rr *rmockReranker, cfg RetrieverConfig) *Retriever {
	var rIface rerank.Reranker
	if rr != nil {
		rIface = rr
	}
	return NewRetriever(meta, vec, &rmockEmbedder{dim: 8}, rIface, nil, cfg)
}

// --- tests ---

func TestRetrieve_HappyPath(t *testing.T) {
	meta := &rmockMeta{bm25Res: []postgres.BM25Result{
		{ChunkID: "c1", MemoryID: "m1", Content: "hello world", Rank: 0.9},
		{ChunkID: "c2", MemoryID: "m2", Content: "foo bar", Rank: 0.5},
	}}
	vec := &rmockVec{res: []qdrant.SearchResult{
		{ID: "c1", Score: 0.9, Payload: map[string]any{"memory_id": "m1", "source": "src1", "created_at": "2024-01-01T00:00:00Z"}},
		{ID: "c3", Score: 0.7, Payload: map[string]any{"memory_id": "m3", "source": "src3", "created_at": "2024-01-02T00:00:00Z"}},
	}}
	r := newTestRetriever(meta, vec, nil, RetrieverConfig{})
	resp, err := r.Retrieve(context.Background(), RetrieveInput{Query: "test", UserID: "u1", K: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatalf("expected results, got 0")
	}
	if resp.Stats.Degraded {
		t.Errorf("expected Degraded=false")
	}
	if !resp.Stats.RerankSkipped {
		t.Errorf("expected RerankSkipped=true (no reranker)")
	}
	// c1 should have content from BM25.
	var found bool
	for _, res := range resp.Results {
		if res.ChunkID == "c1" {
			found = true
			if res.Content != "hello world" {
				t.Errorf("c1 content: got %q want %q", res.Content, "hello world")
			}
			if res.Source != "src1" {
				t.Errorf("c1 source: got %q want %q", res.Source, "src1")
			}
			if res.CreatedAt != "2024-01-01T00:00:00Z" {
				t.Errorf("c1 created_at: got %q", res.CreatedAt)
			}
		}
	}
	if !found {
		t.Errorf("expected c1 in results")
	}
}

func TestRetrieve_VecFailure_Degraded(t *testing.T) {
	meta := &rmockMeta{bm25Res: []postgres.BM25Result{
		{ChunkID: "c1", MemoryID: "m1", Content: "x", Rank: 0.5},
	}}
	vec := &rmockVec{err: errors.New("boom")}
	r := newTestRetriever(meta, vec, nil, RetrieverConfig{})
	resp, err := r.Retrieve(context.Background(), RetrieveInput{Query: "q", K: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Stats.Degraded {
		t.Errorf("expected Degraded=true")
	}
	if len(resp.Results) != 1 || resp.Results[0].ChunkID != "c1" {
		t.Errorf("expected single result c1, got %+v", resp.Results)
	}
}

func TestRetrieve_BM25Failure_HardError(t *testing.T) {
	meta := &rmockMeta{bm25Err: errors.New("db down")}
	vec := &rmockVec{}
	r := newTestRetriever(meta, vec, nil, RetrieverConfig{})
	_, err := r.Retrieve(context.Background(), RetrieveInput{Query: "q", K: 5})
	if err == nil {
		t.Fatal("expected error from BM25 failure")
	}
}

func TestRetrieve_RerankSkippedWhenFewCandidates(t *testing.T) {
	meta := &rmockMeta{bm25Res: []postgres.BM25Result{
		{ChunkID: "c1", MemoryID: "m1", Content: "a", Rank: 0.9},
		{ChunkID: "c2", MemoryID: "m2", Content: "b", Rank: 0.5},
		{ChunkID: "c3", MemoryID: "m3", Content: "c", Rank: 0.3},
	}}
	vec := &rmockVec{}
	rr := &rmockReranker{}
	r := newTestRetriever(meta, vec, rr, RetrieverConfig{FinalK: 5})
	resp, err := r.Retrieve(context.Background(), RetrieveInput{Query: "q", Rerank: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Stats.RerankSkipped {
		t.Errorf("expected RerankSkipped=true (3 candidates <= finalK=5)")
	}
	if rr.called {
		t.Errorf("reranker should not have been called")
	}
}

func TestRetrieve_RerankReorders(t *testing.T) {
	// 6 candidates so > FinalK=2.
	bm25 := make([]postgres.BM25Result, 6)
	for i := 0; i < 6; i++ {
		bm25[i] = postgres.BM25Result{
			ChunkID:  string(rune('a' + i)),
			MemoryID: "m",
			Content:  "x",
			Rank:     float64(6-i) / 10.0,
		}
	}
	meta := &rmockMeta{bm25Res: bm25}
	vec := &rmockVec{}
	rr := &rmockReranker{reverseOrder: true}
	r := newTestRetriever(meta, vec, rr, RetrieverConfig{FinalK: 2})
	resp, err := r.Retrieve(context.Background(), RetrieveInput{Query: "q", Rerank: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rr.called {
		t.Fatalf("expected reranker to be called")
	}
	if resp.Stats.RerankSkipped {
		t.Errorf("expected RerankSkipped=false")
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	// reverseOrder reverses fused list; first result should be the last fused candidate ('f').
	if resp.Results[0].ChunkID != "f" {
		t.Errorf("expected reversed order, first=%q", resp.Results[0].ChunkID)
	}
}

func TestRetrieve_EmptyResults(t *testing.T) {
	meta := &rmockMeta{}
	vec := &rmockVec{}
	r := newTestRetriever(meta, vec, nil, RetrieverConfig{})
	resp, err := r.Retrieve(context.Background(), RetrieveInput{Query: "q"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Errorf("expected empty results, got %d", len(resp.Results))
	}
}

func TestRetrieve_UserIDDefaults(t *testing.T) {
	meta := &rmockMeta{}
	vec := &rmockVec{}
	r := newTestRetriever(meta, vec, nil, RetrieverConfig{})
	_, err := r.Retrieve(context.Background(), RetrieveInput{Query: "q"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.gotUserID != "default" {
		t.Errorf("BM25 userID: got %q want %q", meta.gotUserID, "default")
	}
	if vec.gotUserID != "default" {
		t.Errorf("vec userID: got %q want %q", vec.gotUserID, "default")
	}
}

func TestRetrieve_TimingStatsPopulated(t *testing.T) {
	meta := &rmockMeta{
		bm25Res:   []postgres.BM25Result{{ChunkID: "c1", MemoryID: "m1", Content: "x", Rank: 0.5}},
		bm25Sleep: 2 * time.Millisecond,
	}
	vec := &rmockVec{
		res:   []qdrant.SearchResult{{ID: "c1", Score: 0.9, Payload: map[string]any{"memory_id": "m1"}}},
		sleep: 2 * time.Millisecond,
	}
	r := NewRetriever(meta, vec, &rmockEmbedder{dim: 8, sleep: 2 * time.Millisecond}, nil, nil, RetrieverConfig{})
	resp, err := r.Retrieve(context.Background(), RetrieveInput{Query: "q"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Stats.VecMs <= 0 {
		t.Errorf("expected VecMs > 0, got %d", resp.Stats.VecMs)
	}
	if resp.Stats.BM25Ms <= 0 {
		t.Errorf("expected BM25Ms > 0, got %d", resp.Stats.BM25Ms)
	}
	if resp.Stats.TotalMs <= 0 {
		t.Errorf("expected TotalMs > 0, got %d", resp.Stats.TotalMs)
	}
	// FusionMs may be 0 since the fusion step is fast. Just ensure it's >= 0 (always true).
}
