//go:build integration

// Package integration contains end-to-end tests for the Engram stack.
//
// These tests boot real Postgres and Qdrant containers via testcontainers
// and use a fake Ollama HTTP server (httptest) for embeddings/chat. The
// containers are shared across tests in this package via TestMain.
//
// Run with:
//
//	DOCKER_HOST="unix://${HOME}/.colima/default/docker.sock" \
//	TESTCONTAINERS_RYUK_DISABLED=true \
//	go test -tags integration ./test/integration/... -v
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gregdhill/engram/internal/chunk"
	"github.com/gregdhill/engram/internal/embed"
	"github.com/gregdhill/engram/internal/graph"
	"github.com/gregdhill/engram/internal/httpapi"
	"github.com/gregdhill/engram/internal/mcp"
	"github.com/gregdhill/engram/internal/memory"
	"github.com/gregdhill/engram/internal/rerank"
	pgstore "github.com/gregdhill/engram/internal/store/postgres"
	qdrantstore "github.com/gregdhill/engram/internal/store/qdrant"
)

// ---------------------------------------------------------------------------
// Shared container state (TestMain)
// ---------------------------------------------------------------------------

const (
	embedDim         = 768
	qdrantCollection = "engram_integration"
)

var (
	pgDSN      string
	qdrantAddr string

	pgContainer     testcontainers.Container
	qdrantContainer testcontainers.Container
)

// TestMain spins up Postgres and Qdrant once for the whole package.
func TestMain(m *testing.M) {
	// Colima Docker socket on macOS dev machine; CI may already set DOCKER_HOST.
	if os.Getenv("DOCKER_HOST") == "" {
		_ = os.Setenv("DOCKER_HOST", "unix:///"+os.Getenv("HOME")+"/.colima/default/docker.sock")
	}
	_ = os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	ctx := context.Background()

	pgC, dsn, err := startPostgres(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start postgres: %v\n", err)
		os.Exit(1)
	}
	pgContainer = pgC
	pgDSN = dsn

	qC, addr, err := startQdrant(ctx)
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		fmt.Fprintf(os.Stderr, "start qdrant: %v\n", err)
		os.Exit(1)
	}
	qdrantContainer = qC
	qdrantAddr = addr

	code := m.Run()

	_ = qdrantContainer.Terminate(ctx)
	_ = pgContainer.Terminate(ctx)
	os.Exit(code)
}

func startPostgres(ctx context.Context) (testcontainers.Container, string, error) {
	req := testcontainers.ContainerRequest{
		Image:        "postgres:16",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "engram",
			"POSTGRES_PASSWORD": "engram",
			"POSTGRES_DB":       "engram",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, "", err
	}
	host, err := c.Host(ctx)
	if err != nil {
		return c, "", err
	}
	port, err := c.MappedPort(ctx, "5432")
	if err != nil {
		return c, "", err
	}
	dsn := fmt.Sprintf("postgres://engram:engram@%s:%s/engram?sslmode=disable", host, port.Port())
	return c, dsn, nil
}

func startQdrant(ctx context.Context) (testcontainers.Container, string, error) {
	req := testcontainers.ContainerRequest{
		Image:        "qdrant/qdrant:latest",
		ExposedPorts: []string{"6334/tcp"},
		WaitingFor:   wait.ForListeningPort("6334/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, "", err
	}
	host, err := c.Host(ctx)
	if err != nil {
		return c, "", err
	}
	port, err := c.MappedPort(ctx, "6334/tcp")
	if err != nil {
		return c, "", err
	}
	return c, fmt.Sprintf("%s:%s", host, port.Port()), nil
}

// ---------------------------------------------------------------------------
// Fake Ollama server
// ---------------------------------------------------------------------------

// startFakeOllama returns an httptest server that mimics enough of the Ollama
// HTTP API for the integration suite:
//
//   - POST /api/embed — returns a fixed embedDim-element vector for each input
//     (all zeros except index 0 = 1.0). One vector per input.
//   - POST /api/chat  — returns {"message":{"role":"assistant","content":"[5]"}}
//     (single relevance score, the format the LLM-style reranker expects).
func startFakeOllama(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/embed", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Fixed vector, identical for every input — fine for integration testing.
		vec := make([]float32, embedDim)
		vec[0] = 1.0
		out := make([][]float32, len(req.Input))
		for i := range req.Input {
			out[i] = vec
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	})

	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": "[5]",
			},
		})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// ---------------------------------------------------------------------------
