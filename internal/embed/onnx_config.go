package embed

import "time"

// ONNXConfig holds configuration for the ONNX embedder.
// Defined without a build tag so config code compiles in all build modes.
type ONNXConfig struct {
	ModelDir  string        // directory containing model.onnx and tokenizer.json
	LibPath   string        // path to onnxruntime shared library (.dylib/.so); auto-detected if empty
	MaxSeqLen int           // max token sequence length (default 8192)
	Dim       int           // embedding dimension (768 for nomic-embed-text-v1.5)
	BatchSize int           // texts per ONNX forward pass (default 32)
	Timeout   time.Duration // per-batch timeout (default 5s)
}
