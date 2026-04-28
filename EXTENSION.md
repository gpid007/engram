# ONNX Embedder Extension Plan

> **Execution model:** Each WS below is a self-contained prompt for a specific
> agent. WS prompts are written as **literal copy-paste instructions**: exact
> file paths, exact code blocks, exact verification commands. No design
> decisions are deferred to the executing agent.
>
> **Orchestration:** `ac-sonnet` owns Track A (ONNX session + tokenizer).
> `lo-coder-q4` owns Track B (config, ops, CI) — all B sub-tasks are
> independent and can run in parallel with each other and with Track A.
> B2 (main.go wiring) is the only sync point: it must wait for Track A to
> complete before running.

---

## Ground Truth (Verified Against Repo)

- **Module path:** `github.com/gregdhill/engram`
- **Embedder interface:** `internal/embed/ollama.go:18-21` — current interface:
  ```go
  type Embedder interface {
      Embed(ctx context.Context, texts []string) ([][]float32, error)
      Dim() int
  }
  ```
  **This interface will be extended in WS-A1 to add `EmbedQuery`.**
- **OllamaEmbedder:** `internal/embed/ollama.go` — implements `Embed` and `Dim`
  only. `EmbedQuery` will be added in WS-A1 as a thin wrapper over `Embed`.
- **EmbeddingConfig:** `internal/config/config.go` — already has `Provider`
  field. Missing: `ModelDir string`, `MaxSeqLen int`.
- **Env overrides already present:** `ENGRAM_EMBEDDING_PROVIDER` at line 232.
  Need to add: `ENGRAM_EMBEDDING_MODEL_DIR`, `ENGRAM_EMBEDDING_MAX_SEQ_LEN`.
- **main.go embedder construction:** lines 91-97 — direct
  `embed.NewOllamaEmbedder(...)`. Replace with provider switch in WS-B2.
- **Retriever embed call:** uses `embedder.Embed(...)` for query vectors.
  Must be changed to `embedder.EmbedQuery(...)` in WS-B2 (single line change).
- **ONNX runtime:** `github.com/yalue/onnxruntime_go v1.28.0` already in
  `go.mod`. Cross-encoder pattern in `internal/rerank/crossenc.go`.
- **Build tag precedent:** `//go:build crossenc` on reranker. Mirror with
  `//go:build onnxembed` for ONNX embedder.
- **Dockerfile:** `deploy/Dockerfile` — currently pure CGO_ENABLED=0 Alpine.
  Full replacement needed in WS-B6 (multistage Rust + Go).
- **docker-compose.yml:** `deploy/docker-compose.yml` — services: postgres,
  qdrant, ollama, init-models, neo4j, engram. volumes: pgdata, qdrantdata,
  ollamadata, neo4jdata. Add: model-init service, models volume.
- **CI:** `.github/workflows/ci.yml` — unit job and integration job. Add:
  integration-onnx job in WS-B7.
- **HF revision pin:** `16999335555c8808544a0344d2d4d9834ba70404`
  (nomic-ai/nomic-embed-text-v1.5, verified stable)

---

## Design Summary

**Goal:** Add a local ONNX embedding provider using `nomic-embed-text-v1.5`.
Replace the Ollama HTTP round-trip with in-process ONNX inference, reducing
bulk ingest time from ~5-10 min to <30s for a 100-page document.

**Interface extension:** Split `Embed` into `EmbedBatch` (uses
`search_document:` prefix, for ingest) and `EmbedQuery` (uses `search_query:`
prefix, for retrieval). Both are required for full nomic-v1.5 quality. Ollama
implementation adds both as thin wrappers over its existing logic.

**Model delivery:** Init-container sidecar using `curlimages/curl` with an
inline shell script. Main engram container mounts the model volume read-only.
Graceful fallback to Ollama if model files are absent.

**Tokenizer:** `github.com/daulet/tokenizers` — wraps HuggingFace Rust
tokenizers. CGO required. Build tag `onnxembed` gates the dependency so
default builds stay pure-Go.

**Multistage Dockerfile:** `rust:1-bookworm` → `golang:1.23-bookworm` →
`gcr.io/distroless/cc-debian12`. CGO_ENABLED=1. Default image remains
CGO_ENABLED=0 Alpine (no onnxembed tag).

---

## Fixed Issues vs. Prior State

