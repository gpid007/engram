# Engram Architecture

This document covers internals: data flows, the chunker and fusion math,
failure modes and degradation paths, and how to extend the system. For a
quickstart and API reference, see the top-level [`README.md`](../README.md).

## Component Overview

```
┌──────────────────────────────────────────────────────┐
│ Transports:  MCP (stdio)  +  HTTP (JSON over POST)   │
└──────────────┬───────────────────────────────────────┘
               │ same handlers
┌──────────────▼───────────────────────────────────────┐
│ Memory Service (internal/memory)                     │
│   Ingestor · Retriever · Reconciler                  │
└────┬──────────────┬──────────────────┬───────────────┘
     │              │                  │
   Qdrant       Postgres            Ollama
  (vectors)   (rows + tsvector)   (embed + LLM rerank)
                                     │
                            optional: cross-encoder ONNX
                            optional: remote rerank API
```

The service layer is a thin orchestrator. All persistence and AI calls go
through interfaces (`VectorStore`, `MetaStore`, `Embedder`, `Reranker`,
`Chunker`, `GraphStore`) so adapters are independently testable.

## Data Model

Every memory has one parent row and ≥1 chunks:

```
memories(id, user_id, source, content, metadata jsonb,
         importance, created_at, accessed_at, access_count)

chunks(id, memory_id, user_id, ord, content,
       tsv tsvector GENERATED, content_hash bytea, created_at)
   UNIQUE (user_id, content_hash)
   GIN INDEX ON tsv

pending_vectors(chunk_id PK, attempts, last_error, enqueued_at)
```

Qdrant holds one point per chunk: id = `chunk_id` (UUID), vector dim = embedder
dim (768 for `nomic-embed-text`), distance = cosine. Payload carries
`{memory_id, chunk_id, user_id, ord, source, importance, created_at}` so the
vector leg can return everything needed for fusion without a Postgres lookup.

## Ingest Data Flow

```
client (MCP/HTTP)
   │   {content, user_id?, source?, metadata?}
   ▼
Ingestor.Store
   │
   ├─► Chunker.Chunk(content)
   │     ├─ split into sentences (regex)
   │     ├─ embed all sentences in one batch  ── Ollama
   │     ├─ centroid-walk: start new chunk when
   │     │    cosine(running_centroid, sent) < 0.6
   │     │    OR token count > max_tokens (512)
   │     ├─ enforce min_tokens (100) by merging
   │     └─ chunk_emb = mean(sentence_embs)   (no second pass)
   │
   ├─► sha256 each chunk content → content_hash
   │
   ├─► tx BEGIN
   │     INSERT memories(...)
   │     INSERT chunks(...) ON CONFLICT (user_id, content_hash) DO NOTHING
   │     (returning surviving chunk ids and dedup count)
   │   tx COMMIT
   │
   ├─► VectorStore.Upsert([{chunk_id, vec, payload}, ...])
   │     │
   │     └── failure? ──► MetaStore.EnqueuePending(chunk_ids)
   │                       (Postgres remains consistent;
   │                        Reconciler will retry later)
   │
   └─► return {memory_id, chunks_stored, chunks_deduped, stored}
```

### Importance heuristic

`importance` defaults to `0.5`. The plan reserves it for future weighting; the
reranker does not currently use it. Consumers may override via metadata.

### Discard rules

- Empty/whitespace `content` → 400 `invalid_input`.
- All chunks dedup hit existing rows → response sets `chunks_stored: 0,
  chunks_deduped: N, stored: false`. The parent `memories` row is still
  inserted (cheap, allows source/metadata updates); de-dup is at the chunk
  layer where retrieval happens.

## Retrieve Data Flow

