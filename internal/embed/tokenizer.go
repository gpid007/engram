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

// encodeBatch tokenizes a slice of texts, padding/truncating each to seqLen.
// seqLen is the minimum of maxSeqLen and the longest tokenized text in the
// batch — this avoids allocating and running inference over thousands of
// padding tokens for short inputs (e.g. a 5-word query would otherwise use
// 8192 positions instead of ~10, a ~800x slowdown).
// Returns flat int64 slices of length len(texts)*seqLen for input_ids and
// attention_mask. Token type IDs are all zeros (nomic uses only two segments).
func (w *tokenizerWrapper) encodeBatch(texts []string, maxSeqLen int) (
	inputIDs, attentionMask []int64, err error,
) {
	n := len(texts)

	// First pass: tokenize all texts and find the actual max token length.
	type encoded struct {
		ids  []uint32
		mask []uint32
	}
	encs := make([]encoded, n)
	actualMax := 0
	for i, text := range texts {
		enc, encErr := w.tk.EncodeWithOptionsErr(text, true /* addSpecialTokens */,
			tokenizers.WithReturnAttentionMask(),
		)
		if encErr != nil {
			return nil, nil, fmt.Errorf("encode text[%d]: %w", i, encErr)
		}
		ids := enc.IDs
		mask := enc.AttentionMask
		// Truncate to hard max if necessary.
		if len(ids) > maxSeqLen {
			ids = ids[:maxSeqLen]
			mask = mask[:maxSeqLen]
		}
		encs[i] = encoded{ids: ids, mask: mask}
		if len(ids) > actualMax {
			actualMax = len(ids)
		}
	}

	// Use the actual batch max as sequence length — never more than maxSeqLen.
	seqLen := actualMax
	if seqLen == 0 {
		seqLen = 1 // degenerate case: empty input
	}

	// Second pass: pack into flat tensors padded to seqLen.
	inputIDs = make([]int64, n*seqLen)
	attentionMask = make([]int64, n*seqLen)
	for i, enc := range encs {
		base := i * seqLen
		for j, id := range enc.ids {
			inputIDs[base+j] = int64(id)
			if len(enc.mask) > j {
				attentionMask[base+j] = int64(enc.mask[j])
			} else {
				attentionMask[base+j] = 1
			}
		}
		// Remaining positions stay zero (padding).
	}
	return inputIDs, attentionMask, nil
}

// close releases the tokenizer's resources.
func (w *tokenizerWrapper) close() {
	w.tk.Close()
}