| # | Issue | Fix |
|---|-------|-----|
| 1 | `Embedder` interface has only `Embed` — no prefix differentiation | Extend interface with `EmbedBatch` + `EmbedQuery`; rename `Embed` → `EmbedBatch` on Ollama |
| 2 | Retriever calls `embedder.Embed()` for query vectors — wrong prefix | Change to `embedder.EmbedQuery()` in main.go and retriever |
| 3 | Dockerfile is CGO_ENABLED=0 Alpine — incompatible with daulet/tokenizers | Multistage Rust+Go build for `-tags onnxembed` variant |
| 4 | No model file delivery mechanism | Sidecar init container + named volume |
| 5 | ONNX session not goroutine-safe | Mutex in ONNXEmbedder |
| 6 | Config already has Provider but no ModelDir/MaxSeqLen | Add two fields in WS-B1 |

---

## Workstreams

---

### WS-A1 — Interface Extension + Ollama Update

**Agent:** `ac-sonnet` | **Blocks:** WS-A2, WS-B2 | **Est:** 20 min

#### Files to edit

1. `internal/embed/ollama.go`

#### Exact change: extend `Embedder` interface

Replace the `Embedder` interface (lines 18-21 of `internal/embed/ollama.go`):

**Before:**
```go
// Embedder produces vectors for text inputs.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dim() int
}
```

**After:**
```go
// Embedder produces vectors for text inputs.
// EmbedBatch applies document-side prefixes (for ingest).
// EmbedQuery applies query-side prefixes (for retrieval).
// Implementations that do not use task prefixes may use the same
// underlying logic for both methods.
type Embedder interface {
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	EmbedQuery(ctx context.Context, texts []string) ([][]float32, error)
	Dim() int
}
```

#### Exact change: rename `Embed` → `EmbedBatch` on OllamaEmbedder, add `EmbedQuery`

Find the `Embed` method on `OllamaEmbedder` (around line 75). Rename it to
`EmbedBatch`. Then add `EmbedQuery` directly below it:

**Replace:**
```go
// Embed embeds the given texts by splitting into batches and calling Ollama.
func (o *OllamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
```

**With:**
```go
// EmbedBatch embeds texts for document ingestion. Ollama's nomic-embed-text
// model handles task prefixes internally via its modelfile template, so no
// explicit prefix is prepended here.
func (o *OllamaEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
```

Then add immediately after the closing `}` of `EmbedBatch`:

```go
// EmbedQuery embeds texts for retrieval queries. Delegates to EmbedBatch
// because Ollama's nomic-embed-text modelfile applies the correct template
// regardless of the calling path.
func (o *OllamaEmbedder) EmbedQuery(ctx context.Context, texts []string) ([][]float32, error) {
	return o.EmbedBatch(ctx, texts)
}
```

#### Verification

```bash
go build ./internal/embed/...
go test ./internal/embed/... -count=1
```

Both must exit 0. The existing tests call `Embed` — update any test call sites
to `EmbedBatch` if needed (grep for `.Embed(` in `internal/embed/`).

Also grep entire repo for `.Embed(` and `.Embed(ctx` to find all callers that
must be updated:

```bash
grep -rn "\.Embed(" --include="*.go" .
```

Update every caller:
- Ingest-side callers → `EmbedBatch`
- Query/retrieval-side callers → `EmbedQuery`
- If unclear, default to `EmbedBatch` and note in a TODO comment.

---

### WS-A2 — ONNX Embedder Implementation

**Agent:** `ac-sonnet` | **Depends on:** WS-A1 | **Est:** 3-4 h

#### Files to create

1. `internal/embed/onnx_stub.go` — stub for default (non-onnxembed) builds
2. `internal/embed/tokenizer.go` — daulet/tokenizers wrapper
3. `internal/embed/onnx.go` — real ONNX embedder
4. `internal/embed/onnx_test.go` — round-trip tests

#### go.mod

Add: `github.com/daulet/tokenizers` (latest).

Run: `go get github.com/daulet/tokenizers`

Note: this requires CGO. It will only compile when `CGO_ENABLED=1` AND build
tag `onnxembed` is set. The stub file handles the `!onnxembed` case.

#### `internal/embed/onnx_stub.go`

