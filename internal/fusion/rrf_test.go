package fusion

import (
	"math"
	"reflect"
	"testing"
)

const eps = 1e-9

// rrf returns 1/(k+rank), rank is 1-based.
func rrf(k float64, rank int) float64 {
	return 1.0 / (k + float64(rank))
}

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) <= eps
}

// expectedResult is a small struct to compare Score + IDs with epsilon.
type expectedResult struct {
	ChunkID  string
	MemoryID string
	Score    float64
	Content  string
}

func assertResults(t *testing.T, got []Result, want []expectedResult) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %d, want %d; got=%+v", len(got), len(want), got)
	}
	for i := range got {
		if got[i].ChunkID != want[i].ChunkID {
			t.Errorf("idx %d ChunkID: got %q want %q", i, got[i].ChunkID, want[i].ChunkID)
		}
		if got[i].MemoryID != want[i].MemoryID {
			t.Errorf("idx %d MemoryID: got %q want %q", i, got[i].MemoryID, want[i].MemoryID)
		}
		if !approxEqual(got[i].Score, want[i].Score) {
			t.Errorf("idx %d Score: got %.17g want %.17g", i, got[i].Score, want[i].Score)
		}
		if want[i].Content != "" && got[i].Content != want[i].Content {
			t.Errorf("idx %d Content: got %q want %q", i, got[i].Content, want[i].Content)
		}
	}
}

func defaultCfg() Config {
	return Config{K: 60, VectorFloor: 0.25, BM25K: 10}
}

func TestFuse_Disjoint(t *testing.T) {
	cfg := defaultCfg()
	vec := []VecHit{
		{ChunkID: "v1", MemoryID: "m1", Score: 0.9},
		{ChunkID: "v2", MemoryID: "m2", Score: 0.8},
		{ChunkID: "v3", MemoryID: "m3", Score: 0.7},
	}
	bm := []BM25Hit{
		{ChunkID: "b1", MemoryID: "m4", Content: "c1", Rank: 0.5},
		{ChunkID: "b2", MemoryID: "m5", Content: "c2", Rank: 0.4},
		{ChunkID: "b3", MemoryID: "m6", Content: "c3", Rank: 0.3},
	}
	got := Fuse(cfg, vec, bm)
	// All six chunks eligible. v1 and b1 both score rrf(60,1) = 1/61 → tie
	// Tiebreak by ChunkID ascending: b1 < v1.
	want := []expectedResult{
		{ChunkID: "b1", MemoryID: "m4", Score: rrf(60, 1), Content: "c1"},
		{ChunkID: "v1", MemoryID: "m1", Score: rrf(60, 1)},
		{ChunkID: "b2", MemoryID: "m5", Score: rrf(60, 2), Content: "c2"},
		{ChunkID: "v2", MemoryID: "m2", Score: rrf(60, 2)},
		{ChunkID: "b3", MemoryID: "m6", Score: rrf(60, 3), Content: "c3"},
		{ChunkID: "v3", MemoryID: "m3", Score: rrf(60, 3)},
	}
	assertResults(t, got, want)
}

func TestFuse_FullyOverlapping(t *testing.T) {
	cfg := defaultCfg()
	vec := []VecHit{
		{ChunkID: "a", MemoryID: "ma", Score: 0.9},
		{ChunkID: "b", MemoryID: "mb", Score: 0.8},
		{ChunkID: "c", MemoryID: "mc", Score: 0.7},
	}
	bm := []BM25Hit{
		{ChunkID: "a", MemoryID: "ma", Content: "ca", Rank: 0.5},
		{ChunkID: "b", MemoryID: "mb", Content: "cb", Rank: 0.4},
		{ChunkID: "c", MemoryID: "mc", Content: "cc", Rank: 0.3},
	}
	got := Fuse(cfg, vec, bm)
	want := []expectedResult{
		{ChunkID: "a", MemoryID: "ma", Score: rrf(60, 1) + rrf(60, 1), Content: "ca"},
		{ChunkID: "b", MemoryID: "mb", Score: rrf(60, 2) + rrf(60, 2), Content: "cb"},
		{ChunkID: "c", MemoryID: "mc", Score: rrf(60, 3) + rrf(60, 3), Content: "cc"},
	}
	assertResults(t, got, want)
	// Confirm overlap scores higher than single-list
	if !(got[0].Score > rrf(60, 1)+eps) {
		t.Errorf("overlap should exceed single-list contribution")
	}
}

