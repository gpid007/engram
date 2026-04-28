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
		enc, encErr := w.tk.EncodeWithOptionsErr(text, true /* addSpecialTokens */,
			tokenizers.WithReturnAttentionMask(),
		)
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
			// If mask is empty (tokenizer didn't return it), treat all as 1.
			if len(mask) > j {
				attentionMask[base+j] = int64(mask[j])
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
