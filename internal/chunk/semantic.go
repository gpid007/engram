// Package chunk implements semantic chunking.
package chunk

import (
	"context"
	"math"
	"strings"
	"unicode"

	"github.com/gregdhill/engram/internal/embed"
)

// Default config values.
const (
	defaultMaxTokens           = 512
	defaultMinTokens           = 100
	defaultSimilarityThreshold = 0.6
)

// Chunk is a semantic chunk of text with its mean-pooled embedding.
type Chunk struct {
	Content string
	EmbVec  []float32 // mean of sentence embeddings in this chunk
}

// Config controls chunking behaviour.
type Config struct {
	MaxTokens           int     // default 512
	MinTokens           int     // default 100
	SimilarityThreshold float64 // default 0.6
}

// Chunker splits text into semantic chunks.
type Chunker struct {
	embedder embed.Embedder
	cfg      Config
}

// NewChunker constructs a Chunker. Zero-valued Config fields are replaced with defaults.
func NewChunker(embedder embed.Embedder, cfg Config) *Chunker {
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = defaultMaxTokens
	}
	if cfg.MinTokens <= 0 {
		cfg.MinTokens = defaultMinTokens
	}
	if cfg.SimilarityThreshold == 0 {
		cfg.SimilarityThreshold = defaultSimilarityThreshold
	}
	return &Chunker{embedder: embedder, cfg: cfg}
}

// protoChunk is an in-progress chunk during accumulation.
type protoChunk struct {
	sentences  []string
	embeddings [][]float32
	tokens     int
	centroid   []float32
}

// Chunk splits text into semantic chunks.
func (c *Chunker) Chunk(ctx context.Context, text string) ([]Chunk, error) {
	sentences := splitSentences(text)
	if len(sentences) == 0 {
		return []Chunk{}, nil
	}

	// Embed all sentences in one batch.
	embeddings, err := c.embedder.Embed(ctx, sentences)
	if err != nil {
		return nil, err
	}
	if len(embeddings) != len(sentences) {
		// Defensive: embedder returned wrong number; treat as single chunk to avoid OOB.
		return []Chunk{{Content: strings.Join(sentences, " ")}}, nil
	}

	var chunks []protoChunk
	var current protoChunk

	flush := func() {
		if len(current.sentences) > 0 {
			chunks = append(chunks, current)
			current = protoChunk{}
		}
	}

	for i, sent := range sentences {
		emb := embeddings[i]
		tok := tokenCount(sent)

		if len(current.sentences) == 0 {
			current.sentences = append(current.sentences, sent)
			current.embeddings = append(current.embeddings, emb)
			current.tokens = tok
			current.centroid = cloneVec(emb)
			continue
		}

		sim := cosine(current.centroid, emb)
		exceedsMax := current.tokens+tok > c.cfg.MaxTokens

		if sim < c.cfg.SimilarityThreshold || exceedsMax {
			flush()
			current.sentences = append(current.sentences, sent)
			current.embeddings = append(current.embeddings, emb)
			current.tokens = tok
			current.centroid = cloneVec(emb)
			continue
		}

		current.sentences = append(current.sentences, sent)
		current.embeddings = append(current.embeddings, emb)
		current.tokens += tok
		current.centroid = meanVec(current.embeddings)
	}
	flush()

	// Enforce minimum chunk size: merge tiny chunks forward (or backward if last).
	chunks = mergeSmall(chunks, c.cfg.MinTokens)

	// Build output chunks with mean embedding and joined content.
	out := make([]Chunk, 0, len(chunks))
	for _, pc := range chunks {
		out = append(out, Chunk{
			Content: strings.Join(pc.sentences, " "),
			EmbVec:  meanVec(pc.embeddings),
		})
	}
	return out, nil
}

// mergeSmall merges chunks that fall below minTokens into a neighbour.
// Tiny chunks are merged forward into the next chunk; the last chunk merges backward.
func mergeSmall(chunks []protoChunk, minTokens int) []protoChunk {
	if len(chunks) <= 1 {
		return chunks
	}
	for {
		idx := -1
		for i, c := range chunks {
			if c.tokens < minTokens {
				idx = i
				break
			}
		}
		if idx == -1 {
			return chunks
		}
		var merged protoChunk
		var newChunks []protoChunk
		if idx == len(chunks)-1 {
			// Merge last into previous (idx-1).
			prev := chunks[idx-1]
			merged = protoChunk{
				sentences:  append(append([]string{}, prev.sentences...), chunks[idx].sentences...),
				embeddings: append(append([][]float32{}, prev.embeddings...), chunks[idx].embeddings...),
				tokens:     prev.tokens + chunks[idx].tokens,
			}
			merged.centroid = meanVec(merged.embeddings)
			newChunks = append(newChunks, chunks[:idx-1]...)
			newChunks = append(newChunks, merged)
		} else {
			next := chunks[idx+1]
			merged = protoChunk{
				sentences:  append(append([]string{}, chunks[idx].sentences...), next.sentences...),
				embeddings: append(append([][]float32{}, chunks[idx].embeddings...), next.embeddings...),
				tokens:     chunks[idx].tokens + next.tokens,
			}
			merged.centroid = meanVec(merged.embeddings)
			newChunks = append(newChunks, chunks[:idx]...)
			newChunks = append(newChunks, merged)
			newChunks = append(newChunks, chunks[idx+2:]...)
		}
		chunks = newChunks
		if len(chunks) <= 1 {
			return chunks
		}
	}
}

