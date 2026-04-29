package memory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gregdhill/engram/internal/chunk"
	"github.com/gregdhill/engram/internal/store/postgres"
	"github.com/gregdhill/engram/internal/store/qdrant"
)

// --- mocks ---

type mockEmbedder struct {
	dim int
}

func (m *mockEmbedder) Dim() int { return m.dim }

func (m *mockEmbedder) embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, m.dim)
		// Deterministic distinct vectors so the chunker creates separate chunks
		// when sentences differ.
		for j := 0; j < m.dim; j++ {
			v[j] = float32((int(t[0])+j)%7) * 0.1
		}
		out[i] = v
	}
	return out, nil
}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return m.embed(ctx, texts)
}

func (m *mockEmbedder) EmbedQuery(ctx context.Context, texts []string) ([][]float32, error) {
	return m.embed(ctx, texts)
}

type mockMeta struct {
	saveMemoryCalls  int
	savedMemory      *postgres.Memory
	saveChunksCalls  int
	savedChunks      [][]*postgres.Chunk
	saveChunksErr    error
	enqueued         []string
	enqueuePendingErr error
}

func (m *mockMeta) SaveMemory(_ context.Context, mem *postgres.Memory) error {
	m.saveMemoryCalls++
	m.savedMemory = mem
	return nil
}
func (m *mockMeta) SaveChunks(_ context.Context, chunks []*postgres.Chunk) error {
	m.saveChunksCalls++
	m.savedChunks = append(m.savedChunks, chunks)
	return m.saveChunksErr
}
func (m *mockMeta) SearchBM25(context.Context, string, string, int) ([]postgres.BM25Result, error) {
	return nil, nil
}
func (m *mockMeta) GetUserState(context.Context, string) (*postgres.UserState, error) {
	return nil, nil
}
func (m *mockMeta) EnqueuePending(_ context.Context, chunkID string) error {
	m.enqueued = append(m.enqueued, chunkID)
	return m.enqueuePendingErr
}
func (m *mockMeta) DrainPending(context.Context, int) ([]*postgres.PendingVector, error) {
	return nil, nil
}
func (m *mockMeta) DeletePending(context.Context, string) error                       { return nil }
func (m *mockMeta) GetChunksByIDs(_ context.Context, _ []string) ([]*postgres.Chunk, error) { return nil, nil }
func (m *mockMeta) DeleteMemory(_ context.Context, _ string) error                    { return nil }
func (m *mockMeta) Close() error                                                      { return nil }

type mockVec struct {
	upsertCalls int
	upserted    [][]qdrant.Point
	upsertErr   error
}

func (v *mockVec) EnsureCollection(context.Context, uint64) error { return nil }
func (v *mockVec) Upsert(_ context.Context, points []qdrant.Point) error {
	v.upsertCalls++
	v.upserted = append(v.upserted, points)
	return v.upsertErr
}
func (v *mockVec) Search(context.Context, []float32, uint64, string) ([]qdrant.SearchResult, error) {
	return nil, nil
}
func (v *mockVec) DeleteByMemoryID(_ context.Context, _ string) error { return nil }
func (v *mockVec) Close() error                                       { return nil }

// --- helpers ---

// newIngestorWithChunks builds an Ingestor whose chunker produces a fixed
// number of chunks (the input string contains N period-terminated sentences).
func newTestIngestor(meta postgres.MetaStore, vec qdrant.VectorStore) *Ingestor {
	emb := &mockEmbedder{dim: 4}
	// MinTokens=1 so we don't merge tiny single-sentence chunks; threshold=2
	// (impossible cosine) forces every sentence into its own chunk.
	chunker := chunk.NewChunker(emb, chunk.Config{
		MaxTokens:           512,
		MinTokens:           1,
		SimilarityThreshold: 2.0,
	})
	return NewIngestor(meta, vec, chunker, IngestorOptions{})
}

func sentences(n int) string {
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		// Use varied first letters so mockEmbedder produces different vectors.
		parts[i] = string(rune('a'+i%26)) + "lpha bravo charlie delta echo."
	}
	return strings.Join(parts, " ")
}

// --- tests ---

