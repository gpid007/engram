//go:build onnxembed

package embed

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	// nomicDocPrefix is prepended to all texts during ingestion.
	// Required by nomic-embed-text-v1.5 for document-side task encoding.
	nomicDocPrefix = "search_document: "
	// nomicQueryPrefix is prepended to query texts during retrieval.
	nomicQueryPrefix = "search_query: "

	defaultONNXMaxSeqLen = 8192
	defaultONNXDim       = 768
	defaultONNXBatch     = 32
	defaultONNXTimeout   = 5 * time.Second
)

// ONNXEmbedder runs nomic-embed-text-v1.5 (or compatible) ONNX models
// in-process. It is goroutine-safe via an internal mutex.
type ONNXEmbedder struct {
	cfg       ONNXConfig
	tokenizer *tokenizerWrapper
	session   *ort.DynamicAdvancedSession
	mu        sync.Mutex // ONNX sessions are not goroutine-safe
}

// Compile-time check that ONNXEmbedder satisfies Embedder.
var _ Embedder = (*ONNXEmbedder)(nil)

// NewONNXEmbedder loads the tokenizer and ONNX model, runs a warmup pass,
// and returns a ready-to-use embedder. Returns an error if any file is
// missing, the runtime cannot initialise, or the warmup fails.
func NewONNXEmbedder(cfg ONNXConfig) (*ONNXEmbedder, error) {
	if cfg.MaxSeqLen <= 0 {
		cfg.MaxSeqLen = defaultONNXMaxSeqLen
	}
	if cfg.Dim <= 0 {
		cfg.Dim = defaultONNXDim
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultONNXBatch
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultONNXTimeout
	}

	// Verify both model files exist before touching the ONNX runtime.
	modelPath := filepath.Join(cfg.ModelDir, "model.onnx")
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("onnx: model file not found at %q: %w", modelPath, err)
	}
	tokPath := filepath.Join(cfg.ModelDir, "tokenizer.json")
	if _, err := os.Stat(tokPath); err != nil {
		return nil, fmt.Errorf("onnx: tokenizer.json not found at %q: %w", tokPath, err)
	}

	// Load tokenizer.
	tok, err := loadTokenizer(cfg.ModelDir)
	if err != nil {
		return nil, err
	}

	// Initialise ONNX runtime (idempotent).
	if !ort.IsInitialized() {
		if err := ort.InitializeEnvironment(); err != nil {
			tok.close()
			return nil, fmt.Errorf("onnx: runtime init: %w", err)
		}
	}

	// nomic-embed-text-v1.5 ONNX graph inputs/outputs.
	inputNames := []string{"input_ids", "attention_mask"}
	outputNames := []string{"last_hidden_state"}

	session, err := ort.NewDynamicAdvancedSession(modelPath, inputNames, outputNames, nil)
	if err != nil {
		tok.close()
		return nil, fmt.Errorf("onnx: create session: %w", err)
	}

	e := &ONNXEmbedder{
		cfg:       cfg,
		tokenizer: tok,
		session:   session,
	}

	// Warmup: short string and long string to exercise both the fast path
	// and the padding/truncation path. Catches tensor shape mismatches at
	// boot rather than at first user request.
	warmupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.embedRaw(warmupCtx, []string{"warmup"}); err != nil {
		e.Close()
		return nil, fmt.Errorf("onnx: warmup (short) failed: %w", err)
	}
	longText := strings.Repeat("lorem ipsum dolor sit amet ", 50)
	if _, err := e.embedRaw(warmupCtx, []string{longText}); err != nil {
		e.Close()
		return nil, fmt.Errorf("onnx: warmup (long) failed: %w", err)
	}

	return e, nil
}

// EmbedBatch embeds texts for document ingestion using the search_document: prefix.
func (e *ONNXEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	prefixed := make([]string, len(texts))
	for i, t := range texts {
		prefixed[i] = nomicDocPrefix + t
	}
	return e.embedInBatches(ctx, prefixed)
}

// EmbedQuery embeds texts for retrieval queries using the search_query: prefix.
func (e *ONNXEmbedder) EmbedQuery(ctx context.Context, texts []string) ([][]float32, error) {
	prefixed := make([]string, len(texts))
	for i, t := range texts {
		prefixed[i] = nomicQueryPrefix + t
	}
	return e.embedInBatches(ctx, prefixed)
}