// --- sentence splitting ---

// splitSentences splits text into sentences, preserving punctuation on the left side.
// Crude abbreviation handling: if the token ending in `.` is ≤3 chars or all-caps, don't split.
func splitSentences(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var sentences []string
	var sb strings.Builder

	runes := []rune(text)
	i := 0
	for i < len(runes) {
		r := runes[i]
		sb.WriteRune(r)

		if r == '.' || r == '!' || r == '?' {
			// Consume any consecutive end-punctuation (e.g. "?!", "...").
			for i+1 < len(runes) && (runes[i+1] == '.' || runes[i+1] == '!' || runes[i+1] == '?') {
				i++
				sb.WriteRune(runes[i])
			}

			atEnd := i+1 >= len(runes)
			nextIsSpace := !atEnd && unicode.IsSpace(runes[i+1])

			if atEnd || nextIsSpace {
				// Suppress split for plausible abbreviations ending in a single `.`.
				if r == '.' && shouldSuppressSplit(sb.String()) {
					i++
					continue
				}

				sentence := strings.TrimSpace(sb.String())
				if sentence != "" {
					sentences = append(sentences, sentence)
				}
				sb.Reset()

				// Skip following whitespace.
				for i+1 < len(runes) && unicode.IsSpace(runes[i+1]) {
					i++
				}
			}
		}
		i++
	}

	// Trailing fragment without terminal punctuation.
	tail := strings.TrimSpace(sb.String())
	if tail != "" {
		sentences = append(sentences, tail)
	}

	return sentences
}

// shouldSuppressSplit returns true if the buffer ends with a probable abbreviation.
// Heuristic: last whitespace-delimited token (excluding trailing dots) is ≤3 chars,
// all-caps, or contains internal dots (e.g. "U.S.").
func shouldSuppressSplit(buf string) bool {
	trimmed := strings.TrimRight(buf, ".")
	if trimmed == "" {
		return false
	}
	lastSpace := strings.LastIndexFunc(trimmed, unicode.IsSpace)
	token := trimmed[lastSpace+1:]
	if token == "" {
		return false
	}
	if strings.Contains(token, ".") {
		return true
	}
	allLetters := true
	allUpper := true
	for _, r := range token {
		if !unicode.IsLetter(r) {
			allLetters = false
			allUpper = false
			break
		}
		if !unicode.IsUpper(r) {
			allUpper = false
		}
	}
	if !allLetters {
		return false
	}
	if len([]rune(token)) <= 3 {
		return true
	}
	if allUpper {
		return true
	}
	return false
}

// --- tokenization ---

// tokenCount returns a whitespace+punctuation word count (split on non-letter/digit boundaries).
func tokenCount(s string) int {
	count := 0
	inWord := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if !inWord {
				count++
				inWord = true
			}
		} else {
			inWord = false
		}
	}
	return count
}

// --- vector math ---

// cosine returns the cosine similarity between a and b.
// Returns 0 for zero-vectors or mismatched lengths.
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		ai := float64(a[i])
		bi := float64(b[i])
		dot += ai * bi
		na += ai * ai
		nb += bi * bi
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// meanVec returns the element-wise mean of vecs. Assumes all vectors share the same length.
func meanVec(vecs [][]float32) []float32 {
	if len(vecs) == 0 {
		return nil
	}
	dim := len(vecs[0])
	out := make([]float32, dim)
	for _, v := range vecs {
		if len(v) != dim {
			continue
		}
		for j, x := range v {
			out[j] += x
		}
	}
	n := float32(len(vecs))
	for j := range out {
		out[j] /= n
	}
	return out
}

// cloneVec returns a copy of v.
func cloneVec(v []float32) []float32 {
	out := make([]float32, len(v))
	copy(out, v)
	return out
}