// Stack wiring
// ---------------------------------------------------------------------------

// stack groups the components needed to drive ingestion + retrieval.
type stack struct {
	meta      *pgstore.Store
	vec       *qdrantstore.Store
	embedder  embed.Embedder
	chunker   *chunk.Chunker
	ingestor  *memory.Ingestor
	retriever *memory.Retriever
}

// buildStack constructs a fresh wired stack pointing at the given infrastructure.
//
// Each call wipes shared Postgres tables and recreates the Qdrant collection
// state so tests are isolated even though the containers are reused.
func buildStack(t *testing.T, dsn, qaddr, ollamaURL string, retCfg memory.RetrieverConfig) *stack {
	t.Helper()
	ctx := context.Background()

	meta, err := pgstore.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgstore.New: %v", err)
	}
	t.Cleanup(func() { _ = meta.Close() })

	// Now that migrations have run, ensure tables are clean for this test.
	if err := truncateTables(ctx, dsn); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}

	vec, err := qdrantstore.New(ctx, qaddr, qdrantCollection)
	if err != nil {
		t.Fatalf("qdrant.New: %v", err)
	}
	t.Cleanup(func() { _ = vec.Close() })

	if err := vec.EnsureCollection(ctx, embedDim); err != nil {
		t.Fatalf("EnsureCollection: %v", err)
	}

	embedder := embed.NewOllamaEmbedder(embed.Config{
		BaseURL: ollamaURL,
		Model:   "fake-embed",
		Dim:     embedDim,
		Batch:   32,
		Timeout: 5 * time.Second,
		Retries: 1,
	})

	// MinTokens=1 so short test fixtures aren't merged unexpectedly.
	chunker := chunk.NewChunker(embedder, chunk.Config{
		MaxTokens:           512,
		MinTokens:           1,
		SimilarityThreshold: 0.6,
	})

	ing := memory.NewIngestor(meta, vec, chunker)
	ret := memory.NewRetriever(meta, vec, embedder, nil, graph.NopStore{}, retCfg)

	return &stack{
		meta:      meta,
		vec:       vec,
		embedder:  embedder,
		chunker:   chunker,
		ingestor:  ing,
		retriever: ret,
	}
}

// truncateTables clears app data between tests sharing the same Postgres
// instance. It opens a one-shot pgx connection because pgstore.Store doesn't
// expose a generic exec method.
func truncateTables(ctx context.Context, dsn string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx,
		`TRUNCATE TABLE pending_vectors, chunks, memories RESTART IDENTITY CASCADE`)
	return err
}

// emptyMeta is a small helper: postgres.memories.metadata is NOT NULL, so all
// StoreInputs supplied by the tests must carry a non-nil map.
func emptyMeta() map[string]any { return map[string]any{} }

// ---------------------------------------------------------------------------
// Mock vector store for degraded-path testing
// ---------------------------------------------------------------------------

// errVecStore is a VectorStore whose Search always errors. The other methods
// are no-ops so it can be substituted into a Retriever without disturbing
// data already written via the real store.
type errVecStore struct{}

func (errVecStore) EnsureCollection(_ context.Context, _ uint64) error    { return nil }
func (errVecStore) Upsert(_ context.Context, _ []qdrantstore.Point) error { return nil }
func (errVecStore) Search(_ context.Context, _ []float32, _ uint64, _ string) ([]qdrantstore.SearchResult, error) {
	return nil, errors.New("vector store offline")
}
func (errVecStore) Close() error { return nil }

// noopReranker leaves candidates in their input order. Used so we can request
// rerank=true and exercise the "skip due to too few candidates" branch
// without depending on a live LLM.
type noopReranker struct{}

