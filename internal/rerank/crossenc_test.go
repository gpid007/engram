//go:build crossenc

package rerank

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestNewCrossEncoderReranker_MissingModel verifies that NewCrossEncoderReranker
// returns an informative error when the model file does not exist.
func TestNewCrossEncoderReranker_MissingModel(t *testing.T) {
	_, err := NewCrossEncoderReranker("/nonexistent/path/model.onnx", 5*time.Second)
	if err == nil {
		t.Fatal("expected error for missing model, got nil")
	}
	t.Logf("error (expected): %v", err)
}

// TestTokenize_Deterministic checks that tokenize produces stable output for
// the same input (deterministic hashing).
func TestTokenize_Deterministic(t *testing.T) {
	a := tokenize("hello world")
	b := tokenize("hello world")
	if len(a) != len(b) {
		t.Fatalf("length mismatch: %d vs %d", len(a), b)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("token[%d]: %d != %d", i, a[i], b[i])
		}
	}
}

// TestTokenize_IDRange checks that all token IDs fall in [1000, 30000).
func TestTokenize_IDRange(t *testing.T) {
	ids := tokenize("the quick brown fox jumps over the lazy dog")
	for i, id := range ids {
		if id < 1000 || id >= 30000 {
			t.Errorf("token[%d] = %d out of expected range [1000,30000)", i, id)
		}
	}
}

// TestBuildInput_SpecialTokens verifies the [CLS]/[SEP] placement and that
// the output length is always maxSeqLen.
func TestBuildInput_SpecialTokens(t *testing.T) {
	inputIDs, attn, ttids := buildInput("query text", "document text")

	if len(inputIDs) != maxSeqLen {
		t.Fatalf("inputIDs length = %d, want %d", len(inputIDs), maxSeqLen)
	}
	if len(attn) != maxSeqLen {
		t.Fatalf("attn length = %d, want %d", len(attn), maxSeqLen)
	}
	if len(ttids) != maxSeqLen {
		t.Fatalf("ttids length = %d, want %d", len(ttids), maxSeqLen)
	}

	if inputIDs[0] != clsToken {
		t.Errorf("inputIDs[0] = %d, want CLS=%d", inputIDs[0], clsToken)
	}
	if attn[0] != 1 {
		t.Errorf("attn[0] = %d, want 1", attn[0])
	}
}

// TestBuildInput_Truncation ensures long inputs are truncated to maxSeqLen.
func TestBuildInput_Truncation(t *testing.T) {
	longText := make([]string, 600)
	for i := range longText {
		longText[i] = "word"
	}
	q := "query"
	d := strings.Join(longText, " ")
	inputIDs, _, _ := buildInput(q, d)
	if len(inputIDs) != maxSeqLen {
		t.Fatalf("expected %d tokens, got %d", maxSeqLen, len(inputIDs))
	}
}

// TestRerank_WithRealModel exercises the full pipeline only when
// CROSSENC_MODEL_PATH is set in the environment.
func TestRerank_WithRealModel(t *testing.T) {
	modelPath := os.Getenv("CROSSENC_MODEL_PATH")
	if modelPath == "" {
		t.Skip("CROSSENC_MODEL_PATH not set; skipping live model test")
	}

	r, err := NewCrossEncoderReranker(modelPath, 30*time.Second)
	if err != nil {
		t.Fatalf("NewCrossEncoderReranker: %v", err)
	}

	candidates := []Candidate{
		{ChunkID: "1", Content: "Paris is the capital of France."},
		{ChunkID: "2", Content: "The Eiffel Tower stands 330 metres tall."},
		{ChunkID: "3", Content: "Banana bread is a type of quick bread."},
	}

	got, err := r.Rerank(context.Background(), "capital of France", candidates)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(got) != len(candidates) {
		t.Fatalf("expected %d results, got %d", len(candidates), len(got))
	}

	t.Logf("reranked order:")
	for i, c := range got {
		t.Logf("  [%d] id=%s score=%.4f", i, c.ChunkID, c.Score)
	}

	// The most relevant document (Paris / France) should rank first.
	if got[0].ChunkID != "1" && got[0].ChunkID != "2" {
		t.Errorf("expected France-related doc first, got chunk %s", got[0].ChunkID)
	}
}
