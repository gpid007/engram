//go:build !onnxembed

package embed

import (
	"context"
	"errors"
)

// ErrONNXEmbedNotBuilt is returned when the binary was not compiled with
// -tags onnxembed. Rebuild with CGO_ENABLED=1 and -tags onnxembed.
var ErrONNXEmbedNotBuilt = errors.New(
	"onnx embedder not compiled in; rebuild with CGO_ENABLED=1 -tags onnxembed",
)

// ONNXEmbedder is the stub type. All methods return ErrONNXEmbedNotBuilt.
type ONNXEmbedder struct{}

// NewONNXEmbedder always returns ErrONNXEmbedNotBuilt in stub builds.
func NewONNXEmbedder(_ ONNXConfig) (*ONNXEmbedder, error) {
	return nil, ErrONNXEmbedNotBuilt
}

func (*ONNXEmbedder) EmbedBatch(_ context.Context, _ []string) ([][]float32, error) {
	return nil, ErrONNXEmbedNotBuilt
}

func (*ONNXEmbedder) EmbedQuery(_ context.Context, _ []string) ([][]float32, error) {
	return nil, ErrONNXEmbedNotBuilt
}

func (*ONNXEmbedder) Dim() int     { return 0 }
func (*ONNXEmbedder) Close() error { return nil }