```go
//go:build !onnxembed

package embed

import (
	"context"
	"errors"
	"time"
)

// ErrONNXEmbedNotBuilt is returned when the binary was not compiled with
// -tags onnxembed. Rebuild with CGO_ENABLED=1 and -tags onnxembed.
var ErrONNXEmbedNotBuilt = errors.New(
	"onnx embedder not compiled in; rebuild with CGO_ENABLED=1 -tags onnxembed",
)

// ONNXConfig holds configuration for the ONNX embedder.
// Defined in stub so config code compiles without the build tag.
type ONNXConfig struct {
	ModelDir  string        // directory containing model.onnx and tokenizer.json
	MaxSeqLen int           // max token sequence length (default 8192)
	Dim       int           // embedding dimension (768 for nomic-embed-text-v1.5)
	BatchSize int           // texts per ONNX forward pass (default 32)
	Timeout   time.Duration // per-batch timeout (default 5s)
}

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

func (*ONNXEmbedder) Dim() int    { return 0 }
func (*ONNXEmbedder) Close() error { return nil }
```

#### `internal/embed/tokenizer.go`

```go
//go:build onnxembed

package embed

import (
	"fmt"
	"path/filepath"

	"github.com/daulet/tokenizers"
)

// tokenizerWrapper wraps a daulet/tokenizers Tokenizer for batch encoding.
type tokenizerWrapper struct {
	tk *tokenizers.Tokenizer
}

// loadTokenizer loads tokenizer.json from the given model directory.
func loadTokenizer(modelDir string) (*tokenizerWrapper, error) {
	path := filepath.Join(modelDir, "tokenizer.json")
	tk, err := tokenizers.FromFile(path)
	if err != nil {
		return nil, fmt.Errorf("load tokenizer from %q: %w", path, err)
	}
	return &tokenizerWrapper{tk: tk}, nil
}

// encodeBatch tokenizes a slice of texts, padding/truncating each to maxSeqLen.
// Returns flat int64 slices of length len(texts)*maxSeqLen for input_ids and
// attention_mask. Token type IDs are all zeros (nomic uses only two segments).
func (w *tokenizerWrapper) encodeBatch(texts []string, maxSeqLen int) (
	inputIDs, attentionMask []int64, err error,
) {
	n := len(texts)
	inputIDs = make([]int64, n*maxSeqLen)
	attentionMask = make([]int64, n*maxSeqLen)

	for i, text := range texts {
		enc, encErr := w.tk.Encode(text, true /* addSpecialTokens */)
		if encErr != nil {
			return nil, nil, fmt.Errorf("encode text[%d]: %w", i, encErr)
		}

		ids := enc.IDs
		mask := enc.AttentionMask

		// Truncate to maxSeqLen if necessary.
		if len(ids) > maxSeqLen {
			ids = ids[:maxSeqLen]
			mask = mask[:maxSeqLen]
		}

		base := i * maxSeqLen
		for j, id := range ids {
			inputIDs[base+j] = int64(id)
			attentionMask[base+j] = int64(mask[j])
		}
		// Remaining positions stay zero (padding).
	}
	return inputIDs, attentionMask, nil
}

// close releases the tokenizer's resources.
func (w *tokenizerWrapper) close() {
	w.tk.Close()
}
```

#### `internal/embed/onnx.go`

```go
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
	nomicDocPrefix   = "search_document: "
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
	inputNames  := []string{"input_ids", "attention_mask"}
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
```

#### `internal/embed/onnx_test.go`

```go
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
	// Embed "hello world" in two batches: one with a short companion,
	// one with a long companion. The "hello world" vector must be identical
	// in both cases (within float epsilon), proving the mask correctly
	// excludes padding tokens from the mean pool.
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

	// vecsShort[0] and vecsLong[0] are both "hello world" — must be equal.
	sim := cosineSim(vecsShort[0], vecsLong[0])
	if math.Abs(sim-1.0) > 1e-4 {
		t.Errorf("padding affected mean pool: cos sim of identical text = %.6f, want 1.0 (±1e-4)", sim)
	}
}

func TestONNX_EmbedQueryUsesDifferentPrefix(t *testing.T) {
	// search_query: and search_document: prefixes should produce measurably
	// different vectors for the same text (nomic is calibrated this way).
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
	// Should be similar (same text) but not identical (different prefix).
	if sim > 0.9999 {
		t.Error("EmbedQuery and EmbedBatch produced identical vectors — prefix not applied")
	}
	// But still in the same ballpark (shouldn't be completely different).
	if sim < 0.7 {
		t.Errorf("EmbedQuery vs EmbedBatch sim = %.4f, suspiciously low — prefix may be wrong", sim)
	}
}
```

#### Verification

