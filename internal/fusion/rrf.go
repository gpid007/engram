// Package fusion implements Reciprocal Rank Fusion.
package fusion

import "sort"

// VecHit is a result from vector search.
type VecHit struct {
	ChunkID  string
	MemoryID string
	Score    float32 // cosine similarity
	Payload  map[string]any
}

// BM25Hit is a result from BM25 full-text search.
type BM25Hit struct {
	ChunkID  string
	MemoryID string
	Content  string
	Rank     float64 // ts_rank score (higher = better); position in list is its 1-based rank
}

// Result is a fused result.
type Result struct {
	ChunkID  string
	MemoryID string
	Score    float64 // RRF score
	Content  string
	Payload  map[string]any
}

// Config controls fusion behaviour.
type Config struct {
	K           float64 // RRF k constant, default 60
	VectorFloor float32 // minimum cosine score for vec hits to be eligible
	BM25K       int     // max rank (1-based) for bm25 hits to be eligible
}

// entry tracks accumulated RRF state for a single chunk.
type entry struct {
	memoryID  string
	content   string
	payload   map[string]any
	score     float64
	eligible  bool
}

// Fuse merges vector and BM25 results using RRF.
// vecHits are ordered by cosine score descending (position = rank).
// bm25Hits are ordered by ts_rank descending (position = rank).
func Fuse(cfg Config, vecHits []VecHit, bm25Hits []BM25Hit) []Result {
	k := cfg.K
	if k <= 0 {
		k = 60
	}
	acc := make(map[string]*entry)
	get := func(id string) *entry {
		e, ok := acc[id]
		if !ok {
			e = &entry{}
			acc[id] = e
		}
		return e
	}
	for i, v := range vecHits {
		e := get(v.ChunkID)
		e.memoryID = v.MemoryID
		if v.Payload != nil {
			e.payload = v.Payload
		}
		e.score += 1.0 / (k + float64(i+1))
		if v.Score >= cfg.VectorFloor {
			e.eligible = true
		}
	}
	for i, b := range bm25Hits {
		e := get(b.ChunkID)
		if e.memoryID == "" {
			e.memoryID = b.MemoryID
		}
		if b.Content != "" {
			e.content = b.Content
		}
		e.score += 1.0 / (k + float64(i+1))
		if i+1 <= cfg.BM25K {
			e.eligible = true
		}
	}
	out := make([]Result, 0, len(acc))
	for id, e := range acc {
		if !e.eligible {
			continue
		}
		out = append(out, Result{ChunkID: id, MemoryID: e.memoryID, Score: e.score, Content: e.content, Payload: e.payload})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ChunkID < out[j].ChunkID
	})
	return out
}
