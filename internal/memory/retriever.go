package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/gregdhill/engram/internal/embed"
	"github.com/gregdhill/engram/internal/fusion"
	"github.com/gregdhill/engram/internal/graph"
	"github.com/gregdhill/engram/internal/rerank"
	"github.com/gregdhill/engram/internal/store/postgres"
	"github.com/gregdhill/engram/internal/store/qdrant"
)

// RetrieveInput is the query input.
type RetrieveInput struct {
	Query  string
	UserID string
	K      int
	Rerank bool
}

// RetrieveResult is a single retrieved memory chunk.
type RetrieveResult struct {
	MemoryID  string
	ChunkID   string
	Content   string
	Score     float64
	Source    string
	CreatedAt string
}

// RetrieveStats contains timing and diagnostic information.
type RetrieveStats struct {
	VecMs         int64
	BM25Ms        int64
	FusionMs      int64
	RerankMs      int64
	TotalMs       int64
	RerankSkipped bool
	Degraded      bool
}

// RetrieveResponse is the full response from Retrieve.
type RetrieveResponse struct {
	Results []RetrieveResult
	Stats   RetrieveStats
}

// RetrieverConfig holds retrieval parameters.
type RetrieverConfig struct {
	VectorK     int
	BM25K       int
	RerankK     int
	FinalK      int
	RRFK        float64
	VectorFloor float32
}

// Retriever orchestrates hybrid retrieval.
type Retriever struct {
	meta     postgres.MetaStore
	vec      qdrant.VectorStore
	embedder embed.Embedder
	reranker rerank.Reranker
	graph    graph.GraphStore
	cfg      RetrieverConfig
}

// NewRetriever constructs a Retriever. reranker and graph may be nil.
func NewRetriever(
	meta postgres.MetaStore,
	vec qdrant.VectorStore,
	embedder embed.Embedder,
	reranker rerank.Reranker,
	graph graph.GraphStore,
	cfg RetrieverConfig,
) *Retriever {
	if cfg.VectorK <= 0 {
		cfg.VectorK = 20
	}
	if cfg.BM25K <= 0 {
		cfg.BM25K = 20
	}
	if cfg.RerankK <= 0 {
		cfg.RerankK = 20
	}
	if cfg.FinalK <= 0 {
		cfg.FinalK = 5
	}
	if cfg.RRFK <= 0 {
		cfg.RRFK = 60
	}
	if cfg.VectorFloor <= 0 {
		cfg.VectorFloor = 0.25
	}
	return &Retriever{meta: meta, vec: vec, embedder: embedder, reranker: reranker, graph: graph, cfg: cfg}
}