```bash
# Default build still passes (stub compiles):
go build ./...
go test ./internal/embed/... -count=1

# ONNX build compiles (requires CGO_ENABLED=1 and Rust toolchain):
CGO_ENABLED=1 go build -tags onnxembed ./...

# ONNX tests against real model (after fetching model files):
ENGRAM_TEST_MODEL_DIR=/tmp/models/nomic-embed-text-v1.5 \
  CGO_ENABLED=1 go test -tags onnxembed -count=1 -v ./internal/embed/...
```

All 6 tests must pass. Do not proceed to WS-B2 until WS-A2 verification passes.

---

### WS-B1 — Config Extension

**Agent:** `lo-coder-q4` | **Independent** | **Est:** 20 min

#### File to edit

1. `internal/config/config.go`

#### Exact change 1: extend `EmbeddingConfig` struct

Find the `EmbeddingConfig` struct. It currently is:

```go
// EmbeddingConfig holds embedder settings.
type EmbeddingConfig struct {
	Provider  string `yaml:"provider"`
	BaseURL   string `yaml:"base_url"`
	Model     string `yaml:"model"`
	Dim       int    `yaml:"dim"`
	Batch     int    `yaml:"batch"`
	TimeoutMS int    `yaml:"timeout_ms"`
	Retries   int    `yaml:"retries"`
}
```

Replace with:

```go
// EmbeddingConfig holds embedder settings.
type EmbeddingConfig struct {
	Provider  string `yaml:"provider"`   // "ollama" (default) | "onnx"
	BaseURL   string `yaml:"base_url"`   // for provider=ollama
	Model     string `yaml:"model"`      // for provider=ollama
	Dim       int    `yaml:"dim"`        // embedding dimension (768)
	Batch     int    `yaml:"batch"`      // texts per forward pass
	TimeoutMS int    `yaml:"timeout_ms"` // per-batch timeout
	Retries   int    `yaml:"retries"`    // for provider=ollama

	// ONNX-specific fields (only used when provider=onnx).
	ModelDir  string `yaml:"model_dir"`   // e.g. /models/nomic-embed-text-v1.5
	MaxSeqLen int    `yaml:"max_seq_len"` // max token sequence length (default 8192)
}
```

#### Exact change 2: add defaults

In `Defaults()`, find the `Embedding:` block. Add `ModelDir` and `MaxSeqLen`
defaults. The block currently ends after `Retries`. Add two lines:

```go
		Embedding: EmbeddingConfig{
			Provider:  "ollama",
			BaseURL:   "http://localhost:11434",
			Model:     "nomic-embed-text",
			Dim:       768,
			Batch:     32,
			TimeoutMS: 30000,
			Retries:   3,
			ModelDir:  "",    // must be set when provider=onnx
			MaxSeqLen: 8192,
		},
```

#### Exact change 3: add env overrides

In `applyEnvOverrides()`, after the existing `setInt("ENGRAM_EMBEDDING_RETRIES", ...)` line, add:

```go
	setStr("ENGRAM_EMBEDDING_MODEL_DIR", &cfg.Embedding.ModelDir)
	setInt("ENGRAM_EMBEDDING_MAX_SEQ_LEN", &cfg.Embedding.MaxSeqLen)
```

#### Exact change 4: add validation

In `validate()`, find where embedding validation occurs (around
`if cfg.Embedding.Dim <= 0`). After the existing embedding validation block,
add:

```go
	if cfg.Embedding.Provider == "onnx" {
		if strings.TrimSpace(cfg.Embedding.ModelDir) == "" {
			errs = append(errs, "embedding.model_dir must be set when provider=onnx")
		} else {
			if _, err := os.Stat(filepath.Join(cfg.Embedding.ModelDir, "model.onnx")); err != nil {
				errs = append(errs, fmt.Sprintf(
					"embedding.model_dir %q: model.onnx not found (sidecar may not have finished)",
					cfg.Embedding.ModelDir,
				))
			}
			if _, err := os.Stat(filepath.Join(cfg.Embedding.ModelDir, "tokenizer.json")); err != nil {
				errs = append(errs, fmt.Sprintf(
					"embedding.model_dir %q: tokenizer.json not found",
					cfg.Embedding.ModelDir,
				))
			}
		}
	}
```

Make sure `path/filepath` is in the import block. It may already be present.
If not, add it.

#### Verification

```bash
go build ./internal/config/...
go test ./internal/config/... -count=1
```

---

### WS-B2 — main.go Wiring