```
client (MCP/HTTP)
   │   {query, user_id?, k?, rerank?}
   ▼
Retriever.Retrieve
   │
   ├─► Embedder.Embed([query]) ── Ollama  ─── failure? hard error
   │
   ├─► errgroup
   │     ┌─ VectorStore.Search(qvec, vector_k=20, filter user_id)
   │     │     → []{chunk_id, score (cosine)}
   │     │     drop scores < vector_floor (0.25)
   │     │
   │     └─ MetaStore.SearchBM25(query, bm25_k=20, user_id)
   │           → []{chunk_id, ts_rank}
   │
   │   if vec leg fails  → degraded=true, continue with bm25 only
   │   if bm25 leg fails → hard fail (Postgres is canonical)
   │
   ├─► fusion.RRF(vec_hits, bm25_hits)
   │     score(d) = Σ_lists 1 / (rrf_k + rank_d)        // rrf_k=60
   │     keep d if d∈vec with cosine ≥ floor OR d∈bm25
   │     stable sort desc, dedupe by chunk_id
   │
   ├─► should_rerank?
   │     - rerank flag not set in request → skip
   │     - len(candidates) ≤ final_k       → skip
   │     - configured reranker is nil       → skip
   │     - else fetch chunk content via MetaStore for top rerank_k=20
   │       Reranker.Rerank(query, docs) with timeout
   │       on timeout / parse error → fused order, rerank_skipped=true
   │
   ├─► GraphStore.ExpandRelated(top_ids, depth=1)   // nop today
   │     adds related chunk_ids; merged maintaining current order
   │
   ├─► take final_k (5), enrich (memory_id, source, created_at)
   │
   └─► return {results[], stats{vec_ms, bm25_ms, fusion_ms,
                                 rerank_ms, total_ms,
                                 rerank_skipped, degraded}}
```

## Semantic Chunking

Goal: chunks that are topically coherent, bounded in size, and embedded once.

```
sentences = split(content, /(?<=[.!?])\s+/)
sent_embs = Embedder.Embed(sentences)           // ONE batched call

chunks    = []
current   = {sents: [], emb_sum: zero, tokens: 0}
centroid  = zero

for i, s in sentences:
    sim = cosine(centroid, sent_embs[i])  // 1.0 when current is empty
    boundary =
        current.tokens + tokens(s) > max_tokens (512)
        OR (current.tokens >= min_tokens (100) AND sim < 0.6)

    if boundary AND len(current.sents) > 0:
        chunks.append(finalize(current))
        current = empty

    current.sents.append(s)
    current.emb_sum += sent_embs[i]
    current.tokens += tokens(s)
    centroid = current.emb_sum / len(current.sents)

if current not empty:
    if current.tokens < min_tokens AND chunks not empty:
        merge into last chunk
    else:
        chunks.append(finalize(current))

# Each chunk's embedding is mean of its sentence embeddings — no second pass.
return chunks
```

Token counting is a whitespace+punctuation heuristic; it intentionally avoids
adding a tokenizer dependency. The chunker is deterministic for a given input
and embedder.

Edge cases handled:
- Empty / whitespace-only input → empty chunk slice (Ingestor rejects upstream).
- Single short sentence → one chunk, accepts being below `min_tokens`.
- All-punctuation input → split yields nothing → empty chunks.
- Long monologue with no boundaries < 0.6 → forced split at `max_tokens`.

## RRF Fusion

