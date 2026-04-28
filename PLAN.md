# Engram — Build Plan

A portable, MCP-native memory harness in Go. Hybrid retrieval (Qdrant vectors + Postgres BM25), local embeddings + reranking via Ollama, with swappable rerankers for remote/cloud later. Runs via Docker Compose. Designed to be implemented in ~2500 LOC by a small swarm of parallel agents.

This document is the single source of truth for execution. Each Work Package (WP) is self-contained, has explicit inputs/outputs, and a recommended agent. Agents pick a WP, complete it against its acceptance criteria, and hand off via the artifacts listed.

---

## 0. Architecture Snapshot

```
┌──────────────────────────────────────────────────────┐
│ Transports:  MCP (stdio)  +  HTTP (JSON over POST)   │
└──────────────┬───────────────────────────────────────┘
               │ same tool handlers, two adapters
┌──────────────▼───────────────────────────────────────┐
│ Memory Service (internal/memory)                     │
│  Ingestor · Retriever · Admin                        │
│  Ports: VectorStore, MetaStore, Embedder,            │
│         Reranker, Chunker, GraphStore (stub)         │
└────┬──────────────┬──────────────────┬───────────────┘
     │              │                  │
   Qdrant       Postgres            Ollama
   (vectors)    (rows + tsvector)   (embed + rerank fallback)
                                    │
                            optional: cross-encoder
                            (ONNX bge-reranker)
                            optional: remote API rerank
```

Layout:

```
cmd/engram/main.go
internal/config/
internal/mcp/                # stdio transport, tool handlers
internal/httpapi/            # HTTP transport, same handlers
internal/memory/             # Ingestor, Retriever, ports
internal/embed/ollama.go
internal/rerank/llm.go
internal/rerank/crossenc.go  # ONNX, optional build tag
internal/rerank/remote.go    # API-based, future-friendly
internal/store/qdrant/
internal/store/postgres/
internal/store/postgres/migrations/*.sql
internal/chunk/semantic.go
internal/fusion/rrf.go
internal/graph/              # interface + nop impl now (Neo4j later)
deploy/docker-compose.yml
deploy/Dockerfile
test/integration/
test/eval/                   # tiny recall@k harness
```

LOC budget: ~2500. Hard ceiling: 4000.

---

## 1. Decisions Locked