**Agent:** `lo-coder-q4` | **Depends on:** WS-A1, WS-A2 | **Est:** 30 min

Do not start this workstream until WS-A2 verification passes.

#### File to edit

1. `cmd/engram/main.go`

#### Exact change 1: add import

In the import block, `embed` is already imported as
`"github.com/gregdhill/engram/internal/embed"`. No new import needed.

#### Exact change 2: replace embedder construction

Find lines 91-97 in `cmd/engram/main.go`:

```go
	embedder := embed.NewOllamaEmbedder(embed.Config{
		BaseURL: cfg.Embedding.BaseURL,
		Model:   cfg.Embedding.Model,
		Dim:     cfg.Embedding.Dim,
		Batch:   cfg.Embedding.Batch,
		Timeout: time.Duration(cfg.Embedding.TimeoutMS) * time.Millisecond,
		Retries: cfg.Embedding.Retries,
	})
```

Replace with:

```go
	ollamaEmbedder := embed.NewOllamaEmbedder(embed.Config{
		BaseURL: cfg.Embedding.BaseURL,
		Model:   cfg.Embedding.Model,
		Dim:     cfg.Embedding.Dim,
		Batch:   cfg.Embedding.Batch,
		Timeout: time.Duration(cfg.Embedding.TimeoutMS) * time.Millisecond,
		Retries: cfg.Embedding.Retries,
	})

	var embedder embed.Embedder = ollamaEmbedder
	if cfg.Embedding.Provider == "onnx" {
		onnxEmb, onnxErr := embed.NewONNXEmbedder(embed.ONNXConfig{
			ModelDir:  cfg.Embedding.ModelDir,
			MaxSeqLen: cfg.Embedding.MaxSeqLen,
			Dim:       cfg.Embedding.Dim,
			BatchSize: cfg.Embedding.Batch,
			Timeout:   time.Duration(cfg.Embedding.TimeoutMS) * time.Millisecond,
		})
		if onnxErr != nil {
			slog.Warn("onnx embedder failed; falling back to ollama",
				"err", onnxErr,
				"model_dir", cfg.Embedding.ModelDir,
			)
		} else {
			embedder = onnxEmb
			slog.Info("onnx embedder initialized", "model_dir", cfg.Embedding.ModelDir)
		}
	}
```

#### Exact change 3: update retriever query embedding

Search for the call in `cmd/engram/main.go` or `internal/memory/retriever.go`
where `embedder.Embed(` is called with a query (single string, retrieval path).
Change it to `embedder.EmbedQuery(`. Also change any ingest-path calls from
`embedder.Embed(` to `embedder.EmbedBatch(`.

Run this grep first to find all callers:

```bash
grep -rn "embedder\.Embed\|\.Embed(ctx" --include="*.go" .
```

Update each one appropriately. If the context is ingest (writing to store),
use `EmbedBatch`. If the context is query (reading, retrieval), use
`EmbedQuery`.

#### Verification

```bash
go build ./...
go vet ./...
go test ./internal/memory/... ./internal/embed/... -count=1 -timeout 60s
```

---

### WS-B3 — YAML Config Updates

**Agent:** `lo-coder-q4` | **Independent** | **Est:** 5 min

#### Files to edit

1. `engram.yaml`
2. `engram.local.yaml`

#### `engram.yaml`

Find the `embedding:` section. Add the two new fields as comments below the
existing commented fields. The result should look like:

```yaml
embedding:
  provider: ollama
  base_url: http://localhost:11434
  model: nomic-embed-text
  dim: 768
  batch: 32
  timeout_ms: 30000
  retries: 3
  # ONNX provider (requires -tags onnxembed build and model sidecar):
  # provider: onnx
  # model_dir: /models/nomic-embed-text-v1.5
  # max_seq_len: 8192
```

#### `engram.local.yaml`

Leave the embedding provider as `ollama` (local dev still uses Ollama unless
explicitly switched). Add the new fields commented:

```yaml
  # To use ONNX embedder locally:
  # provider: onnx
  # model_dir: /models/nomic-embed-text-v1.5
  # max_seq_len: 8192
```

#### Verification

```bash
go test ./internal/config/... -count=1
```

---

### WS-B4 — Model Fetch Script

**Agent:** `lo-coder-q4` | **Independent** | **Est:** 10 min

#### File to create

`scripts/fetch-models.sh`