[Reciprocal Rank Fusion](https://plg.uwaterloo.ca/~gvcormac/cormacksigir09-rrf.pdf)
combines two ranked lists without score normalization.

```
score(d) = Σ_{L ∈ lists}  1 / (k + rank_L(d))

rank_L(d) is 1-based index of d in list L (∞ if absent ⇒ term is 0)
k = 60      // smooths small rank differences
```

In Engram:

```
score(d) =
    [d ∈ vec_hits AND cosine(d) ≥ vector_floor] · 1/(60 + vec_rank(d))
  + [d ∈ bm25_hits]                              · 1/(60 + bm25_rank(d))
```

The vector floor protects against noise: weak cosine matches (random tokens
trip into top-20) are excluded entirely from the fused score; they don't even
contribute their RRF term. BM25 has no analogous floor since BM25 scores are
not comparable across queries.

After fusion we have a single ranked list, deduped by `chunk_id`, sorted
stably so ties preserve vector-first preference.

## Failure Modes & Degradation

| Failure                          | Behavior |
|---|---|
| Ollama embed (ingest)            | 3× exponential backoff; if still failing, hard error to client (nothing persisted). Circuit breaker opens for 30s after 10 consecutive failures. |
| Ollama embed (retrieve query)    | Same retry; on final failure return `embedding_failed`. |
| Postgres unavailable             | Hard fail. Postgres is the canonical store — there is no read-only mode. Liveness vs readiness: `/healthz` still returns OK, `/readyz` does not. |
| Qdrant unavailable on ingest     | Postgres tx commits; chunk_ids enqueued in `pending_vectors`. The reconciler retries with exp backoff per row, capped attempts. Client sees `stored: true`. |
| Qdrant unavailable on retrieve   | `degraded: true` flag; results come from BM25 only. RRF reduces to a single-list rank. |
| Reranker timeout                 | Fused order returned; `rerank_skipped: true`. Timeout configurable via `rerank.timeout_ms`. |
| Reranker parse error (LLM)       | Same as timeout: fall back to fused order. We never trust unparseable LLM output. |
| Cross-encoder model file missing | Logged at startup as warning; `reranker = nil`; retrieval proceeds without rerank. |
| Qdrant dim mismatch on boot      | `EnsureCollection` returns an error and engram refuses to start. Drop the collection or change `embedding.model` to one matching the existing dim. |

The reconciler runs in `cmd/engram/main.go` as a background goroutine. It
polls `pending_vectors` on a fixed interval, retries Qdrant upserts, and
deletes the row on success. Failures bump `attempts` and store `last_error`
for inspection.

## Extension Points

Engram defines interfaces for the components most likely to evolve. New
implementations slot in via a factory in `cmd/engram/main.go`; the service
layer doesn't change.

### Custom reranker

Implement:

```go
type Reranker interface {
    Rerank(ctx context.Context, query string, docs []string) ([]float64, error)
}
```

Add a case to the switch on `cfg.Rerank.Provider` in `cmd/engram/main.go`.
The retriever already handles timeout and fallback; new rerankers don't need
to re-implement those.

### Remote embedder

The current `Embedder` interface is provider-neutral:

```go
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
    Dim() int
}
```

Drop in an OpenAI/Voyage/Cohere client; ensure `Dim()` matches whatever the
running Qdrant collection was created with, or rebuild the collection.

### GraphStore (Neo4j hook)

```go
type GraphStore interface {
    ExpandRelated(ctx context.Context, chunkIDs []string, depth int) ([]string, error)
}
```

Today there is only `NopStore{}` returning an empty slice. A Neo4j or DGraph
adapter writing relationships at ingest time and traversing on retrieve plugs
in here without changing the Retriever. Edge writes would happen as a
post-step in `Ingestor.Store`; the interface only covers the read path so
the write side is implementation-defined.

### Query rewriting / expansion

There's no hook today. The natural seam is in `Retriever.Retrieve` before
the `Embedder.Embed` call: rewrite `query` (LLM call), keep both original
and rewritten, embed both, union results. Consider adding an optional
`QueryRewriter` interface if you go this route.

### Memory decay / summarization

Not implemented. The data model carries `accessed_at` and `access_count` for
this purpose. A periodic job in `cmd/engram` similar to the reconciler can:
- decay `importance` based on `now() - accessed_at`,
- summarize a user's oldest chunks below a threshold via the LLM and
  replace them with the summary.

Both are pure Postgres + Ollama operations; no service-layer changes needed.

## Performance Targets

From the build plan, on dev hardware:

- Ingest: < 200ms for a single short memory (dominated by embedding round-trip).
- Retrieve, no rerank: p95 < 150ms.
- Retrieve, cross-encoder rerank: p95 < 250ms.
- Retrieve, LLM-1b rerank: p95 < 1.2s.

These are upheld by the eval harness in `test/eval` and the load test in
`test/load`.

## What's Intentionally Not Here

- Authentication / authorization. `user_id` is a soft namespace.
- Real Neo4j integration. Interface only.
- Distributed deployment. Single-node compose.
- Multi-tenant enforcement beyond `user_id` filtering.

These are listed in `PLAN.md §9` as explicit non-goals for this iteration.