func TestFuse_EmptyVec(t *testing.T) {
	cfg := defaultCfg()
	bm := []BM25Hit{
		{ChunkID: "b1", MemoryID: "m1", Content: "x", Rank: 0.5},
		{ChunkID: "b2", MemoryID: "m2", Content: "y", Rank: 0.4},
	}
	got := Fuse(cfg, nil, bm)
	want := []expectedResult{
		{ChunkID: "b1", MemoryID: "m1", Score: rrf(60, 1), Content: "x"},
		{ChunkID: "b2", MemoryID: "m2", Score: rrf(60, 2), Content: "y"},
	}
	assertResults(t, got, want)
}

func TestFuse_EmptyBM25(t *testing.T) {
	cfg := defaultCfg()
	vec := []VecHit{
		{ChunkID: "v1", MemoryID: "m1", Score: 0.9},
		{ChunkID: "v2", MemoryID: "m2", Score: 0.8},
	}
	got := Fuse(cfg, vec, nil)
	want := []expectedResult{
		{ChunkID: "v1", MemoryID: "m1", Score: rrf(60, 1)},
		{ChunkID: "v2", MemoryID: "m2", Score: rrf(60, 2)},
	}
	assertResults(t, got, want)
}

func TestFuse_BothEmpty(t *testing.T) {
	cfg := defaultCfg()
	got := Fuse(cfg, nil, nil)
	if got == nil {
		t.Fatalf("expected non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %+v", got)
	}
}

func TestFuse_VectorFloorExclusion(t *testing.T) {
	cfg := Config{K: 60, VectorFloor: 0.25, BM25K: 10}
	// v1: above floor, in vec only          → eligible (vec leg)
	// v2: BELOW floor (0.20), in vec only   → excluded entirely
	// v3: BELOW floor (0.20), also in bm25  → eligible via bm25 leg, gets BOTH contributions
	vec := []VecHit{
		{ChunkID: "v1", MemoryID: "m1", Score: 0.9}, // rank 1
		{ChunkID: "v2", MemoryID: "m2", Score: 0.20}, // rank 2 — excluded
		{ChunkID: "v3", MemoryID: "m3", Score: 0.20}, // rank 3 — eligible via bm25
	}
	bm := []BM25Hit{
		{ChunkID: "v3", MemoryID: "m3", Content: "shared", Rank: 0.5}, // rank 1
	}
	got := Fuse(cfg, vec, bm)
	// v3 score = rrf(60,3) [vec] + rrf(60,1) [bm25]
	// v1 score = rrf(60,1)
	// v3 vs v1: rrf(60,3)+rrf(60,1) ≈ 0.0158730+0.0163934 = 0.0322664 > 0.0163934 → v3 first
	want := []expectedResult{
		{ChunkID: "v3", MemoryID: "m3", Score: rrf(60, 3) + rrf(60, 1), Content: "shared"},
		{ChunkID: "v1", MemoryID: "m1", Score: rrf(60, 1)},
	}
	assertResults(t, got, want)
	// Confirm v2 absent
	for _, r := range got {
		if r.ChunkID == "v2" {
			t.Errorf("v2 should have been excluded (below floor and not in bm25)")
		}
	}
}