```bash
#!/usr/bin/env bash
# fetch-models.sh — downloads nomic-embed-text-v1.5 ONNX model files
# from HuggingFace into MODEL_DIR (default: /models/nomic-embed-text-v1.5).
#
# Uses -z (time-conditional) so existing up-to-date files are not re-downloaded.
# Set HF_REVISION to a pinned commit SHA for reproducibility.
#
# Usage:
#   MODEL_DIR=/models/nomic-embed-text-v1.5 HF_REVISION=<sha> bash scripts/fetch-models.sh
set -euo pipefail

MODEL_DIR="${MODEL_DIR:-/models/nomic-embed-text-v1.5}"
HF_REPO="nomic-ai/nomic-embed-text-v1.5"
HF_REVISION="${HF_REVISION:-16999335555c8808544a0344d2d4d9834ba70404}"

if [ "$HF_REVISION" = "PLACEHOLDER_PIN_ME" ]; then
    echo "ERROR: HF_REVISION must be set to a pinned commit SHA" >&2
    exit 1
fi

echo "Fetching nomic-embed-text-v1.5 into ${MODEL_DIR} @ ${HF_REVISION}"
mkdir -p "$MODEL_DIR"

# -f: fail on HTTP errors
# -L: follow redirects (HuggingFace uses LFS redirect)
# -z: skip download if local file is newer than remote
# --retry 3: transient network failures
# ?download=true: required for HuggingFace LFS files
curl -fL --retry 3 \
    -z "${MODEL_DIR}/model.onnx" \
    -o "${MODEL_DIR}/model.onnx" \
    "https://huggingface.co/${HF_REPO}/resolve/${HF_REVISION}/onnx/model.onnx?download=true"

curl -fL --retry 3 \
    -z "${MODEL_DIR}/tokenizer.json" \
    -o "${MODEL_DIR}/tokenizer.json" \
    "https://huggingface.co/${HF_REPO}/resolve/${HF_REVISION}/tokenizer.json?download=true"

echo "Done. Model files in ${MODEL_DIR}:"
ls -lh "${MODEL_DIR}"
```

Make it executable:

```bash
chmod +x scripts/fetch-models.sh
```

#### Verification

```bash
bash -n scripts/fetch-models.sh   # syntax check only, no download
```

---

### WS-B5 — Docker Compose Sidecar

**Agent:** `lo-coder-q4` | **Independent** | **Est:** 20 min

#### File to edit

1. `deploy/docker-compose.yml`

#### Exact change 1: add model-init service

Add the following service before the `engram:` service definition:

```yaml
  model-init:
    image: curlimages/curl:latest
    user: root
    volumes:
      - models:/models
      - ../scripts/fetch-models.sh:/fetch-models.sh:ro
    environment:
      MODEL_DIR: /models/nomic-embed-text-v1.5
      HF_REVISION: ${HF_REVISION:-16999335555c8808544a0344d2d4d9834ba70404}
    entrypoint: ["sh", "/fetch-models.sh"]
    restart: "no"
    networks:
      - engram
```

**Note:** Do NOT add `model-init` to `engram`'s `depends_on`. Engram falls
back to Ollama if the model is absent. Graceful degradation is intentional.

#### Exact change 2: add model volume mount to engram service

In the `engram:` service, add to `volumes:`:

```yaml
      - models:/models:ro
```

In the `engram:` service, add to `environment:`:

```yaml
      ENGRAM_EMBEDDING_MODEL_DIR: /models/nomic-embed-text-v1.5
      ENGRAM_EMBEDDING_PROVIDER: ${ENGRAM_EMBEDDING_PROVIDER:-ollama}
```

#### Exact change 3: add models volume

In the top-level `volumes:` block, add:

```yaml
  models:
```

The volumes block should now read:

```yaml
volumes:
  pgdata:
  qdrantdata:
  ollamadata:
  neo4jdata:
  models:
```

#### Verification

```bash
docker compose -f deploy/docker-compose.yml config >/dev/null
# Must exit 0 with no YAML parse errors.
```

---

### WS-B6 — Multistage Dockerfile

**Agent:** `lo-coder-q4` | **Depends on:** WS-A2 (for known CGO link flags) | **Est:** 45 min

Do not start this workstream until WS-A2 verification passes and ac-sonnet
has confirmed the exact CGO_LDFLAGS required by daulet/tokenizers.

#### File to replace entirely

`deploy/Dockerfile`

The new Dockerfile has three stages:

