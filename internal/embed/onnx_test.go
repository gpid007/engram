//go:build onnxembed

package embed

import (
	"context"
	"math"
	"os"
	"testing"
)

// modelDir returns the test model directory or skips the test.
func modelDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("ENGRAM_TEST_MODEL_DIR")
	if dir == "" {
		t.Skip("ENGRAM_TEST_MODEL_DIR not set; skipping ONNX integration test")
	}
	return dir
}

func testEmbedder(t *testing.T) *ONNXEmbedder {
	t.Helper()
	e, err := NewONNXEmbedder(ONNXConfig{ModelDir: modelDir(t)})
	if err != nil {
		t.Fatalf("NewONNXEmbedder: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func cosineSim(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func TestONNX_WarmupSucceeds(t *testing.T) {
	// NewONNXEmbedder already runs warmup; reaching here means it passed.
	_ = testEmbedder(t)
}

func TestONNX_DimensionsAreCorrect(t *testing.T) {
	e := testEmbedder(t)
	vecs, err := e.EmbedBatch(context.Background(), []string{"hello world", "test sentence"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != 768 {
			t.Errorf("vec[%d]: expected dim 768, got %d", i, len(v))
		}
	}
}

func TestONNX_VectorsAreL2Normalized(t *testing.T) {
	e := testEmbedder(t)
	vecs, err := e.EmbedBatch(context.Background(), []string{
		"normalization test sentence",
		"another sentence to verify unit vectors",
	})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	for i, v := range vecs {
		var norm float64
		for _, x := range v {
			norm += float64(x) * float64(x)
		}
		norm = math.Sqrt(norm)
		if math.Abs(norm-1.0) > 1e-4 {
			t.Errorf("vec[%d]: L2 norm = %f, want 1.0 (±1e-4)", i, norm)
		}
	}
}

func TestONNX_SemanticSimilaritySanity(t *testing.T) {
	e := testEmbedder(t)
	ctx := context.Background()
	vecs, err := e.EmbedBatch(ctx, []string{"cat", "kitten", "airplane"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	catKitten := cosineSim(vecs[0], vecs[1])
	catAirplane := cosineSim(vecs[0], vecs[2])
	if catKitten <= catAirplane {
		t.Errorf("expected sim(cat,kitten)=%.4f > sim(cat,airplane)=%.4f", catKitten, catAirplane)
	}
	if catKitten-catAirplane < 0.1 {
		t.Errorf("expected difference > 0.1, got %.4f", catKitten-catAirplane)
	}
}

func TestONNX_PaddingDoesNotAffectMeanPool(t *testing.T) {
	e := testEmbedder(t)
	ctx := context.Background()

	shortCompanion := "hi"
	longCompanion := "the quick brown fox jumps over the lazy dog " +
		"the quick brown fox jumps over the lazy dog " +
		"the quick brown fox jumps over the lazy dog"

	vecsShort, err := e.EmbedBatch(ctx, []string{"hello world", shortCompanion})
	if err != nil {
		t.Fatalf("EmbedBatch short: %v", err)
	}
	vecsLong, err := e.EmbedBatch(ctx, []string{"hello world", longCompanion})
	if err != nil {
		t.Fatalf("EmbedBatch long: %v", err)
	}

	sim := cosineSim(vecsShort[0], vecsLong[0])
	if math.Abs(sim-1.0) > 1e-4 {
		t.Errorf("padding affected mean pool: cos sim of identical text = %.6f, want 1.0 (±1e-4)", sim)
	}
}

func TestONNX_EmbedQueryUsesDifferentPrefix(t *testing.T) {
	e := testEmbedder(t)
	ctx := context.Background()
	text := "what is the capital of France"

	docVec, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	queryVec, err := e.EmbedQuery(ctx, []string{text})
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}

	sim := cosineSim(docVec[0], queryVec[0])
	if sim > 0.9999 {
		t.Error("EmbedQuery and EmbedBatch produced identical vectors — prefix not applied")
	}
	if sim < 0.7 {
		t.Errorf("EmbedQuery vs EmbedBatch sim = %.4f, suspiciously low — prefix may be wrong", sim)
	}
}
