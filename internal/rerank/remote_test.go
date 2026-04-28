package rerank

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"context"
)

func mockServer(t *testing.T, status int, body string, assertFn func(r *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if assertFn != nil {
			assertFn(r)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
}

var testCandidates = []Candidate{
	{ChunkID: "c1", Content: "apple fruit"},
	{ChunkID: "c2", Content: "banana fruit"},
	{ChunkID: "c3", Content: "cherry fruit"},
}

func TestRemoteReranker_HappyPath(t *testing.T) {
	srv := mockServer(t, 200, `{"results":[{"index":2,"relevance_score":0.9},{"index":0,"relevance_score":0.5},{"index":1,"relevance_score":0.1}]}`, nil)
	defer srv.Close()

	r := NewRemoteReranker(RemoteConfig{BaseURL: srv.URL, Model: "test"})
	got, err := r.Rerank(context.Background(), "fruit", testCandidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}
	if got[0].ChunkID != "c3" || got[1].ChunkID != "c1" || got[2].ChunkID != "c2" {
		t.Errorf("wrong order: %v", got)
	}
}

func TestRemoteReranker_Disabled(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	r := NewRemoteReranker(RemoteConfig{BaseURL: ""})
	got, err := r.Rerank(context.Background(), "q", testCandidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("HTTP call should not be made when disabled")
	}
	if len(got) != len(testCandidates) {
		t.Errorf("expected original candidates returned")
	}
}

func TestRemoteReranker_EmptyCandidates(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	r := NewRemoteReranker(RemoteConfig{BaseURL: srv.URL})
	got, err := r.Rerank(context.Background(), "q", []Candidate{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("HTTP call should not be made for empty candidates")
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice")
	}
}

func TestRemoteReranker_HTTP429(t *testing.T) {
	srv := mockServer(t, 429, `{"error":"rate limited"}`, nil)
	defer srv.Close()

	r := NewRemoteReranker(RemoteConfig{BaseURL: srv.URL})
	_, err := r.Rerank(context.Background(), "q", testCandidates)
	if !errors.Is(err, ErrNonRetryable) {
		t.Errorf("expected ErrNonRetryable, got %v", err)
	}
}

func TestRemoteReranker_HTTP503(t *testing.T) {
	srv := mockServer(t, 503, `{"error":"unavailable"}`, nil)
	defer srv.Close()

	r := NewRemoteReranker(RemoteConfig{BaseURL: srv.URL})
	_, err := r.Rerank(context.Background(), "q", testCandidates)
	if !errors.Is(err, ErrRetryable) {
		t.Errorf("expected ErrRetryable, got %v", err)
	}
}

func TestRemoteReranker_MalformedJSON(t *testing.T) {
	srv := mockServer(t, 200, `not json`, nil)
	defer srv.Close()

	r := NewRemoteReranker(RemoteConfig{BaseURL: srv.URL})
	_, err := r.Rerank(context.Background(), "q", testCandidates)
	if err == nil {
		t.Error("expected error on malformed JSON")
	}
}

func TestRemoteReranker_AuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(rerankResponse{Results: []rerankResponseItem{{Index: 0, RelevanceScore: 1.0}}})
	}))
	defer srv.Close()

	r := NewRemoteReranker(RemoteConfig{BaseURL: srv.URL, APIKey: "test-key"})
	_, err := r.Rerank(context.Background(), "q", testCandidates[:1])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("expected 'Bearer test-key', got %q", gotAuth)
	}
}