**Stage 1: `rust-builder`** — compiles the Rust tokenizers shim required by
`daulet/tokenizers`. The `daulet/tokenizers` package ships a `tokenizers/`
directory with a Rust crate that must be compiled into a static library. The
Go CGO bindings link against this library.

**Stage 2: `go-builder`** — builds the Go binary with CGO_ENABLED=1 and
`-tags onnxembed`. Links against the Rust static lib from Stage 1.

**Stage 3: `runtime`** — minimal runtime image. Copies the binary and any
required `.so` files (onnxruntime shared library).

```dockerfile
# syntax=docker/dockerfile:1

# ─── Stage 1: Build the Rust tokenizer shim ────────────────────────────────
FROM rust:1-bookworm AS rust-builder
WORKDIR /rust

# Copy only the Rust source from the daulet/tokenizers vendored crate.
# The path inside the Go module vendor directory is:
#   vendor/github.com/daulet/tokenizers/tokenizers/
# We copy the whole vendor dir and build in-place.
COPY vendor/github.com/daulet/tokenizers/tokenizers/ ./
RUN cargo build --release 2>&1
# Output: target/release/libtokenizers.a (static) and/or libtokenizers.so (shared)

# ─── Stage 2: Build the Go binary ──────────────────────────────────────────
FROM golang:1.23-bookworm AS go-builder
WORKDIR /src

# Install onnxruntime shared library (required at link time for onnxruntime_go).
# Use the same version pinned in go.mod: v1.28.0
ARG ONNXRUNTIME_VERSION=1.20.1
RUN apt-get update && apt-get install -y --no-install-recommends wget && \
    wget -q "https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}/onnxruntime-linux-x64-${ONNXRUNTIME_VERSION}.tgz" \
        -O /tmp/ort.tgz && \
    tar -xzf /tmp/ort.tgz -C /usr/local/lib --strip-components=2 \
        "onnxruntime-linux-x64-${ONNXRUNTIME_VERSION}/lib/" && \
    ldconfig && rm /tmp/ort.tgz

# Copy Rust static lib from Stage 1.
COPY --from=rust-builder /rust/target/release/libtokenizers.a /usr/local/lib/libtokenizers.a

# Copy Go source.
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Build default binary (no CGO, no onnxembed) — keeps the standard image small.
ARG BUILD_TAGS=""
RUN if [ -z "$BUILD_TAGS" ]; then \
        CGO_ENABLED=0 go build -ldflags="-s -w" -o /engram ./cmd/engram/; \
    else \
        CGO_ENABLED=1 \
        CGO_LDFLAGS="-L/usr/local/lib -ltokenizers -lonnxruntime" \
        go build -tags "$BUILD_TAGS" -ldflags="-s -w" -o /engram ./cmd/engram/; \
    fi

# ─── Stage 3: Runtime ──────────────────────────────────────────────────────
# Use cc-debian12 (includes libc) for CGO builds; static for default builds.
FROM gcr.io/distroless/cc-debian12 AS runtime
COPY --from=go-builder /engram /engram
COPY engram.yaml /engram.yaml

# Copy onnxruntime .so for runtime linking (only needed for onnxembed builds;
# harmless if present in non-onnxembed builds).
COPY --from=go-builder /usr/local/lib/libonnxruntime.so* /usr/local/lib/

ENTRYPOINT ["/engram"]
CMD ["--config", "/engram.yaml"]
```

#### Note for executing agent

The exact `CGO_LDFLAGS` values above (`-ltokenizers -lonnxruntime`) must be
confirmed against what `daulet/tokenizers` actually requires. After WS-A2
passes locally, run:

```bash
CGO_ENABLED=1 go build -tags onnxembed -v -work ./cmd/engram/ 2>&1 | grep cgo
```

and adjust the Dockerfile flags accordingly. If daulet/tokenizers uses a
different library name or requires additional flags, update Stage 2.

#### Verification

```bash
# Default build (no tags) — fast sanity check:
docker build -f deploy/Dockerfile -t engram:test .

# ONNX build:
docker build -f deploy/Dockerfile --build-arg BUILD_TAGS=onnxembed -t engram:onnx .
```

Both must exit 0.

---

### WS-B7 — CI Integration-ONNX Job

**Agent:** `lo-coder-q4` | **Independent (can run in parallel with A)** | **Est:** 30 min

#### File to edit

1. `.github/workflows/ci.yml`

#### Exact change: add `integration-onnx` job

Append the following job after the existing `integration` job:

