package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// makeEmbeddings returns n vectors each of length dim, filled with 0.1.
func makeEmbeddings(n, dim int) [][]float32 {
	out := make([][]float32, n)
	for i := range out {
		v := make([]float32, dim)
		for j := range v {
			v[j] = 0.1
		}
		out[i] = v
	}
	return out
}

// serveEmbed is a helper that writes a valid embed response for the given request input length.
func serveEmbed(w http.ResponseWriter, r *http.Request, dim int) int {
	var req embedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return 0
	}
	resp := embedResponse{Embeddings: makeEmbeddings(len(req.Input), dim)}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
	return len(req.Input)
}

// Test 1: Happy path — embed 3 texts, verify count and dim.
func TestHappyPath(t *testing.T) {
	const dim = 4
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveEmbed(w, r, dim)
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(Config{
		BaseURL: srv.URL,
		Model:   "test",
		Dim:     dim,
		Batch:   32,
		Retries: 3,
		Timeout: 5 * time.Second,
	})

	texts := []string{"hello", "world", "foo"}
	got, err := e.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 embeddings, got %d", len(got))
	}
	for i, v := range got {
		if len(v) != dim {
			t.Errorf("embedding[%d]: expected dim %d, got %d", i, dim, len(v))
		}
	}
}

// Test 2: Batching — Batch=2, embed 5 texts, expect 3 HTTP requests (2+2+1).
func TestBatching(t *testing.T) {
	const dim = 4
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		serveEmbed(w, r, dim)
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(Config{
		BaseURL: srv.URL,
		Model:   "test",
		Dim:     dim,
		Batch:   2,
		Retries: 1,
		Timeout: 5 * time.Second,
	})

	texts := []string{"a", "b", "c", "d", "e"}
	got, err := e.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 embeddings, got %d", len(got))
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Errorf("expected 3 HTTP calls, got %d", n)
	}
}

// Test 3: Retry on 500 — first call 500, second call 200, verify success and 2 requests.
func TestRetryOn500(t *testing.T) {
	const dim = 4
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		serveEmbed(w, r, dim)
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(Config{
		BaseURL: srv.URL,
		Model:   "test",
		Dim:     dim,
		Batch:   32,
		Retries: 3,
		Timeout: 5 * time.Second,
	})

	got, err := e.EmbedBatch(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 embedding, got %d", len(got))
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("expected 2 HTTP calls, got %d", n)
	}
}

// Test 4: No retry on 400 — mock returns 400, verify error after 1 attempt only.
func TestNoRetryOn400(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(Config{
		BaseURL: srv.URL,
		Model:   "test",
		Dim:     4,
		Batch:   32,
		Retries: 3,
		Timeout: 5 * time.Second,
	})

	_, err := e.EmbedBatch(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error on 400, got nil")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("expected exactly 1 HTTP call, got %d", n)
	}
}

// Test 5: Circuit breaker opens after 10 consecutive failures.
func TestCircuitBreakerOpens(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	// Retries=1 means 1 attempt per batch. Each Embed() call with 1 text = 1 failure.
	e := NewOllamaEmbedder(Config{
		BaseURL: srv.URL,
		Model:   "test",
		Dim:     4,
		Batch:   1,
		Retries: 1,
		Timeout: 5 * time.Second,
	})

	ctx := context.Background()
	// Drive 10 consecutive failures to open the circuit.
	for i := 0; i < failureThreshold; i++ {
		_, _ = e.EmbedBatch(ctx, []string{"x"})
	}

	callsBefore := atomic.LoadInt32(&calls)

	// Now the circuit should be open — this call must not hit the mock.
	_, err := e.EmbedBatch(ctx, []string{"x"})
	if err == nil {
		t.Fatal("expected circuit-open error, got nil")
	}
	if atomic.LoadInt32(&calls) != callsBefore {
		t.Errorf("circuit breaker did not block the request: mock was called after circuit opened")
	}
}

// Test 6: Circuit breaker closes after 30s open, probe succeeds.
func TestCircuitBreakerCloses(t *testing.T) {
	const dim = 4
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		// First 10 calls fail to open the circuit; subsequent calls succeed (probe).
		if n <= int32(failureThreshold) {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		serveEmbed(w, r, dim)
	}))
	defer srv.Close()

	now := time.Now()
	nowFunc := func() time.Time { return now }

	e := NewOllamaEmbedder(Config{
		BaseURL: srv.URL,
		Model:   "test",
		Dim:     dim,
		Batch:   1,
		Retries: 1,
		Timeout: 5 * time.Second,
	})
	// Inject fake clock.
	e.nowFunc = nowFunc

	ctx := context.Background()
	// Open the circuit.
	for i := 0; i < failureThreshold; i++ {
		_, _ = e.EmbedBatch(ctx, []string{"x"})
	}

	// Confirm circuit is open.
	if e.state != circuitOpen {
		t.Fatal("expected circuit to be open")
	}

	// Advance fake time past the open duration.
	now = now.Add(openDuration + time.Second)

	// Next call should be allowed as a probe.
	got, err := e.EmbedBatch(ctx, []string{"probe"})
	if err != nil {
		t.Fatalf("expected probe to succeed, got error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 embedding from probe, got %d", len(got))
	}

	// Circuit should now be closed.
	e.mu.Lock()
	state := e.state
	e.mu.Unlock()
	if state != circuitClosed {
		t.Errorf("expected circuit to be closed after successful probe, got state %d", state)
	}
}