func (noopReranker) Rerank(_ context.Context, _ string, c []rerank.Candidate) ([]rerank.Candidate, error) {
	return c, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestIngestAndRetrieve_Roundtrip(t *testing.T) {
	ctx := context.Background()
	ollama := startFakeOllama(t)
	st := buildStack(t, pgDSN, qdrantAddr, ollama.URL, memory.RetrieverConfig{FinalK: 5})

	memories := []string{
		"Elephants are the largest land animals and live in herds.",
		"Python is a high-level programming language popular for data science.",
		"The Pacific Ocean is the largest and deepest ocean on Earth.",
	}
	for i, content := range memories {
		res, err := st.ingestor.Store(ctx, memory.StoreInput{
			Content:  content,
			UserID:   "alice",
			Source:   fmt.Sprintf("doc-%d", i),
			Metadata: emptyMeta(),
		})
		if err != nil {
			t.Fatalf("Store[%d]: %v", i, err)
		}
		if !res.Stored {
			t.Fatalf("Store[%d]: expected Stored=true, got %+v", i, res)
		}
		if res.ChunksStored == 0 {
			t.Fatalf("Store[%d]: expected ChunksStored>0, got %+v", i, res)
		}
	}

	resp, err := st.retriever.Retrieve(ctx, memory.RetrieveInput{
		Query:  "elephants land animals herds",
		UserID: "alice",
		K:      3,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected at least one result")
	}
	top := strings.ToLower(resp.Results[0].Content)
	if !strings.Contains(top, "elephant") {
		t.Errorf("expected top result to mention elephants, got: %s", resp.Results[0].Content)
	}
}

func TestDeduplication(t *testing.T) {
	ctx := context.Background()
	ollama := startFakeOllama(t)
	st := buildStack(t, pgDSN, qdrantAddr, ollama.URL, memory.RetrieverConfig{FinalK: 5})

	content := "A unique sentence about deduplication semantics."
	first, err := st.ingestor.Store(ctx, memory.StoreInput{
		Content:  content,
		UserID:   "dedup-user",
		Metadata: emptyMeta(),
	})
	if err != nil {
		t.Fatalf("first Store: %v", err)
	}
	if !first.Stored || first.ChunksStored == 0 {
		t.Fatalf("first Store unexpected: %+v", first)
	}

	second, err := st.ingestor.Store(ctx, memory.StoreInput{
		Content:  content,
		UserID:   "dedup-user",
		Metadata: emptyMeta(),
	})
	if err != nil {
		t.Fatalf("second Store: %v", err)
	}
	if second.Stored {
		t.Errorf("expected second Store to be deduped (Stored=false), got %+v", second)
	}
	if second.ChunksDeduped == 0 {
		t.Errorf("expected ChunksDeduped>0 on duplicate, got %+v", second)
	}
}

func TestRetrieve_VecLegDegraded(t *testing.T) {
	ctx := context.Background()
	ollama := startFakeOllama(t)

	// First ingest with a fully working stack so BM25 has something to find.
	st := buildStack(t, pgDSN, qdrantAddr, ollama.URL, memory.RetrieverConfig{FinalK: 5})
	if _, err := st.ingestor.Store(ctx, memory.StoreInput{
		Content:  "Volcanoes erupt molten lava and form new islands over time.",
		UserID:   "deg-user",
		Metadata: emptyMeta(),
	}); err != nil {
		t.Fatalf("Store: %v", err)
	}

	// Build a retriever pointed at the same meta store but with a broken vec leg.
	degraded := memory.NewRetriever(
		st.meta,
		errVecStore{},
		st.embedder,
		nil,
		graph.NopStore{},
		memory.RetrieverConfig{FinalK: 5},
	)

	resp, err := degraded.Retrieve(ctx, memory.RetrieveInput{
		Query:  "volcanoes lava islands",
		UserID: "deg-user",
	})
	if err != nil {
		t.Fatalf("Retrieve (degraded): %v", err)
	}
	if !resp.Stats.Degraded {
		t.Error("expected Stats.Degraded=true when vector leg fails")
	}
	if len(resp.Results) == 0 {
		t.Error("expected BM25-only fallback to still return results")
	}
}

func TestRetrieve_EmptyResults(t *testing.T) {
	ctx := context.Background()
	ollama := startFakeOllama(t)
	st := buildStack(t, pgDSN, qdrantAddr, ollama.URL, memory.RetrieverConfig{FinalK: 5})

	// No data ingested for this user; query should return cleanly with no panic.
	resp, err := st.retriever.Retrieve(ctx, memory.RetrieveInput{
		Query:  "asdklfjasdlkfj nonsense gibberish xyz123",
		UserID: "empty-user",
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Errorf("expected 0 results, got %d", len(resp.Results))
	}
}

func TestRerank_Skipped_FewCandidates(t *testing.T) {
	ctx := context.Background()
	ollama := startFakeOllama(t)

	// FinalK=10 with only 2 ingested memories ⇒ candidates ≤ FinalK ⇒ skip.
	cfg := memory.RetrieverConfig{FinalK: 10}
	st := buildStack(t, pgDSN, qdrantAddr, ollama.URL, cfg)

	// Replace retriever with one that has a non-nil reranker so the skip
	// decision is driven solely by the candidate-count rule.
	st.retriever = memory.NewRetriever(st.meta, st.vec, st.embedder, noopReranker{}, graph.NopStore{}, cfg)

	for i, c := range []string{
		"First short memory about cats.",
		"Second short memory about dogs.",
	} {
		if _, err := st.ingestor.Store(ctx, memory.StoreInput{
			Content:  c,
			UserID:   "rerank-user",
			Source:   fmt.Sprintf("s%d", i),
			Metadata: emptyMeta(),
		}); err != nil {
			t.Fatalf("Store[%d]: %v", i, err)
		}
	}

	resp, err := st.retriever.Retrieve(ctx, memory.RetrieveInput{
		Query:  "cats dogs",
		UserID: "rerank-user",
		Rerank: true,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if !resp.Stats.RerankSkipped {
		t.Errorf("expected RerankSkipped=true with %d results vs FinalK=%d",
			len(resp.Results), cfg.FinalK)
	}
}

func TestHTTP_StoreAndRetrieve(t *testing.T) {
	ollama := startFakeOllama(t)
	st := buildStack(t, pgDSN, qdrantAddr, ollama.URL, memory.RetrieverConfig{FinalK: 5})

	api := httpapi.NewServer("", st.ingestor, st.retriever, st.meta, nil, nil)
	srv := httptest.NewServer(api)
	defer srv.Close()

	// POST /v1/memories — supply non-nil metadata to satisfy the NOT NULL column.
	storeBody := mustJSON(t, map[string]any{
		"content":  "The Eiffel Tower is a wrought-iron lattice tower in Paris.",
		"user_id":  "http-user",
		"source":   "wiki",
		"metadata": map[string]any{},
	})
	resp, err := http.Post(srv.URL+"/v1/memories", "application/json", bytes.NewReader(storeBody))
	if err != nil {
		t.Fatalf("POST /v1/memories: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := readAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("POST /v1/memories: status %d, body=%s", resp.StatusCode, body)
	}
	var stored memory.StoreResult
	if err := json.NewDecoder(resp.Body).Decode(&stored); err != nil {
		t.Fatalf("decode store response: %v", err)
	}
	resp.Body.Close()
	if !stored.Stored || stored.MemoryID == "" {
		t.Fatalf("unexpected store response: %+v", stored)
	}

	// POST /v1/retrieve
	retrieveBody := mustJSON(t, map[string]any{
		"query":   "Eiffel Tower Paris",
		"user_id": "http-user",
		"k":       5,
	})
	resp2, err := http.Post(srv.URL+"/v1/retrieve", "application/json", bytes.NewReader(retrieveBody))
	if err != nil {
		t.Fatalf("POST /v1/retrieve: %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/retrieve: status %d", resp2.StatusCode)
	}
	var got memory.RetrieveResponse
	if err := json.NewDecoder(resp2.Body).Decode(&got); err != nil {
		t.Fatalf("decode retrieve response: %v", err)
	}
	resp2.Body.Close()
	if len(got.Results) == 0 {
		t.Fatal("expected at least one retrieval result")
	}
}

func TestHTTP_Healthz(t *testing.T) {
	ollama := startFakeOllama(t)
	st := buildStack(t, pgDSN, qdrantAddr, ollama.URL, memory.RetrieverConfig{FinalK: 5})

	api := httpapi.NewServer("", st.ingestor, st.retriever, st.meta, nil, nil)
	srv := httptest.NewServer(api)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", body)
	}
}

// TestMCP_Compile verifies the MCP server constructor links and returns a
// non-nil server even with nil dependencies. It does not exercise the
// stdio transport.
func TestMCP_Compile(t *testing.T) {
	s := mcp.NewServer(nil, nil, nil)
	if s == nil {
		t.Fatal("mcp.NewServer returned nil")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func readAll(r interface{ Read(p []byte) (int, error) }) ([]byte, error) {
	var buf bytes.Buffer
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
		}
		if err != nil {
			if err.Error() == "EOF" {
				return buf.Bytes(), nil
			}
			return buf.Bytes(), err
		}
	}
}
