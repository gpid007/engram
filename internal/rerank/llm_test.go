package rerank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// makeServer creates a test HTTP server that always responds with the given
// Ollama chat response body containing the provided score content string.
func makeServer(t *testing.T, content string, delay time.Duration) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			time.Sleep(delay)
		}
		resp := ollamaChatResponse{
			Message: ollamaMessage{Role: "assistant", Content: content},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func candidates3() []Candidate {
	return []Candidate{
		{ChunkID: "c1", Content: "document one"},
		{ChunkID: "c2", Content: "document two"},
		{ChunkID: "c3", Content: "document three"},
	}
}

// Test 1: Happy path — [7, 9, 3] → order should be doc2, doc1, doc3.
func TestLLMReranker_HappyPath(t *testing.T) {
	srv := makeServer(t, "[7, 9, 3]", 0)
	defer srv.Close()

	rr := NewLLMReranker(LLMConfig{BaseURL: srv.URL, Model: "test"})
	got, err := rr.Rerank(context.Background(), "query", candidates3())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}
	want := []string{"c2", "c1", "c3"}
	for i, w := range want {
		if got[i].ChunkID != w {
			t.Errorf("position %d: want %s, got %s", i, w, got[i].ChunkID)
		}
	}
}

// Test 2: Markdown fenced response — ```json\n[5,8,2]\n``` → parses correctly.
func TestLLMReranker_MarkdownFenced(t *testing.T) {
	srv := makeServer(t, "```json\n[5,8,2]\n```", 0)
	defer srv.Close()

	rr := NewLLMReranker(LLMConfig{BaseURL: srv.URL, Model: "test"})
	got, err := rr.Rerank(context.Background(), "query", candidates3())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expected order: scores [5,8,2] → doc2(8), doc1(5), doc3(2)
	want := []string{"c2", "c1", "c3"}
	for i, w := range want {
		if got[i].ChunkID != w {
			t.Errorf("position %d: want %s, got %s", i, w, got[i].ChunkID)
		}
	}
}

// Test 3: Malformed response → returns original order, no error.
func TestLLMReranker_MalformedResponse(t *testing.T) {
	srv := makeServer(t, "I cannot score these", 0)
	defer srv.Close()

	rr := NewLLMReranker(LLMConfig{BaseURL: srv.URL, Model: "test"})
	orig := candidates3()
	got, err := rr.Rerank(context.Background(), "query", orig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, c := range orig {
		if got[i].ChunkID != c.ChunkID {
			t.Errorf("position %d: want %s, got %s", i, c.ChunkID, got[i].ChunkID)
		}
	}
}

// Test 4: Wrong length array → returns original order, no error.
func TestLLMReranker_WrongLength(t *testing.T) {
	srv := makeServer(t, "[5, 8]", 0)
	defer srv.Close()

	rr := NewLLMReranker(LLMConfig{BaseURL: srv.URL, Model: "test"})
	orig := candidates3()
	got, err := rr.Rerank(context.Background(), "query", orig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, c := range orig {
		if got[i].ChunkID != c.ChunkID {
			t.Errorf("position %d: want %s, got %s", i, c.ChunkID, got[i].ChunkID)
		}
	}
}

// Test 5: Score clamping — [15, -2, 5] → clamped [10, 0, 5] → order doc1(10), doc3(5), doc2(0).
func TestLLMReranker_ScoreClamping(t *testing.T) {
	srv := makeServer(t, "[15, -2, 5]", 0)
	defer srv.Close()

	rr := NewLLMReranker(LLMConfig{BaseURL: srv.URL, Model: "test"})
	got, err := rr.Rerank(context.Background(), "query", candidates3())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"c1", "c3", "c2"}
	for i, w := range want {
		if got[i].ChunkID != w {
			t.Errorf("position %d: want %s, got %s", i, w, got[i].ChunkID)
		}
	}
}

// Test 6: Timeout — mock sleeps 200ms, Timeout=50ms → original order, no error.
func TestLLMReranker_Timeout(t *testing.T) {
	srv := makeServer(t, "[7, 9, 3]", 200*time.Millisecond)
	defer srv.Close()

	rr := NewLLMReranker(LLMConfig{BaseURL: srv.URL, Model: "test", Timeout: 50 * time.Millisecond})
	orig := candidates3()
	got, err := rr.Rerank(context.Background(), "query", orig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, c := range orig {
		if got[i].ChunkID != c.ChunkID {
			t.Errorf("position %d: want %s, got %s", i, c.ChunkID, got[i].ChunkID)
		}
	}
}

// Test 7: Empty candidates → empty output, no error, no panic.
func TestLLMReranker_EmptyCandidates(t *testing.T) {
	// No server needed — should return before making any HTTP call.
	rr := NewLLMReranker(LLMConfig{BaseURL: "http://127.0.0.1:0", Model: "test"})
	got, err := rr.Rerank(context.Background(), "query", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty result, got %d items", len(got))
	}

	// Also test with empty (non-nil) slice.
	got, err = rr.Rerank(context.Background(), "query", []Candidate{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty result, got %d items", len(got))
	}
}
