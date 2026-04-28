# Engram

A portable, MCP-native memory harness in Go. Engram gives LLM agents a durable
hybrid-retrieval memory: Qdrant for dense vectors, Postgres for BM25 + metadata,
Ollama for local embeddings, and a swappable reranker (cross-encoder, LLM, or
remote API). Runs end-to-end via Docker Compose. Same handlers serve both an
MCP stdio transport and an HTTP+JSON API.

## Quickstart

```bash
git clone https://github.com/gregdhill/engram.git
cd engram
docker compose -f deploy/docker-compose.yml up
```

First boot pulls the Ollama models (`nomic-embed-text`, `llama3.2:1b`) and
takes ~2 minutes. The `init-models` service exits successfully once both are
cached; `engram` waits on it. After everything reports healthy:

```bash
# Store a memory
curl -s -X POST http://localhost:8080/v1/memories \
  -H "Content-Type: application/json" \
  -d '{"content":"The mitochondria is the powerhouse of the cell.","source":"biology"}' | jq .

# Retrieve context
curl -s -X POST http://localhost:8080/v1/retrieve \
  -H "Content-Type: application/json" \
  -d '{"query":"cell energy","k":3}' | jq .
```

## MCP Integration

Engram exposes three MCP tools: `store_memory`, `retrieve_context`,
`get_user_state`. Connect via stdio against the running container.

### OpenCode (`~/.config/opencode/config.json`)

```json
{
  "mcp": {
    "engram": {
      "type": "local",
      "command": ["docker", "exec", "-i", "engram-engram-1", "/engram", "--mcp"]
    }
  }
}
```

### Claude Desktop (`claude_desktop_config.json`)

```json
{
  "mcpServers": {
    "engram": {
      "command": "docker",
      "args": ["exec", "-i", "engram-engram-1", "/engram", "--mcp"]
    }
  }
}
```

The container name `engram-engram-1` matches `docker compose ps`. For a binary
install, replace the command with the path to `engram --mcp`.

## HTTP API

All routes accept and return JSON. Errors use a uniform envelope:
`{"error":{"code":"...","message":"...","retryable":bool}}`.

### `POST /v1/memories` — ingest content

```bash
curl -s -X POST http://localhost:8080/v1/memories \
  -H "Content-Type: application/json" \
  -d '{
    "content": "Postgres uses MVCC for concurrency control.",
    "user_id": "alice",
    "source": "notes",
    "metadata": {"tag": "db"}
  }' | jq .
# => {"memory_id":"...","chunks_stored":1,"chunks_deduped":0,"stored":true}
```

### `POST /v1/retrieve` — hybrid retrieval

```bash
curl -s -X POST http://localhost:8080/v1/retrieve \
  -H "Content-Type: application/json" \
  -d '{"query":"how does postgres handle concurrency","user_id":"alice","k":5,"rerank":true}' | jq .
# => {"results":[{"memory_id":"...","chunk_id":"...","content":"...","score":0.91,...}],
#     "stats":{"vec_ms":12,"bm25_ms":4,"fusion_ms":1,"rerank_ms":87,"total_ms":104,
#              "rerank_skipped":false,"degraded":false}}
```

### `GET /v1/users/{id}/state` — aggregate stats

```bash
curl -s http://localhost:8080/v1/users/alice/state | jq .
# => {"memory_count":42,"chunk_count":68,"first_memory":"...","last_memory":"...",
#     "top_sources":["notes","slack"]}
```

### `GET /healthz` / `GET /readyz` / `GET /metrics`

`/healthz` is a liveness ping. `/readyz` checks vector and embedder
reachability. `/metrics` exposes Prometheus counters/histograms:
`engram_ingest_duration_ms`, `engram_retrieve_duration_ms` (label `rerank`),
`engram_embedder_failures_total`, `engram_pending_vectors`.

## Configuration