// Dim returns the embedding dimension (768 for nomic-embed-text-v1.5).
func (e *ONNXEmbedder) Dim() int { return e.cfg.Dim }

// Close releases the ONNX session and tokenizer.
func (e *ONNXEmbedder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session != nil {
		if err := e.session.Destroy(); err != nil {
			return fmt.Errorf("onnx: destroy session: %w", err)
		}
		e.session = nil
	}
	if e.tokenizer != nil {
		e.tokenizer.close()
		e.tokenizer = nil
	}
	return nil
}

// embedInBatches splits texts into cfg.BatchSize chunks and calls embedRaw.
func (e *ONNXEmbedder) embedInBatches(ctx context.Context, texts []string) ([][]float32, error) {
	batchSize := e.cfg.BatchSize
	var all [][]float32
	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := e.embedRaw(ctx, texts[i:end])
		if err != nil {
			return nil, err
		}
		all = append(all, vecs...)
	}
	return all, nil
}

// embedRaw tokenizes and runs a single ONNX forward pass for one batch.
// Applies mean pooling (masked) and L2 normalization.
func (e *ONNXEmbedder) embedRaw(ctx context.Context, texts []string) ([][]float32, error) {
	if e.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.cfg.Timeout)
		defer cancel()
	}

	batchSize := len(texts)
	seqLen := e.cfg.MaxSeqLen

	// Tokenize.
	flatIDs, flatMask, err := e.tokenizer.encodeBatch(texts, seqLen)
	if err != nil {
		return nil, fmt.Errorf("onnx: tokenize: %w", err)
	}

	// Build input tensors: [batch, seqLen].
	shape := ort.NewShape(int64(batchSize), int64(seqLen))

	idsData := make([]int64, len(flatIDs))
	copy(idsData, flatIDs)
	maskData := make([]int64, len(flatMask))
	copy(maskData, flatMask)

	inIDs, err := ort.NewTensor(shape, idsData)
	if err != nil {
		return nil, fmt.Errorf("onnx: ids tensor: %w", err)
	}
	defer inIDs.Destroy()

	inMask, err := ort.NewTensor(shape, maskData)
	if err != nil {
		return nil, fmt.Errorf("onnx: mask tensor: %w", err)
	}
	defer inMask.Destroy()

	// Output: last_hidden_state [batch, seqLen, hidden].
	outHidden, err := ort.NewEmptyTensor[float32](ort.NewShape(int64(batchSize), int64(seqLen), int64(e.cfg.Dim)))
	if err != nil {
		return nil, fmt.Errorf("onnx: output tensor: %w", err)
	}
	defer outHidden.Destroy()

	// Run inference (mutex for goroutine safety).
	e.mu.Lock()
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("onnx: context cancelled before inference: %w", err)
	}
	runErr := e.session.Run(
		[]ort.ArbitraryTensor{inIDs, inMask},
		[]ort.ArbitraryTensor{outHidden},
	)
	e.mu.Unlock()
	if runErr != nil {
		return nil, fmt.Errorf("onnx: inference: %w", runErr)
	}

	hidden := outHidden.GetData() // length = batchSize * seqLen * dim

	// Mean pool over token dimension, weighted by attention mask.
	// Then L2-normalize each vector.
	result := make([][]float32, batchSize)
	for b := 0; b < batchSize; b++ {
		vec := make([]float32, e.cfg.Dim)
		var maskSum float64
		for s := 0; s < seqLen; s++ {
			m := flatMask[b*seqLen+s]
			if m == 0 {
				continue
			}
			maskSum += float64(m)
			base := (b*seqLen+s)*e.cfg.Dim
			for d := 0; d < e.cfg.Dim; d++ {
				vec[d] += hidden[base+d] * float32(m)
			}
		}
		if maskSum > 0 {
			invMask := float32(1.0 / maskSum)
			for d := 0; d < e.cfg.Dim; d++ {
				vec[d] *= invMask
			}
		}

		// L2 normalize.
		var norm float64
		for _, v := range vec {
			norm += float64(v) * float64(v)
		}
		norm = math.Sqrt(norm)
		if norm > 1e-9 {
			invNorm := float32(1.0 / norm)
			for d := 0; d < e.cfg.Dim; d++ {
				vec[d] *= invNorm
			}
		}

		result[b] = vec
	}

	return result, nil
}