```yaml
  integration-onnx:
    name: Integration tests (ONNX embedder)
    runs-on: ubuntu-latest
    if: "!contains(github.event.head_commit.message, '[skip-onnx]')"

    steps:
      - uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.25'
          cache: true

      - name: Set up Rust toolchain
        uses: dtolnay/rust-toolchain@stable

      - name: Cache model files
        uses: actions/cache@v4
        with:
          path: /tmp/models
          key: nomic-embed-text-v1.5-16999335555c8808544a0344d2d4d9834ba70404
          restore-keys: |
            nomic-embed-text-v1.5-

      - name: Fetch model files
        run: |
          MODEL_DIR=/tmp/models/nomic-embed-text-v1.5 \
          HF_REVISION=16999335555c8808544a0344d2d4d9834ba70404 \
          bash scripts/fetch-models.sh

      - name: Install ONNX Runtime
        run: |
          ORT_VERSION=1.20.1
          wget -q "https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-linux-x64-${ORT_VERSION}.tgz" \
              -O /tmp/ort.tgz
          sudo tar -xzf /tmp/ort.tgz -C /usr/local/lib \
              --strip-components=2 "onnxruntime-linux-x64-${ORT_VERSION}/lib/"
          sudo ldconfig

      - name: Build with onnxembed tag
        run: CGO_ENABLED=1 go build -tags onnxembed ./...

      - name: Run ONNX embed tests
        run: |
          CGO_ENABLED=1 go test -tags onnxembed -count=1 -v \
            ./internal/embed/... -timeout 120s
        env:
          ENGRAM_TEST_MODEL_DIR: /tmp/models/nomic-embed-text-v1.5

      - name: Run full unit tests with onnxembed tag
        run: |
          CGO_ENABLED=1 go test -tags onnxembed -count=1 \
            ./internal/... ./cmd/... -timeout 120s
        env:
          ENGRAM_TEST_MODEL_DIR: /tmp/models/nomic-embed-text-v1.5
```

#### Verification

Push a branch and confirm the `integration-onnx` job appears in GitHub
Actions. It should be skippable with `[skip-onnx]` in the commit message.

---

## Execution Order

```
Dispatch simultaneously:

  Track A (ac-sonnet):
    WS-A1 (~20m) → WS-A2 (~3-4h) ────────────────────┐
                                                       │
  Track B (lo-coder-q4, all parallel with each other): │
    WS-B1 config (~20m) ───────────────────────────────┤
    WS-B3 yaml (~5m)  ─────────────────────────────────┤
    WS-B4 fetch script (~10m)  ────────────────────────┤
    WS-B5 compose (~20m)  ─────────────────────────────┤
    WS-B7 CI (~30m)  ──────────────────────────────────┤
                                                       ↓
                                 WS-B2 main.go wiring (depends on A1+A2)
                                                       ↓
                                 WS-B6 Dockerfile (depends on A2 for link flags)
                                                       ↓
                                        Final: go build ./...
                                                go vet ./...
                                               go test ./...
```

---

## Cost Routing

| Tier | Model | Used for |
|------|-------|----------|
| Paid (real) | `ac-sonnet` | WS-A1, WS-A2 — interface design + ONNX session wiring |
| Free / local | `lo-coder-q4` | WS-B1 through WS-B7 — mechanical edits with full prescription |

---

## Success Criteria

- [ ] `go build ./...` and `go test ./...` pass with no changes to default behavior
- [ ] `CGO_ENABLED=1 go build -tags onnxembed ./...` compiles cleanly
- [ ] All 6 ONNX embed tests pass against fetched model
- [ ] Cosine sim: `sim("cat","kitten") > sim("cat","airplane")` by margin > 0.1
- [ ] L2 norm of all output vectors ≈ 1.0 (within 1e-4)
- [ ] Padding invariance: identical text produces identical vector regardless of batch companion
- [ ] `EmbedQuery` and `EmbedBatch` produce measurably different vectors (prefix applied)
- [ ] `docker compose up` starts model-init sidecar, fetches model, engram starts on Ollama (default)
- [ ] `ENGRAM_EMBEDDING_PROVIDER=onnx` activates ONNX path; missing model falls back to Ollama with warning
- [ ] Bulk ingest of a 100-page document completes in <30s with `provider: onnx`
- [ ] CI `integration-onnx` job green; model cache reused on subsequent runs (~5s vs full download)
- [ ] All 11 existing integration tests still pass (no regression)