Engram loads `engram.yaml` from the working directory by default, or from
`--config <path>`. Any field can be overridden via env using
`ENGRAM_<SECTION>_<KEY>` (uppercase, underscores).

| Section.field                 | Default                                                | Notes |
|---|---|---|
| `server.mcp_stdio`            | `true`                                                 | Run MCP alongside HTTP. |
| `server.http_addr`            | `:8080`                                                | HTTP listen address. |
| `embedding.provider`          | `ollama`                                               | Only ollama in v1. |
| `embedding.base_url`          | `http://ollama:11434`                                  | Ollama endpoint. |
| `embedding.model`             | `nomic-embed-text`                                     | Must match `embedding.dim`. |
| `embedding.dim`               | `768`                                                  | Vector dimension. |
| `embedding.batch`             | `32`                                                   | Embed batch size. |
| `embedding.timeout_ms`        | `5000`                                                 | Per-request timeout. |
| `embedding.retries`           | `3`                                                    | Exp-backoff retries. |
| `vector.provider`             | `qdrant`                                               | |
| `vector.addr`                 | `qdrant:6334`                                          | gRPC address. |
| `vector.collection`           | `memories`                                             | Auto-created on boot. |
| `meta.dsn`                    | `postgres://engram:engram@postgres:5432/engram?sslmode=disable` | |
| `retrieval.vector_k`          | `20`                                                   | Top-K from vector leg. |
| `retrieval.bm25_k`            | `20`                                                   | Top-K from BM25 leg. |
| `retrieval.rerank_k`          | `20`                                                   | Max candidates into reranker. |
| `retrieval.final_k`           | `5`                                                    | Returned to caller. |
| `retrieval.rrf_k`             | `60`                                                   | RRF constant. |
| `retrieval.vector_floor`      | `0.25`                                                 | Min cosine to keep a vector hit. |
| `rerank.enabled`              | `true`                                                 | Master switch. |
| `rerank.provider`             | `crossenc`                                             | `crossenc` \| `llm` \| `remote` \| `none`. |
| `rerank.crossenc_model_path`  | `/models/bge-reranker-base.onnx`                       | Required for `crossenc`. |
| `rerank.llm_model`            | `llama3.2:1b`                                          | Used by `llm` provider. |
| `rerank.remote.base_url`      | `""`                                                   | Required for `remote`. |
| `rerank.remote.api_key_env`   | `""`                                                   | Env var name to read API key from. |
| `rerank.remote.model`         | `""`                                                   | Provider-specific model id. |
| `rerank.timeout_ms`           | `1500`                                                 | If exceeded, fused order is returned. |
| `rerank.max_candidates`       | `20`                                                   | Hard cap into reranker. |
| `chunking.max_tokens`         | `512`                                                  | Chunk ceiling. |
| `chunking.min_tokens`         | `100`                                                  | Avoid micro-chunks. |
| `chunking.similarity_threshold`| `0.6`                                                 | Cosine threshold for new boundary. |
| `logging.level`               | `info`                                                 | `debug` \| `info` \| `warn` \| `error`. |
| `logging.format`              | `json`                                                 | `json` \| `text`. |

See `engram.yaml` for the full sample.

## Architecture

```
┌──────────────────────────────────────────────────────┐
│ Transports:  MCP (stdio)  +  HTTP (JSON over POST)   │
└──────────────┬───────────────────────────────────────┘
               │ same tool handlers, two adapters
┌──────────────▼───────────────────────────────────────┐
│ Memory Service (internal/memory)                     │
│   Ingestor · Retriever · Reconciler                  │
│   Ports: VectorStore, MetaStore, Embedder,           │
│          Reranker, Chunker, GraphStore (stub)        │
└────┬──────────────┬──────────────────┬───────────────┘
     │              │                  │
   Qdrant       Postgres            Ollama
  (vectors)   (rows + tsvector)   (embed + LLM rerank)
                                     │
                            optional: cross-encoder
                            (ONNX bge-reranker-base)
                            optional: remote rerank API
```

