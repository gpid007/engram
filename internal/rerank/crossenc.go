//go:build crossenc

package rerank

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"sort"
	"strings"
	"time"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	maxSeqLen = 512
	clsToken  = 101 // [CLS]
	sepToken  = 102 // [SEP]
	padToken  = 0   // [PAD]
)

// tokenize splits text on whitespace and maps each word to a deterministic
// integer ID using FNV-32a hashing, then shifts into [1000, 30000) to
// avoid collision with special-token IDs.
func tokenize(text string) []int64 {
	words := strings.Fields(text)
	ids := make([]int64, 0, len(words))
	h := fnv.New32a()
	for _, w := range words {
		h.Reset()
		_, _ = h.Write([]byte(strings.ToLower(w)))
		id := int64(h.Sum32()%29000) + 1000
		ids = append(ids, id)
	}
	return ids
}

// buildInput constructs the three int64 tensors required by BERT-style
// cross-encoders: input_ids, attention_mask, token_type_ids.
// Layout: [CLS] query… [SEP] doc… [SEP] <padding…>
func buildInput(query, doc string) (inputIDs, attentionMask, tokenTypeIDs []int64) {
	qToks := tokenize(query)
	dToks := tokenize(doc)

	// Reserve 3 slots for [CLS], [SEP] (after query), [SEP] (after doc).
	available := maxSeqLen - 3
	if len(qToks) > available {
		qToks = qToks[:available]
	}
	remaining := available - len(qToks)
	if len(dToks) > remaining {
		dToks = dToks[:remaining]
	}

	seqLen := 1 + len(qToks) + 1 + len(dToks) + 1 // CLS + q + SEP + d + SEP

	inputIDs = make([]int64, maxSeqLen)
	attentionMask = make([]int64, maxSeqLen)
	tokenTypeIDs = make([]int64, maxSeqLen)

	pos := 0
	inputIDs[pos] = clsToken
	attentionMask[pos] = 1
	tokenTypeIDs[pos] = 0
	pos++

	for _, id := range qToks {
		inputIDs[pos] = id
		attentionMask[pos] = 1
		tokenTypeIDs[pos] = 0
		pos++
	}

	inputIDs[pos] = sepToken
	attentionMask[pos] = 1
	tokenTypeIDs[pos] = 0
	pos++

	for _, id := range dToks {
		inputIDs[pos] = id
		attentionMask[pos] = 1
		tokenTypeIDs[pos] = 1
		pos++
	}

	inputIDs[pos] = sepToken
	attentionMask[pos] = 1
	tokenTypeIDs[pos] = 1
	pos++

	_ = seqLen // padding positions already zero-initialised
	_ = pos

	return inputIDs, attentionMask, tokenTypeIDs
}

// CrossEncoderReranker scores query–document pairs with a BGE-reranker-base
// ONNX model and reorders candidates by descending relevance.
type CrossEncoderReranker struct {
	modelPath string
	timeout   time.Duration
	session   *ort.DynamicAdvancedSession
}

// NewCrossEncoderReranker loads the ONNX model at modelPath.
// Returns an error if the file is absent or the runtime cannot initialise.
func NewCrossEncoderReranker(modelPath string, timeout time.Duration) (*CrossEncoderReranker, error) {
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("cross-encoder model not found at %q: %w", modelPath, err)
	}

	if !ort.IsInitialized() {
		if err := ort.InitializeEnvironment(); err != nil {
			return nil, fmt.Errorf("onnxruntime init failed: %w", err)
		}
	}

	inputNames := []string{"input_ids", "attention_mask", "token_type_ids"}
	outputNames := []string{"logits"}

	session, err := ort.NewDynamicAdvancedSession(modelPath, inputNames, outputNames, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create ONNX session: %w", err)
	}

	return &CrossEncoderReranker{
		modelPath: modelPath,
		timeout:   timeout,
		session:   session,
	}, nil
}

// Rerank scores every candidate against the query, sorts descending by score,
// and returns the reordered slice.
func (c *CrossEncoderReranker) Rerank(ctx context.Context, query string, candidates []Candidate) ([]Candidate, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}

	// Apply timeout if set.
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	shape := ort.NewShape(1, maxSeqLen)

	scored := make([]Candidate, len(candidates))
	copy(scored, candidates)

	for i, cand := range scored {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("rerank cancelled: %w", err)
		}

		inputIDs, attentionMask, tokenTypeIDs := buildInput(query, cand.Content)

		inIDs, err := ort.NewTensor(shape, inputIDs)
		if err != nil {
			return nil, fmt.Errorf("tensor creation failed for candidate %d: %w", i, err)
		}
		inMask, err := ort.NewTensor(shape, attentionMask)
		if err != nil {
			inIDs.Destroy()
			return nil, fmt.Errorf("tensor creation failed for candidate %d: %w", i, err)
		}
		inType, err := ort.NewTensor(shape, tokenTypeIDs)
		if err != nil {
			inIDs.Destroy()
			inMask.Destroy()
			return nil, fmt.Errorf("tensor creation failed for candidate %d: %w", i, err)
		}

		outLogits, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 1))
		if err != nil {
			inIDs.Destroy()
			inMask.Destroy()
			inType.Destroy()
			return nil, fmt.Errorf("output tensor creation failed for candidate %d: %w", i, err)
		}

		err = c.session.Run(
			[]ort.ArbitraryTensor{inIDs, inMask, inType},
			[]ort.ArbitraryTensor{outLogits},
		)

		inIDs.Destroy()
		inMask.Destroy()
		inType.Destroy()

		if err != nil {
			outLogits.Destroy()
			return nil, fmt.Errorf("inference failed for candidate %d: %w", i, err)
		}

		data := outLogits.GetData()
		outLogits.Destroy()

		if len(data) > 0 {
			scored[i].Score = float64(data[0])
		}
	}

	sort.Slice(scored, func(a, b int) bool {
		return scored[a].Score > scored[b].Score
	})

	return scored, nil
}