// Retrieve runs hybrid retrieval over the query.
func (r *Retriever) Retrieve(ctx context.Context, input RetrieveInput) (*RetrieveResponse, error) {
	start := time.Now()
	userID := input.UserID
	if userID == "" {
		userID = "default"
	}
	finalK := input.K
	if finalK <= 0 {
		finalK = r.cfg.FinalK
	}

	stats := RetrieveStats{}

	// Embed query (counted toward VecMs).
	embedStart := time.Now()
	vecs, err := r.embedder.Embed(ctx, []string{input.Query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("embed returned no vectors")
	}
	queryVec := vecs[0]
	embedDur := time.Since(embedStart)

	// Parallel vec + BM25.
	var (
		mu       sync.Mutex
		vecRes   []qdrant.SearchResult
		bm25Res  []postgres.BM25Result
		vecMs    int64
		bm25Ms   int64
		degraded bool
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		t := time.Now()
		res, verr := r.vec.Search(gctx, queryVec, uint64(r.cfg.VectorK), userID)
		dur := time.Since(t).Milliseconds()
		mu.Lock()
		defer mu.Unlock()
		vecMs = dur
		if verr != nil {
			degraded = true
			return nil
		}
		vecRes = res
		return nil
	})
	g.Go(func() error {
		t := time.Now()
		res, berr := r.meta.SearchBM25(gctx, userID, input.Query, r.cfg.BM25K)
		dur := time.Since(t).Milliseconds()
		mu.Lock()
		bm25Ms = dur
		if berr == nil {
			bm25Res = res
		}
		mu.Unlock()
		if berr != nil {
			return fmt.Errorf("bm25 search: %w", berr)
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	stats.VecMs = vecMs + embedDur.Milliseconds()
	stats.BM25Ms = bm25Ms
	stats.Degraded = degraded

	// Convert to fusion types.
	vecHits := make([]fusion.VecHit, 0, len(vecRes))
	for _, v := range vecRes {
		memID := ""
		if m, ok := v.Payload["memory_id"].(string); ok {
			memID = m
		}
		vecHits = append(vecHits, fusion.VecHit{
			ChunkID:  v.ID,
			MemoryID: memID,
			Score:    v.Score,
			Payload:  v.Payload,
		})
	}
	bm25Hits := make([]fusion.BM25Hit, 0, len(bm25Res))
	for _, b := range bm25Res {
		bm25Hits = append(bm25Hits, fusion.BM25Hit{
			ChunkID:  b.ChunkID,
			MemoryID: b.MemoryID,
			Content:  b.Content,
			Rank:     b.Rank,
		})
	}

	// Fuse.
	fStart := time.Now()
	fused := fusion.Fuse(fusion.Config{
		K:           r.cfg.RRFK,
		VectorFloor: r.cfg.VectorFloor,
		BM25K:       r.cfg.BM25K,
	}, vecHits, bm25Hits)
	stats.FusionMs = time.Since(fStart).Milliseconds()

	// Graph expansion (optional).
	if r.graph != nil && len(fused) > 0 {
		ids := make([]string, 0, len(fused))
		seen := make(map[string]struct{}, len(fused))
		for _, f := range fused {
			ids = append(ids, f.ChunkID)
			seen[f.ChunkID] = struct{}{}
		}
		// Use the user-scoped, capped variant.
		expanded, gerr := r.graph.ExpandRelatedWithOptions(ctx, ids, 1, graph.ExpandOptions{
			UserID:    input.UserID,
			MaxExpand: 10,
		})
		if gerr == nil {
			for _, id := range expanded {
				if _, ok := seen[id]; ok {
					continue
				}
				seen[id] = struct{}{}
				fused = append(fused, fusion.Result{ChunkID: id, Score: 0})
			}
		}
	}

	// Build candidates for rerank.
	candidates := make([]rerank.Candidate, 0, len(fused))
	for _, f := range fused {
		candidates = append(candidates, rerank.Candidate{
			ChunkID:  f.ChunkID,
			MemoryID: f.MemoryID,
			Content:  f.Content,
			Score:    f.Score,
		})
	}

	// Rerank skip rules.
	skipRerank := r.reranker == nil || !input.Rerank || len(candidates) <= finalK
	if skipRerank {
		stats.RerankSkipped = true
	} else {
		rIn := candidates
		if len(rIn) > r.cfg.RerankK {
			rIn = rIn[:r.cfg.RerankK]
		}
		rStart := time.Now()
		rOut, rerr := r.reranker.Rerank(ctx, input.Query, rIn)
		stats.RerankMs = time.Since(rStart).Milliseconds()
		if rerr != nil {
			stats.RerankSkipped = true
		} else {
			candidates = rOut
		}
	}

	// Trim and build results. Pull payload/content from fused entries by chunk ID.
	fusedByID := make(map[string]fusion.Result, len(fused))
	for _, f := range fused {
		fusedByID[f.ChunkID] = f
	}
	if len(candidates) > finalK {
		candidates = candidates[:finalK]
	}
	out := make([]RetrieveResult, 0, len(candidates))
	for _, c := range candidates {
		f := fusedByID[c.ChunkID]
		source := ""
		createdAt := ""
		if f.Payload != nil {
			if s, ok := f.Payload["source"].(string); ok {
				source = s
			}
			if ca, ok := f.Payload["created_at"].(string); ok {
				createdAt = ca
			}
		}
		content := c.Content
		if content == "" {
			content = f.Content
		}
		out = append(out, RetrieveResult{
			MemoryID:  c.MemoryID,
			ChunkID:   c.ChunkID,
			Content:   content,
			Score:     c.Score,
			Source:    source,
			CreatedAt: createdAt,
		})
	}

	stats.TotalMs = time.Since(start).Milliseconds()
	return &RetrieveResponse{Results: out, Stats: stats}, nil
}
