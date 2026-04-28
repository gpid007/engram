package rerank_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gregdhill/engram/internal/rerank"
)

func TestNewCrossEncoderReranker_StubReturnsError(t *testing.T) {
	_, err := rerank.NewCrossEncoderReranker("/nonexistent/model.onnx", 5*time.Second)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !errors.Is(err, rerank.ErrCrossEncNotBuilt) {
		t.Fatalf("expected ErrCrossEncNotBuilt, got: %v", err)
	}
}

func TestErrCrossEncNotBuilt_MessageIsDescriptive(t *testing.T) {
	msg := rerank.ErrCrossEncNotBuilt.Error()
	if msg == "" {
		t.Fatal("error message should not be empty")
	}
	// Verify it mentions both the feature and the remedy.
	for _, substr := range []string{"cross-encoder", "crossenc"} {
		found := false
		for i := 0; i+len(substr) <= len(msg); i++ {
			if msg[i:i+len(substr)] == substr {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("error message %q does not mention %q", msg, substr)
		}
	}
}