func TestFuse_BM25RankExclusion(t *testing.T) {
	cfg := Config{K: 60, VectorFloor: 0.25, BM25K: 2}
	// bm25 list of length 3; rank-3 entry is past BM25K=2.
	// b3: at bm25 rank 3, NOT in vec        → excluded entirely
	// b3v: at bm25 rank 3 AND in vec ≥ floor → eligible via vec; gets BOTH contributions
	vec := []VecHit{
		{ChunkID: "b3v", MemoryID: "mv", Score: 0.9}, // rank 1
	}
	bm := []BM25Hit{
		{ChunkID: "b1", MemoryID: "m1", Content: "x", Rank: 0.5},   // rank 1
		{ChunkID: "b2", MemoryID: "m2", Content: "y", Rank: 0.4},   // rank 2
		{ChunkID: "b3", MemoryID: "m3", Content: "z", Rank: 0.3},   // rank 3 — past BM25K
		{ChunkID: "b3v", MemoryID: "mv", Content: "shared", Rank: 0.2}, // rank 4 — past BM25K
	}
	got := Fuse(cfg, vec, bm)
	// b1: rrf(60,1) eligible
	// b2: rrf(60,2) eligible
	// b3: excluded (past BM25K, not in vec)
	// b3v: rrf(60,1) [vec] + rrf(60,4) [bm25, contributes despite ineligibility there] = eligible via vec
	want := []expectedResult{
		// b1 and b3v both have rrf(60,1) as part of their score — compute totals:
		// b3v = rrf(60,1) + rrf(60,4) ≈ 0.0163934 + 0.0156250 = 0.0320184
		// b1  = rrf(60,1) ≈ 0.0163934
		// So b3v > b1 > b2.
		{ChunkID: "b3v", MemoryID: "mv", Score: rrf(60, 1) + rrf(60, 4), Content: "shared"},
		{ChunkID: "b1", MemoryID: "m1", Score: rrf(60, 1), Content: "x"},
		{ChunkID: "b2", MemoryID: "m2", Score: rrf(60, 2), Content: "y"},
	}
	assertResults(t, got, want)
	for _, r := range got {
		if r.ChunkID == "b3" {
			t.Errorf("b3 should have been excluded (past BM25K, not in vec)")
		}
	}
}

func TestFuse_StableSortByChunkID(t *testing.T) {
	cfg := defaultCfg()
	// Three chunks at the same RRF rank from the bm25 list (pos 1) — but only one
	// can be at position 1. Instead, construct a tie via overlap:
	// "z" appears at vec rank 1 only           → score = rrf(60,1)
	// "a" appears at bm25 rank 1 only          → score = rrf(60,1)
	// "m" appears at bm25 rank 2 only          → score = rrf(60,2) (different)
	// → "a" and "z" tie. Tiebreak: "a" before "z".
	vec := []VecHit{{ChunkID: "z", MemoryID: "mz", Score: 0.9}}
	bm := []BM25Hit{
		{ChunkID: "a", MemoryID: "ma", Content: "ca", Rank: 0.9},
		{ChunkID: "m", MemoryID: "mm", Content: "cm", Rank: 0.5},
	}
	got := Fuse(cfg, vec, bm)
	want := []expectedResult{
		{ChunkID: "a", MemoryID: "ma", Score: rrf(60, 1), Content: "ca"},
		{ChunkID: "z", MemoryID: "mz", Score: rrf(60, 1)},
		{ChunkID: "m", MemoryID: "mm", Score: rrf(60, 2), Content: "cm"},
	}
	assertResults(t, got, want)
}

func TestFuse_Determinism(t *testing.T) {
	cfg := defaultCfg()
	vec := []VecHit{
		{ChunkID: "v1", MemoryID: "m1", Score: 0.9, Payload: map[string]any{"k": 1}},
		{ChunkID: "shared", MemoryID: "ms", Score: 0.8},
		{ChunkID: "v3", MemoryID: "m3", Score: 0.7},
	}
	bm := []BM25Hit{
		{ChunkID: "shared", MemoryID: "ms", Content: "s", Rank: 0.9},
		{ChunkID: "b2", MemoryID: "m2", Content: "b2", Rank: 0.5},
	}
	first := Fuse(cfg, vec, bm)
	second := Fuse(cfg, vec, bm)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("non-deterministic results:\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func TestFuse_DefaultK(t *testing.T) {
	// K<=0 should default to 60.
	cfg := Config{K: 0, VectorFloor: 0.0, BM25K: 10}
	vec := []VecHit{{ChunkID: "x", MemoryID: "m", Score: 1.0}}
	got := Fuse(cfg, vec, nil)
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if !approxEqual(got[0].Score, rrf(60, 1)) {
		t.Errorf("default K not applied: got %.17g want %.17g", got[0].Score, rrf(60, 1))
	}
}