func TestStore_HappyPath(t *testing.T) {
	meta := &mockMeta{}
	vec := &mockVec{}
	ing := newTestIngestor(meta, vec)

	res, err := ing.Store(context.Background(), StoreInput{
		Content: sentences(2),
		UserID:  "user1",
		Source:  "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Stored {
		t.Errorf("Stored=false, want true")
	}
	if res.ChunksStored != 2 {
		t.Errorf("ChunksStored=%d, want 2", res.ChunksStored)
	}
	if res.ChunksDeduped != 0 {
		t.Errorf("ChunksDeduped=%d, want 0", res.ChunksDeduped)
	}
	if res.MemoryID == "" {
		t.Errorf("MemoryID empty")
	}
	if vec.upsertCalls != 1 {
		t.Errorf("Upsert calls=%d, want 1", vec.upsertCalls)
	}
	if len(vec.upserted[0]) != 2 {
		t.Errorf("upserted points=%d, want 2", len(vec.upserted[0]))
	}
	// Verify payload fields.
	p := vec.upserted[0][0]
	for _, k := range []string{"memory_id", "chunk_id", "user_id", "ord", "source", "importance", "created_at"} {
		if _, ok := p.Payload[k]; !ok {
			t.Errorf("payload missing key %q", k)
		}
	}
	if p.Payload["user_id"] != "user1" {
		t.Errorf("payload user_id=%v, want user1", p.Payload["user_id"])
	}
}

func TestStore_AllDeduped(t *testing.T) {
	meta := &mockMeta{saveChunksErr: postgres.ErrDuplicate}
	vec := &mockVec{}
	ing := newTestIngestor(meta, vec)

	res, err := ing.Store(context.Background(), StoreInput{Content: sentences(3)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Stored {
		t.Errorf("Stored=true, want false")
	}
	if res.ChunksStored != 0 {
		t.Errorf("ChunksStored=%d, want 0", res.ChunksStored)
	}
	if res.ChunksDeduped != 3 {
		t.Errorf("ChunksDeduped=%d, want 3", res.ChunksDeduped)
	}
	if vec.upsertCalls != 0 {
		t.Errorf("Upsert called %d times on dedup, want 0", vec.upsertCalls)
	}
}

func TestStore_QdrantFailure_EnqueuesPending(t *testing.T) {
	meta := &mockMeta{}
	vec := &mockVec{upsertErr: errors.New("qdrant down")}
	ing := newTestIngestor(meta, vec)

	res, err := ing.Store(context.Background(), StoreInput{Content: sentences(2)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Stored {
		t.Errorf("Stored=false, want true (chunks persisted)")
	}
	if len(meta.enqueued) != 2 {
		t.Errorf("enqueued=%d, want 2", len(meta.enqueued))
	}
}

func TestStore_EmptyUserIDDefaults(t *testing.T) {
	meta := &mockMeta{}
	vec := &mockVec{}
	ing := newTestIngestor(meta, vec)

	_, err := ing.Store(context.Background(), StoreInput{Content: sentences(1), UserID: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.savedMemory == nil {
		t.Fatal("memory not saved")
	}
	if meta.savedMemory.UserID != "default" {
		t.Errorf("UserID=%q, want %q", meta.savedMemory.UserID, "default")
	}
}

func TestStore_ImportanceHeuristic(t *testing.T) {
	cases := []struct {
		nChunks int
		want    float32
	}{
		{1, 0.4},
		{7, 1.0},
	}
	for _, tc := range cases {
		meta := &mockMeta{}
		vec := &mockVec{}
		ing := newTestIngestor(meta, vec)

		_, err := ing.Store(context.Background(), StoreInput{Content: sentences(tc.nChunks)})
		if err != nil {
			t.Fatalf("n=%d: unexpected error: %v", tc.nChunks, err)
		}
		if meta.savedMemory == nil {
			t.Fatalf("n=%d: memory not saved", tc.nChunks)
		}
		got := meta.savedMemory.Importance
		// Allow tiny float error.
		if got < tc.want-0.001 || got > tc.want+0.001 {
			t.Errorf("n=%d: importance=%v, want %v", tc.nChunks, got, tc.want)
		}
	}
}

func TestStore_NormalizesWhitespace(t *testing.T) {
	meta := &mockMeta{}
	vec := &mockVec{}
	ing := newTestIngestor(meta, vec)

	raw := "   " + sentences(1) + "   \n\t"
	_, err := ing.Store(context.Background(), StoreInput{Content: raw})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.savedMemory == nil {
		t.Fatal("memory not saved")
	}
	if strings.HasPrefix(meta.savedMemory.Content, " ") || strings.HasSuffix(meta.savedMemory.Content, " ") {
		t.Errorf("memory content not trimmed: %q", meta.savedMemory.Content)
	}
	if meta.savedMemory.Content != strings.TrimSpace(raw) {
		t.Errorf("content=%q, want trimmed", meta.savedMemory.Content)
	}
}