Ingest path: `normalize → semantic chunk → embed (mean-pool sentences) →
tx insert memories+chunks → Qdrant upsert → on Qdrant failure enqueue
pending_vectors`.

Retrieve path: `embed query → parallel{Qdrant search, Postgres BM25} → RRF
fuse with vector cosine floor → optional rerank → optional graph expand (nop)
→ final_k`.

Deeper detail in [`docs/architecture.md`](docs/architecture.md).

## Reranker Modes

| Provider   | Model                       | Latency (20 docs) | When to use |
|---|---|---|---|
| `crossenc` | `bge-reranker-base` (ONNX)  | ~80–120ms CPU     | Default. Best quality/latency. Requires the model file (`scripts/download-models.sh`). |
| `llm`      | Ollama small instruct (`llama3.2:1b`) | ~600–1200ms | Fallback when ONNX runtime is unavailable. Uses Ollama you already have. |
| `remote`   | Cohere/Voyage/etc.          | network-bound     | When you want hosted quality and don't mind the dependency. Auth via `rerank.remote.api_key_env`. |
| `none`     | —                           | 0                 | Skip reranking entirely; return RRF order. |

If the configured reranker fails to initialize (e.g. ONNX model missing),
Engram logs a warning and falls back to no reranking rather than refusing to
start.

## Troubleshooting

- **First run hangs at "starting"** — Ollama is pulling models. Check with
  `docker compose logs -f ollama init-models`. Total ~1.6GB; subsequent runs
  hit cache.
- **`embedding_failed` errors after model swap** — Qdrant collection dimension
  is fixed at first boot. If you change `embedding.model` to one with a
  different `dim`, drop the collection: `curl -X DELETE
  http://localhost:6333/collections/memories` then restart engram.
- **`crossenc reranker unavailable`** in logs — the ONNX model file isn't at
  `rerank.crossenc_model_path`. Either run `scripts/download-models.sh`,
  switch `rerank.provider` to `llm` or `none`, or mount the model into the
  container.
- **MCP client can't connect** — confirm the container name with
  `docker compose ps` and adjust the `docker exec` invocation. The binary
  must be invoked with `--mcp` for stdio mode.
- **Pending vectors growing** — Qdrant was unreachable during ingest. The
  reconciler retries in the background; check `engram_pending_vectors` in
  `/metrics`. Inspect with `SELECT count(*) FROM pending_vectors;` against
  Postgres.
- **`degraded: true` in retrieve stats** — vector leg failed, results came
  from BM25 only. Usually transient; check Qdrant health.

## Development

```bash
make build   # go build ./...
make test    # go test ./...
make lint    # go vet ./...
make up      # docker compose up -d
make down    # docker compose down
```

Integration tests (testcontainers, real Postgres + Qdrant + httptest Ollama):

```bash
DOCKER_HOST=unix://$HOME/.colima/default/docker.sock \
TESTCONTAINERS_RYUK_DISABLED=true \
go test -tags integration ./test/integration/... -v -timeout 300s
```

Eval harness (recall@k on a fixture corpus):

```bash
go run ./test/eval
```

## Project Layout

```
cmd/engram/                # binary entrypoint
internal/config/           # YAML + env loader
internal/mcp/              # MCP stdio transport
internal/httpapi/          # HTTP transport + /metrics
internal/memory/           # Ingestor, Retriever, Reconciler
internal/embed/            # Ollama embedder
internal/chunk/            # Semantic chunker
internal/fusion/           # RRF
internal/rerank/           # crossenc | llm | remote
internal/store/postgres/   # MetaStore + migrations
internal/store/qdrant/     # VectorStore
internal/graph/            # GraphStore interface + nop impl
deploy/                    # Dockerfile, compose, init scripts
test/integration/          # roundtrip suite
test/eval/                 # recall@k fixture
```

## License

See `LICENSE`.