| Area | Decision |
|---|---|
| Language | Go 1.23.0 (minimum; pin exact version in `go.mod` toolchain directive) |
| MCP SDK | `github.com/mark3labs/mcp-go` |
| Postgres driver | `jackc/pgx/v5` |
| Qdrant client | `github.com/qdrant/go-client` |
| HTTP | stdlib `net/http` + `chi` (or stdlib only — agent's call) |
| Migrations | plain `.sql` files in `internal/store/postgres/migrations/`, applied on boot |
| Config | YAML + env override; no viper |
| Chunking | Semantic (sentence-level cosine boundary), max 512 tokens, min 100, no overlap |
| Memory unit | Chunk-level rows + parent `memories` row |
| Dedup | `(user_id, sha256(content))` unique on chunks |
| Fusion | Reciprocal Rank Fusion (k=60) + vector cosine floor 0.25 |
| Reranker default | Cross-encoder (`bge-reranker-base`, ONNX, CPU) |
| Reranker fallback | LLM via Ollama (`llama3.2:1b`); also remote-API impl as third option |
| Multi-user | `user_id` field everywhere; no auth enforcement yet |
| Neo4j | `GraphStore` interface + nop impl; no Neo4j code |
| Transports | MCP stdio + HTTP, both ship in v1 |

---

## 2. Data Model

```sql
CREATE TABLE memories (
  id           UUID PRIMARY KEY,
  user_id      TEXT NOT NULL DEFAULT 'default',
  source       TEXT,
  content      TEXT NOT NULL,
  metadata     JSONB NOT NULL DEFAULT '{}',
  importance   REAL NOT NULL DEFAULT 0.5,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  accessed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  access_count INT NOT NULL DEFAULT 0
);

CREATE TABLE chunks (
  id           UUID PRIMARY KEY,
  memory_id    UUID NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
  user_id      TEXT NOT NULL,
  ord          INT NOT NULL,
  content      TEXT NOT NULL,
  tsv          tsvector GENERATED ALWAYS AS (to_tsvector('english', content)) STORED,
  content_hash BYTEA NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX chunks_tsv_gin   ON chunks USING GIN(tsv);
CREATE INDEX chunks_user      ON chunks(user_id);
CREATE UNIQUE INDEX chunks_user_hash ON chunks(user_id, content_hash);

CREATE TABLE pending_vectors (
  chunk_id   UUID PRIMARY KEY REFERENCES chunks(id) ON DELETE CASCADE,
  attempts   INT NOT NULL DEFAULT 0,
  last_error TEXT,
  enqueued_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Qdrant collection `memories`: vector dim = embedder dim (768 for nomic-embed-text), distance Cosine, payload `{memory_id, chunk_id, user_id, ord, source, importance, created_at}`. Point id = `chunk_id`.

---

## 3. Configuration (YAML)

```yaml
server:
  mcp_stdio: true
  http_addr: ":8080"

embedding:
  provider: ollama
  base_url: http://ollama:11434
  model: nomic-embed-text
  dim: 768
  batch: 32
  timeout_ms: 5000
  retries: 3

vector:
  provider: qdrant
  addr: qdrant:6334
  collection: memories

meta:
  provider: postgres
  dsn: postgres://engram:engram@postgres:5432/engram?sslmode=disable

retrieval:
  vector_k: 20
  bm25_k: 20
  rerank_k: 20
  final_k: 5
  rrf_k: 60
  vector_floor: 0.25

rerank:
  enabled: true
  provider: crossenc          # crossenc | llm | remote | none
  crossenc_model_path: /models/bge-reranker-base.onnx
  llm_model: llama3.2:1b
  remote:
    base_url: ""
    api_key_env: ""
    model: ""
  timeout_ms: 1500
  max_candidates: 20

chunking:
  max_tokens: 512
  min_tokens: 100
  similarity_threshold: 0.6

logging:
  level: info
  format: json
```

Env overrides: `ENGRAM_<SECTION>_<KEY>` (uppercase, underscores).

---

## 3a. Service Ports

| Service  | Internal (compose network) | Host-exposed |
|----------|---------------------------|--------------|
| engram HTTP | `:8080` | `8080` |
| Postgres | `5432` | not exposed |
| Qdrant gRPC | `6334` | not exposed |
| Qdrant HTTP | `6333` | `6333` (dashboard) |
| Ollama | `11434` | `11434` |

engram MCP stdio: no port; clients exec the binary or `docker exec -i engram engram --mcp`.

---

## 3b. Ollama Models (pull on first boot)

| Model | Purpose | Approx size |
|---|---|---|
| `nomic-embed-text` | Embeddings (ingest + query) | ~274MB |
| `llama3.2:1b` | LLM reranker fallback | ~1.3GB |

Cross-encoder (`bge-reranker-base.onnx`) is downloaded separately via `scripts/download-models.sh` — not an Ollama model. See WP-08.

`deploy/init/pull-models.sh` must pull both Ollama models above and block until Ollama is ready.

---

## 4. MCP / HTTP Tool Contract

Both transports expose the same three operations with identical schemas.

### `store_memory`
```
in:  { content, user_id?, source?, metadata? }
out: { memory_id, chunks_stored, chunks_deduped, stored }
err: invalid_input | embedding_failed | storage_failed
```

### `retrieve_context`
```
in:  { query, user_id?, k?, rerank? }
out: {
  results: [{ memory_id, chunk_id, content, score, source, created_at }],
  stats:   { vec_ms, bm25_ms, fusion_ms, rerank_ms, total_ms,
             rerank_skipped, degraded }
}
err: invalid_input | embedding_failed | retrieval_failed
```

### `get_user_state`
```
in:  { user_id? }
out: { memory_count, chunk_count, first_memory, last_memory, top_sources[] }
```

Errors uniform: `{ error: { code, message, retryable } }`.

HTTP routes mirror tools: `POST /v1/memories`, `POST /v1/retrieve`, `GET /v1/users/{id}/state`.

---

## 5. Algorithms

**Semantic chunking:** split into sentences (regex), embed all sentences in one batch, walk and start new chunk when `cosine(running_centroid, sentence) < 0.6` OR token count > 512. Min chunk 100 tokens. Chunk embedding = mean of its sentence embeddings (avoids second pass).

**Fusion (RRF):**
```
score(d) = Σ_lists 1 / (60 + rank_d_in_list)
keep d only if d ∈ vec_hits with cosine ≥ 0.25, OR d ∈ bm25_hits with rank ≤ bm25_k.
```

**Reranking:** if `len(candidates) ≤ final_k` → skip. If top fused score gap to second is large → skip. Otherwise call configured reranker, sort by reranker score, take `final_k`. Timeout returns fused order + `rerank_skipped: true`.

**Failure handling:** Postgres failure = hard fail; Qdrant failure on ingest enqueues `pending_vectors`; vector-leg failure on retrieve → BM25-only with `degraded: true`; embedder retries 3x exp backoff.

---

## 6. Work Packages

Each WP lists: scope, dependencies (which WPs must be done first), inputs, outputs, acceptance criteria, recommended agent. Multiple WPs can run in parallel when their dependencies are met.

### Agent assignment heuristic
- **ac-opus (Claude Opus 4.7)** — large/architectural code, retrieval & fusion logic, reranker design, correctness-critical work (semantic chunker, RRF, integration test design).
- **ac-sonnet (Claude Sonnet)** — Go infra glue, MCP/HTTP layer, error handling, tests.
- **gg-25flash / lo-coder (local Qwen Coder)** — boilerplate, scaffolding, migrations, Dockerfile, simple CRUD.
- **lo-qwen35b (local 35B)** — review passes, deterministic refactors.

No Groq agents anywhere in this plan.

---

### WP-01 — Repo skeleton & module layout
- **Depends on:** none
- **Agent:** lo-coder
- **Scope:** `go.mod`, directory tree per §0, empty package files with package docs, `.gitignore`, basic `Makefile` (build, test, lint, up, down).
- **Outputs:** committable skeleton, `go build ./...` passes.
- **Acceptance:** `go vet ./...` clean; tree matches §0 exactly.

### WP-02 — Config loader
- **Depends on:** WP-01
- **Agent:** ac-sonnet
- **Scope:** YAML loader to typed struct (§3), env override, validation, sensible defaults, unit tests.
- **Outputs:** `internal/config/config.go`, `config_test.go`, sample `engram.yaml`.
- **Acceptance:** loads sample file, env override works, validation rejects bad values, 100% line coverage on validators.

### WP-03 — Postgres migrations + MetaStore
- **Depends on:** WP-01
- **Agent:** gg-25flash
- **Scope:** SQL files (§2), migration runner (sorted filename apply, version table), `MetaStore` interface and pgx impl: `SaveMemory`, `SaveChunks`, `SearchBM25`, `GetUserState`, `EnqueuePending`, `DrainPending`.
- **Outputs:** `internal/store/postgres/*.go`, `migrations/0001_init.sql`, tests using testcontainers.
- **Acceptance:** migrations apply idempotently; BM25 query returns ranked chunks; dedup conflict returns `ErrDuplicate` not panic.

### WP-04 — Qdrant VectorStore
- **Depends on:** WP-01
- **Agent:** gg-25flash
- **Scope:** `VectorStore` interface and Qdrant impl: `EnsureCollection(dim)`, `Upsert([]Point)`, `Search(vec, k, filter)`. Filter on `user_id`. Cosine distance.
- **Outputs:** `internal/store/qdrant/*.go`, integration test against ephemeral Qdrant container.
- **Acceptance:** `EnsureCollection` is idempotent; search returns chunk_ids with scores; user_id filter enforced.

### WP-05 — Ollama Embedder
- **Depends on:** WP-01, WP-02
- **Agent:** ac-sonnet
- **Scope:** `Embedder` interface, Ollama HTTP client, batching, retry/backoff, circuit breaker (10 consecutive failures → open 30s), unit tests with httptest.
- **Outputs:** `internal/embed/ollama.go`, tests.
- **Acceptance:** batches respect config, 3 retries with exp backoff observed in test, circuit breaker opens/closes correctly.

### WP-06 — Semantic Chunker
- **Depends on:** WP-01, WP-05
- **Agent:** ac-opus
- **Scope:** sentence splitter, batched sentence embedding, centroid-walk boundary detection, max/min token enforcement, mean-pooled chunk embedding output. Tokenization via simple whitespace + punct heuristic (no external tokenizer).
- **Outputs:** `internal/chunk/semantic.go`, table-driven tests covering: short input, long monologue, mixed-topic input, edge cases (empty, single sentence, all punctuation).
- **Acceptance:** outputs `[]Chunk{Content, EmbVec}`; respects bounds; deterministic for same input.

### WP-07 — Fusion (RRF)
- **Depends on:** WP-01
- **Agent:** ac-opus
- **Scope:** RRF implementation, vector cosine floor filter, deduplication by chunk_id, stable sort.
- **Outputs:** `internal/fusion/rrf.go`, exhaustive tests including: disjoint lists, fully overlapping, empty lists, score-floor exclusion.
- **Acceptance:** correctness against hand-computed expected outputs; ≤30 LOC of logic.

### WP-08 — Reranker: Cross-encoder (default)
- **Depends on:** WP-01, WP-02
- **Agent:** gg-25pro
- **Scope:** `Reranker` interface; ONNX runtime integration (`github.com/yalue/onnxruntime_go` or equivalent), `bge-reranker-base` model loader, batch scoring, build tag `crossenc` so default build doesn't require ONNX libs at compile time. Document model download script.
- **Outputs:** `internal/rerank/crossenc.go`, `scripts/download-models.sh`, tests with a tiny stub model OR a recorded-fixture mode.
- **Acceptance:** scores 20 docs in <120ms on CPU on dev machine; deterministic ordering; gracefully reports missing model file.

### WP-09 — Reranker: LLM (fallback)
- **Depends on:** WP-01, WP-02, WP-05
- **Agent:** ac-sonnet
- **Scope:** Prompt-based rerank against Ollama small instruct model. Single-call batch prompt with numbered docs, parse 0-10 score per doc, robust against malformed output (fallback to fused order with warning).
- **Outputs:** `internal/rerank/llm.go`, tests (mocked Ollama).
- **Acceptance:** parses well-formed responses; never panics on malformed; respects timeout.

### WP-10 — Reranker: Remote API
- **Depends on:** WP-01, WP-02
- **Agent:** lo-coder
- **Scope:** generic remote rerank client (Cohere-style POST: `{query, documents}` → `{results: [{index, score}]}`). Auth via env var. Disabled when base_url empty.
- **Outputs:** `internal/rerank/remote.go`, tests (httptest).
- **Acceptance:** pluggable, tested, errors mapped to retryable/non-retryable correctly.

### WP-11 — GraphStore stub (Neo4j hook)
- **Depends on:** WP-01
- **Agent:** lo-coder
- **Scope:** `GraphStore` interface with `ExpandRelated(ctx, chunkIDs, depth) ([]ChunkID, error)`, nop implementation that returns empty slice, wired into Retriever as optional post-step (no-op when nil).
- **Outputs:** `internal/graph/graph.go`, `internal/graph/nop.go`.
- **Acceptance:** Retriever compiles with `nil` graph store and with nop impl; interface stable enough that adding Neo4j later requires zero changes to Retriever.

### WP-12 — Memory Service: Ingestor
- **Depends on:** WP-03, WP-04, WP-05, WP-06
- **Agent:** gg-25pro
- **Scope:** `Ingestor.Store(ctx, input) (Result, error)` orchestrating: normalize → chunk → embed (reuse chunker mean-pool) → tx insert memories+chunks → Qdrant upsert → on Qdrant failure enqueue pending. Importance heuristic per §1. Discard rules per design.
- **Outputs:** `internal/memory/ingestor.go`, unit tests with mocked ports + integration test against real services.
- **Acceptance:** dedup returns `chunks_deduped` correctly; Qdrant failure leaves consistent Postgres state and pending_vectors row; <100 LOC.

### WP-13 — Memory Service: Retriever
- **Depends on:** WP-04, WP-05, WP-07, WP-08 (or WP-09 fallback), WP-11
- **Agent:** gg-25pro
- **Scope:** `Retriever.Retrieve(ctx, q) (Results, error)`: parallel vec+bm25 via errgroup, RRF, optional rerank with skip rules, optional graph expansion (nop today), timing stats, partial-failure degradation.
- **Outputs:** `internal/memory/retriever.go`, integration test covering: happy path, vec-leg fail, rerank timeout, empty results.
- **Acceptance:** all stat fields populated; degraded path verified; <150 LOC.

### WP-14 — Pending vectors reconciler
- **Depends on:** WP-03, WP-04
- **Agent:** lo-coder
- **Scope:** background goroutine in `cmd/engram` polling `pending_vectors` every N seconds, retrying upsert with exp backoff per row, capped attempts.
- **Outputs:** `internal/memory/reconciler.go`.
- **Acceptance:** unit test with fake stores verifies retry & success removes the row.

### WP-15 — MCP transport
- **Depends on:** WP-12, WP-13
- **Agent:** ac-sonnet
- **Scope:** mcp-go server, register three tools per §4, JSON in/out, structured errors. Stdio only.
- **Outputs:** `internal/mcp/server.go`, `internal/mcp/handlers.go`.
- **Acceptance:** manual test from OpenCode connects and roundtrips all three tools.

### WP-16 — HTTP transport
- **Depends on:** WP-12, WP-13
- **Agent:** ac-sonnet
- **Scope:** stdlib `net/http` server, three routes per §4, same handlers internally, request logging, recover middleware, `/healthz`, `/readyz`.
- **Outputs:** `internal/httpapi/server.go`.
- **Acceptance:** integration test hits all routes; readiness reflects DB+Qdrant+Ollama reachability.

### WP-17 — Wiring & main
- **Depends on:** WP-02, WP-15, WP-16, WP-14
- **Agent:** ac-sonnet
- **Scope:** `cmd/engram/main.go` constructs config → stores → embedder → reranker (factory by config) → service → both transports → reconciler. Graceful shutdown.
- **Outputs:** `cmd/engram/main.go`.
- **Acceptance:** `engram --config engram.yaml` starts both transports; SIGTERM exits cleanly within 5s.

### WP-18 — Docker & Compose
- **Depends on:** WP-17
- **Agent:** lo-coder
- **Scope:** Multi-stage Dockerfile (build + slim runtime, optional `crossenc` build tag image variant), `docker-compose.yml` with services: `postgres`, `qdrant`, `ollama`, `engram`. Healthchecks, named volumes, ollama model pull init container or one-shot job. Network exposes engram HTTP on host port; MCP runs in container too (stdio attach for local dev documented).
- **Outputs:** `deploy/Dockerfile`, `deploy/docker-compose.yml`, `deploy/init/pull-models.sh`.
- **Acceptance:** `docker compose up` from clean state, all services healthy, smoke test (curl `/healthz`, then store + retrieve) succeeds end-to-end.

### WP-19 — Integration test suite
- **Depends on:** WP-17
- **Agent:** ac-opus
- **Scope:** testcontainers-based suite running real Postgres + Qdrant + a fake Ollama (httptest). Cases: ingest → retrieve roundtrip, dedup, partial failures, reranker on/off/timeout, both transports.
- **Outputs:** `test/integration/*.go`.
- **Acceptance:** `go test ./test/integration/...` passes locally and in CI.

### WP-20 — Eval harness (recall@k)
- **Depends on:** WP-17
- **Agent:** gg-25flash
- **Scope:** small fixture corpus (~30 docs), 15 queries with expected memory_ids, runner that prints recall@5 and latency p50/p95. Used to sanity-check retrieval changes.
- **Outputs:** `test/eval/corpus.json`, `test/eval/run.go`.
- **Acceptance:** baseline recall@5 ≥ 0.85 on fixture set; run completes in <30s.

### WP-21 — Load test
- **Depends on:** WP-17
- **Agent:** lo-coder
- **Scope:** `k6` or Go-native load script, 100 concurrent retrieves, asserts p95 latency targets (no rerank: <150ms; rerank crossenc: <250ms; rerank LLM-1b: <1.2s).
- **Outputs:** `test/load/retrieve.go` or `test/load/retrieve.js`.
- **Acceptance:** scripted, results emitted to stdout; documented thresholds.

### WP-22 — Observability minimums
- **Depends on:** WP-17
- **Agent:** ac-sonnet
- **Scope:** structured logs (slog) with request ids, basic Prometheus `/metrics` endpoint exposing: ingest_duration_ms, retrieve_duration_ms (with rerank label), embedder_failures_total, pending_vectors_gauge.
- **Outputs:** `internal/httpapi/metrics.go`.
- **Acceptance:** `curl /metrics` shows the four metrics with non-zero values after a synthetic run.

### WP-23 — Documentation
- **Depends on:** WP-17, WP-18
- **Agent:** ac-opus (Claude Opus 4.7)
- **Scope:** rewrite `README.md` (replace current spec) with: quickstart (compose up), MCP integration snippet for OpenCode, HTTP API examples, config reference, architecture diagram (ASCII), troubleshooting. Keep under 400 lines.
- **Outputs:** `README.md` (replaced), `docs/architecture.md` (deeper detail).
- **Acceptance:** quickstart works on a clean machine following README only.

### WP-24 — CI pipeline
- **Depends on:** WP-01
- **Agent:** lo-coder
- **Scope:** GitHub Actions workflow: on push/PR run `go build ./...`, `go vet ./...`, `go test ./...` (unit only, no containers). Second job runs integration tests (`go test ./test/integration/...`) using `services:` containers for Postgres and Qdrant, mocked Ollama via httptest stub. Cache Go modules.
- **Outputs:** `.github/workflows/ci.yml`.
- **Acceptance:** CI passes on a clean push; integration job skipped on `[skip-integration]` in commit message.

---

## 7. Execution Waves (parallelism map)

```
Wave 1 (parallel):   WP-01
Wave 2 (parallel):   WP-02, WP-03, WP-04, WP-11
Wave 3 (parallel):   WP-05, WP-07, WP-10
Wave 4 (parallel):   WP-06, WP-08, WP-09
Wave 5 (parallel):   WP-12, WP-14
Wave 6 (parallel):   WP-13
Wave 7 (parallel):   WP-15, WP-16, WP-22
Wave 8 (parallel):   WP-17
Wave 9 (parallel):   WP-18, WP-19, WP-20, WP-21, WP-23, WP-24
```

Critical path: 01 → 03/04/05 → 06 → 12 → 13 → 17 → 18.

---

## 8. Acceptance Gates (whole system)

Before declaring done:

1. `docker compose up` from clean repo brings up healthy services.
2. MCP roundtrip from OpenCode: store 3 memories, retrieve, results contain expected items.
3. HTTP roundtrip: same scenario via curl.
4. Integration suite green.
5. Eval harness recall@5 ≥ 0.85, p95 retrieve <250ms with crossenc rerank on dev hardware.
6. Total LOC (excluding generated, vendored, tests) ≤ 4000.
7. Zero LangChain/Haystack/RAGFlow/agent-framework deps. `go list -m all` reviewed.
8. README quickstart followed verbatim by a fresh agent works.

---

## 9. Non-goals (this iteration)

- Authentication / authorization.
- Real Neo4j integration (interface only).
- Memory decay job, summarization triggers, query expansion (extension hooks documented; no code).
- Multi-tenant enforcement beyond `user_id` filtering.
- Distributed deployment, sharding, replication.

---

## 10. Risk Register

| Risk | Mitigation |
|---|---|
| ONNX runtime install pain | Build tag isolates; LLM reranker is fallback; doc both paths |
| Ollama model pulls slow first run | init container pre-pulls; healthcheck waits |
| Qdrant collection dim mismatch on model swap | `EnsureCollection` checks dim; clear error and refuse start |
| Postgres tsvector regex for sentence split too crude | Tests cover edge cases; chunker is the only place this matters |
| RRF with floor drops good results when both lists weak | Eval harness catches regressions |
| LOC ceiling pressure | Cut WP-22 metrics surface, then WP-21 load test, before WP-19 integration |

---

## 11. Handoff Protocol Between Agents

Each WP completion must include:
- Code committed in a branch `wp-NN-shortname`.
- `go build ./... && go vet ./... && go test ./...` clean for that package.
- Brief PR description: scope addressed, deviations from this plan (if any), follow-ups created as new WPs if needed.
- No edits to other WPs' files unless dependency requires it; if so, note in PR.

If an agent encounters an ambiguity, it stops and posts a clarifying question rather than guessing.
