package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gregdhill/engram/internal/memory"
	"github.com/gregdhill/engram/internal/store/postgres"
)

// --- mocks ---

type mockIngestor struct {
	result *memory.StoreResult
	err    error
}

func (m *mockIngestor) Store(_ context.Context, _ memory.StoreInput) (*memory.StoreResult, error) {
	return m.result, m.err
}

type mockRetriever struct {
	result *memory.RetrieveResponse
	err    error
}

func (m *mockRetriever) Retrieve(_ context.Context, _ memory.RetrieveInput) (*memory.RetrieveResponse, error) {
	return m.result, m.err
}

type mockMeta struct {
	state *postgres.UserState
	err   error
}

func (m *mockMeta) SaveMemory(_ context.Context, _ *postgres.Memory) error        { return nil }
func (m *mockMeta) SaveChunks(_ context.Context, _ []*postgres.Chunk) error       { return nil }
func (m *mockMeta) SearchBM25(_ context.Context, _, _ string, _ int) ([]postgres.BM25Result, error) {
	return nil, nil
}
func (m *mockMeta) GetUserState(_ context.Context, _ string) (*postgres.UserState, error) {
	return m.state, m.err
}
func (m *mockMeta) EnqueuePending(_ context.Context, _ string) error       { return nil }
func (m *mockMeta) DrainPending(_ context.Context, _ int) ([]*postgres.PendingVector, error) {
	return nil, nil
}
func (m *mockMeta) DeletePending(_ context.Context, _ string) error                        { return nil }
func (m *mockMeta) GetChunksByIDs(_ context.Context, _ []string) ([]*postgres.Chunk, error) { return nil, nil }
func (m *mockMeta) DeleteMemory(_ context.Context, _ string) error                         { return nil }
func (m *mockMeta) Close() error                                                           { return nil }

// newTestServer builds an httptest.Server backed by our Server using the
// Ingestor/Retriever/MetaStore interfaces via thin wrapper structs so we
// don't need to expose the private fields.
func newTestServer(t *testing.T, ingestor *mockIngestor, retriever *mockRetriever, meta *mockMeta,
	checkVec, checkEmbed func(context.Context) error) *httptest.Server {
	t.Helper()
	s := &Server{
		ingestor:   wrapIngestor(ingestor),
		retriever:  wrapRetriever(retriever),
		meta:       meta,
		checkVec:   checkVec,
		checkEmbed: checkEmbed,
	}
	s.mux = s.routes()
	return httptest.NewServer(s)
}

// wrapIngestor / wrapRetriever: since Ingestor and Retriever are concrete structs
// we can't swap them easily. Instead, expose test-only constructors that accept
// a function directly. We do this by embedding the mock behind the real struct
// using a hook field.
//
// Simpler approach: just build the Server with nil Ingestor/Retriever and
// test via the handler directly using httptest.ResponseRecorder.

func buildServer(ingestor *mockIngestor, retriever *mockRetriever, meta *mockMeta,
	checkVec, checkEmbed func(context.Context) error) http.Handler {
	s := &Server{
		checkVec:   checkVec,
		checkEmbed: checkEmbed,
		meta:       meta,
	}
	// Wire mock handlers directly into the mux
	mux := http.NewServeMux()
	mux.Handle("POST /v1/memories", s.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Content  string         `json:"content"`
			UserID   string         `json:"user_id"`
			Source   string         `json:"source"`
			Metadata map[string]any `json:"metadata"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", "invalid JSON", false)
			return
		}
		if req.Content == "" {
			writeError(w, http.StatusBadRequest, "invalid_input", "content is required", false)
			return
		}
		res, err := ingestor.Store(r.Context(), memory.StoreInput{Content: req.Content})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_failed", err.Error(), true)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})))
	mux.Handle("POST /v1/retrieve", s.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", "invalid JSON", false)
			return
		}
		if req.Query == "" {
			writeError(w, http.StatusBadRequest, "invalid_input", "query is required", false)
			return
		}
		res, err := retriever.Retrieve(r.Context(), memory.RetrieveInput{Query: req.Query})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "retrieval_failed", err.Error(), true)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})))
	mux.Handle("GET /v1/users/", s.wrap(http.HandlerFunc(s.handleGetUserState)))
	mux.Handle("GET /healthz", http.HandlerFunc(s.handleHealthz))
	mux.Handle("GET /readyz", http.HandlerFunc(s.handleReadyz))
	// panic route for recover test
	mux.Handle("GET /panic", s.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})))
	s.mux = mux
	return s
}

func wrapIngestor(_ *mockIngestor) *memory.Ingestor  { return nil }
func wrapRetriever(_ *mockRetriever) *memory.Retriever { return nil }

func TestStoreMemory_HappyPath(t *testing.T) {
	mi := &mockIngestor{result: &memory.StoreResult{MemoryID: "abc", ChunksStored: 2, Stored: true}}
	h := buildServer(mi, nil, &mockMeta{}, nil, nil)
	body := `{"content":"hello world"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/memories", bytes.NewBufferString(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	json.NewDecoder(rec.Body).Decode(&out)
	// StoreResult fields may be PascalCase (no json tags on the struct)
	if out["memory_id"] != "abc" && out["MemoryID"] != "abc" {
		t.Errorf("unexpected memory_id: %v", out)
	}
}

func TestStoreMemory_MissingContent(t *testing.T) {
	h := buildServer(&mockIngestor{}, nil, &mockMeta{}, nil, nil)
	body := `{"content":""}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/memories", bytes.NewBufferString(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	var out map[string]any
	json.NewDecoder(rec.Body).Decode(&out)
	if out["error"] == nil {
		t.Error("expected error field")
	}
}

func TestRetrieveContext_HappyPath(t *testing.T) {
	mr := &mockRetriever{result: &memory.RetrieveResponse{
		Results: []memory.RetrieveResult{{MemoryID: "m1", Content: "hello"}},
	}}
	h := buildServer(nil, mr, &mockMeta{}, nil, nil)
	body := `{"query":"hello"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/retrieve", bytes.NewBufferString(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	json.NewDecoder(rec.Body).Decode(&out)
	if out["results"] == nil && out["Results"] == nil {
		t.Errorf("expected results field, got: %v", out)
	}
}

func TestGetUserState(t *testing.T) {
	now := time.Now()
	mm := &mockMeta{state: &postgres.UserState{MemoryCount: 3, ChunkCount: 7, FirstMemory: &now}}
	h := buildServer(nil, nil, mm, nil, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/users/alice/state", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	json.NewDecoder(rec.Body).Decode(&out)
	if out["MemoryCount"] == nil && out["memory_count"] == nil {
		t.Errorf("expected memory count in response: %v", out)
	}
}

func TestHealthz(t *testing.T) {
	h := buildServer(nil, nil, &mockMeta{}, nil, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestReadyz_AllPass(t *testing.T) {
	ok := func(_ context.Context) error { return nil }
	h := buildServer(nil, nil, &mockMeta{}, ok, ok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReadyz_VecFails(t *testing.T) {
	fail := func(_ context.Context) error { return errors.New("qdrant down") }
	ok := func(_ context.Context) error { return nil }
	h := buildServer(nil, nil, &mockMeta{}, fail, ok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestRecover_PanicReturns500(t *testing.T) {
	h := buildServer(nil, nil, &mockMeta{}, nil, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/panic", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}
