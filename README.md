# Engram

A portable, MCP-native memory harness in Go. Engram gives LLM agents a durable
hybrid-retrieval memory: Qdrant for dense vectors, Postgres for BM25 + metadata,
Ollama or local ONNX inference for embeddings, and a swappable reranker
(cross-encoder, LLM, or remote API). Runs end-to-end via Docker Compose. Same
handlers serve both an MCP stdio transport and an HTTP+JSON API.

## Quickstart (Local with ONNX)

**Fast local setup with native ONNX embeddings:**

```bash
git clone https://github.com/gregdhill/engram.git
cd engram

# 1. Build binary with local ONNX inference (one-time, ~5 min)
make build-onnx

# 2. Start full backend stack (Qdrant, Postgres, Neo4j)
docker compose -f deploy/docker-compose.yml up -d
# Waits for services to be ready on localhost:5432, 6333, 7474

# 3. Start Engram binary in another terminal
./bin/engram -config engram.local.yaml
# Starts HTTP server on :8080
```

The `bin/engram` binary uses local ONNX inference (2–5ms per embedding vs. 50–100ms for Ollama).
Models are cached in `models/nomic-embed-text-v1.5/` (~520 MB, downloaded once).

**Test the API:**

```bash
# Store a memory
curl -s -X POST http://localhost:8080/v1/memories \
  -H "Content-Type: application/json" \
  -d '{"user_id":"greg","content":"The mitochondria is the powerhouse of the cell.","source":"biology","metadata":{"tags":["biology"]}}' | jq .MemoryID

# Retrieve context
curl -s -X POST http://localhost:8080/v1/retrieve \
  -H "Content-Type: application/json" \
  -d '{"user_id":"greg","query":"cell energy","k":3}' | jq '.Results | length'
```


## Model Setup

Models are not stored in git. Download them before first run:

```bash
bash scripts/download-model.sh           # downloads latest release
bash scripts/download-model.sh v0.2.0    # specific version
```

Models are saved to `models/nomic-embed-text-v1.5/` (~520 MB, one-time download).

## CLI Commands

Engram provides a command-line interface for memory management:

```bash
# Store memories from text or files
engram add "Remember this fact"
engram add -f path/to/file.txt
engram add -d path/to/directory/

# Search memories
engram find "query text"
engram find -k 10 "semantic search"

# Delete memories
engram rm <memory-id>
engram remove -q "old memories"  # Delete by semantic search
engram rm --dry-run <id>         # Preview without deleting

# View server status
engram status
```



## Development Workflow

### Setup (once after cloning)

```bash
bash scripts/install-hooks.sh
```

### Branching strategy

All work happens on short-lived feature branches. Direct pushes to `main` are blocked.

```
main          ← stable, protected
  └── feat/my-feature    ← branch off main, work here
  └── fix/the-bug        ← same pattern for fixes
```

**Start a feature:**

```bash
git checkout -b feat/your-feature
# ... commit work ...
```

**Merge to main (after QA):**

```bash
./scripts/merge-to-main.sh
```

This script:
1. Rebases your branch onto latest `main`
2. Pushes the feature branch — `pre-push` hook runs `go test ./...`
3. Fast-forward merges to `main` (no merge commits)
4. Pushes `main`
5. Deletes the feature branch locally and remotely

### Hooks enforced automatically

| Hook | What it does |
| --- | --- |
| `pre-commit` | `go fmt` + `go vet` on staged Go files |
| `commit-msg` | Conventional commit format (`feat:`, `fix:`, etc.) |
| `pre-push` | `go test ./...` when pushing to `main` |
| `post-merge` | Auto-syncs hooks and runs `go mod tidy` if deps changed |

Bypass any hook with `GIT_SKIP_HOOKS=1 git <command>` (not recommended).

## MCP Integration

Engram exposes MCP tools for memory operations. All original tool names remain registered for backwards compatibility.

### MCP Tool Reference

| Canonical name | Aliases                              | Description                 |
| -------------- | ------------------------------------ | --------------------------- |
| `write_memory` | `store_memory`, `remember`           | Store a memory              |
| `read_memory`  | `retrieve_context`, `recall`         | Retrieve relevant memories  |
| `user_state`   | `get_user_state`, `status`           | Get memory stats for a user |
| `erase_memory` | `forget`                             | Delete a memory by ID       |

Connect via stdio against the running container or binary.

### OpenCode — Local ONNX Binary (Recommended)

**Setup (once):**

```bash
cd /path/to/engram
make build-onnx
docker compose -f deploy/docker-compose.yml up -d
```

**Configure OpenCode** (`~/.config/opencode/opencode.jsonc`):

```jsonc
{
  "mcp": {
    "engram": {
      "type": "local",
      "command": ["/path/to/engram/bin/engram", "-mcp", "-config", "/path/to/engram/engram.local.yaml"],
      "enabled": true,
      "timeout": 10000,
      "environment": {
        "NEO4J_PASSWORD": "{env:NEO4J_PASSWORD}"
      }
    }
  }
}
```

The `-mcp` flag runs engram in stdio-only mode, disabling the HTTP server. This prevents port conflicts when running alongside an existing engram HTTP daemon.



**Restart OpenCode** after config changes:
```
ctrl+p → "restart MCP server"  (or restart OpenCode entirely)
```

**Verify it's working:**
```
ctrl+p → "list MCP servers"   # engram should appear
```

### Agent behaviour (AGENTS.md)

The repo ships an `AGENTS.md` that OpenCode injects into every session automatically.
It instructs any model to:

- **Store immediately** when the user provides facts — no asking for permission
- **Split by entity** — one `write_memory` call per person, relationship, or fact
- **Retrieve before recommending** — check memory before suggesting frameworks or patterns
- **Always use `user_id: "greg"`**

This means any model (Claude, Gemini, Llama, etc.) will use Engram correctly as long as
it's running and connected.

### Skills

Three skills ship with this repo under `.opencode/skills/`:

| Skill | Trigger | Purpose |
|-------|---------|---------|
| `remember` | `/skill remember` | Store facts — splits people/relationships, falls back to `engram put` if MCP unavailable |
| `recall` | `/skill recall` | Retrieve memories — falls back to `engram get` if MCP unavailable |
| `engram` | `/skill engram` | Full reference — signatures, tag taxonomy, workflow patterns |

**Quick usage:**

```
/skill remember   → then give it facts: "Zita ist mit Karim zusammen..."
/skill recall     → then ask: "what do I know about Zita?"
```

`remember` and `recall` are the fast path. `engram` is the full reference.

#### Install skills globally (available in every project)

Copy all three skills to `~/.config/opencode/skills/`:

```bash
cp -r .opencode/skills/engram ~/.config/opencode/skills/
cp -r .opencode/skills/remember ~/.config/opencode/skills/
cp -r .opencode/skills/recall ~/.config/opencode/skills/
```

Once installed, any model in any project can use `/skill remember` and `/skill recall`
to store and retrieve memories — using MCP tools if available, or the `engram` CLI
binary as a fallback.

### Global AGENTS.md

For memory retrieval to work in **any** project — not just this repo — copy the global
agent rules:

```bash
cp docs/global-agents.md ~/.config/opencode/AGENTS.md
```

Or manually ensure `~/.config/opencode/AGENTS.md` contains the session-start retrieval
instructions (see `docs/global-agents.md`). This tells every model to call
`user_state` + `read_memory` automatically at the start of each session.

### Claude Desktop (`claude_desktop_config.json`)

```json
{
  "mcpServers": {
    "engram": {
      "command": "/path/to/engram/bin/engram",
      "args": ["-config", "/path/to/engram/engram.local.yaml"]
    }
  }
}
```

Same setup as OpenCode — uses local ONNX binary.

## CLI Usage

The `engram` binary doubles as a CLI client when called with a subcommand.
The daemon must be running (`make daemon-install`).

### put — store facts

Accepts raw text. A local Ollama model (`llama3.2:1b`) parses it into structured
facts, splits by entity, and stores each separately. If Ollama is unavailable, the
raw text is stored as a single untagged fact.

```bash
engram -config engram.local.yaml put "Zita and Karim are a couple, Karim born 1981"
# stored: Zita and Karim are a couple [people relationship zita karim] id=abc12345
# stored: Karim, born 1981 [people person karim] id=def67890
```

### get — retrieve memories

```bash
engram -config engram.local.yaml get "Zita relationship"
# 0.912  Zita and Karim are a couple
# 0.871  Zita, born 1985-07-25
# retrieved 2 results in 84ms
```

### status — memory count and server health

```bash
engram -config engram.local.yaml status
# memories: 42  chunks: 68
# last:     2026-04-29T10:23:11Z
# sources:  conversation, notes
```

### Extending the CLI

The CLI is structured for easy extension:

| File | Purpose |
|---|---|
| `internal/cli/client.go` | HTTP client — add new API endpoints here |
| `internal/cli/parse.go` | `Parser` interface — swap in a different model or remote API |
| `internal/cli/commands.go` | Command handlers — add new subcommands here |
| `cmd/engram/main.go` | `runCLI()` dispatch — wire new subcommands into the switch |

To add a new subcommand: implement a `RunXxx` function in `commands.go`, add a case
to the switch in `runCLI()`, done.

## Daemonizing Engram

Run Engram as a persistent background service that survives shell close and starts automatically on login.

### What gets daemonized

| Component | Method | Survives reboot |
|---|---|---|
| Postgres, Qdrant, Neo4j | Docker `restart: unless-stopped` | Yes (if Docker starts on login) |
| Engram binary (HTTP) | launchd `KeepAlive: true` | Yes |

> **Note:** OpenCode's MCP connection (`type: local`) spawns its own engram process for stdio. The daemon covers the HTTP API only.

### Install

**Prerequisites:**

Install the ONNX runtime shared library (required for local embeddings):

```bash
brew install onnxruntime
```

Set `NEO4J_PASSWORD` in your shell profile so launchd inherits it:

```bash
echo 'export NEO4J_PASSWORD=engrampass' >> ~/.zshenv
source ~/.zshenv
```

Then install the daemon:

```bash
make daemon-install
```

This will:
1. Copy `deploy/ai.engram.plist` to `~/Library/LaunchAgents/`
2. Load it with `launchctl` (starts on login, restarts on crash)
3. Start the Docker backend stack (Postgres, Qdrant, Neo4j)
4. Wait up to 30s for Postgres and Qdrant to be ready

### Uninstall

```bash
make daemon-uninstall
```

### Logs

```bash
tail -f ~/Library/Logs/engram/engram.out.log
tail -f ~/Library/Logs/engram/engram.err.log
```

### Troubleshooting

| Symptom | Fix |
|---|---|
| Daemon not starting | `launchctl list \| grep engram` — check exit code; logs at `~/Library/Logs/engram/` |
| Binary not found | Run `make build-onnx` first |
| Backends not ready | `docker compose -f deploy/docker-compose.yml ps` |
| Restart daemon | `launchctl unload ~/Library/LaunchAgents/ai.engram.plist && launchctl load ~/Library/LaunchAgents/ai.engram.plist` |

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
| `embedding.provider`          | `ollama`                                               | `ollama` \| `onnx`. Use `onnx` for local ONNX inference (requires `build -tags onnxembed`). |
| `embedding.base_url`          | `http://ollama:11434`                                  | Ollama endpoint (unused when `provider=onnx`). |
| `embedding.model`             | `nomic-embed-text`                                     | Must match `embedding.dim`. Unused when `provider=onnx`. |
| `embedding.model_dir`         | `""`                                                   | Path to directory with `model.onnx` + `tokenizer.json`. Required when `provider=onnx`. |
| `embedding.max_seq_len`       | `8192`                                                 | Max token length for ONNX tokenizer truncation. |
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

## ONNX Build (Local Inference)

By default Engram uses Ollama for embeddings. The `onnxembed` build tag swaps in
a local ONNX runtime (via `yalue/onnxruntime_go`) for 5–10x faster ingestion with
no network hop.

### Prerequisites

- Go 1.21+ with CGO enabled
- Rust toolchain (`daulet/tokenizers` compiles a Rust crate via CGO)
- ~600 MB disk for model files

**Install Rust if missing:**
```bash
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
source $HOME/.cargo/env
```

### 1. Fetch model files

```bash
MODEL_DIR=/path/to/models/nomic-embed-text-v1.5 \
HF_REVISION=16999335555c8808544a0344d2d4d9834ba70404 \
bash scripts/fetch-models.sh
```

Expected output in `MODEL_DIR`: `model.onnx` (~580 MB) and `tokenizer.json` (~2 MB).
The script uses conditional HTTP (`-z`) so re-runs are cheap.

### 2. Build with ONNX tag

```bash
CGO_ENABLED=1 go build -tags onnxembed -o bin/engram ./cmd/engram/
# or via make:
make build-onnx
```

`yalue/onnxruntime_go` bundles a pre-built ONNX Runtime shared library for
common platforms (darwin/arm64, linux/amd64, linux/arm64), so no separate
ONNX Runtime install is needed.

### 3. Configure

In your `engram.yaml` (or `engram.local.yaml`):

```yaml
embedding:
  provider: onnx
  model_dir: /path/to/models/nomic-embed-text-v1.5
  max_seq_len: 8192
  dim: 768       # nomic-embed-text-v1.5 produces 768-dim vectors
  batch: 32
```

Or via environment:
```bash
ENGRAM_EMBEDDING_PROVIDER=onnx \
ENGRAM_EMBEDDING_MODEL_DIR=/path/to/models/nomic-embed-text-v1.5 \
./bin/engram -config engram.yaml
```

### 4. Verify

```bash
# Readyz endpoint reports embedder reachable
curl -s http://localhost:8080/readyz | jq .
```

On startup, the ONNX embedder runs a warmup pass. If it fails (wrong path, wrong
architecture), Engram logs the error and exits — it does **not** silently fall
back, so misconfiguration is obvious.

### ONNX in Docker Compose

The `model-init` sidecar downloads models into a named volume automatically:

```bash
ENGRAM_EMBEDDING_PROVIDER=onnx docker compose -f deploy/docker-compose.yml up -d
```

The `engram` service waits for the sidecar to exit before starting.

## Development

```bash
make build        # go build ./...
make build-onnx   # CGO_ENABLED=1 go build -tags onnxembed -o bin/engram ./cmd/engram/
make test         # go test ./...
make lint         # go vet ./...
make up           # docker compose up -d
make down         # docker compose down
```

### Local Testing (ONNX Embedder)

```bash
# Verify ONNX build without full backend stack
./test_onnx_local.sh

# Check binary + model files
file bin/engram
ls -lh models/nomic-embed-text-v1.5/
```

### Integration Testing

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
internal/embed/            # Ollama embedder + ONNX embedder (build tag: onnxembed)
internal/chunk/            # Semantic chunker
internal/fusion/           # RRF
internal/rerank/           # crossenc | llm | remote
internal/store/postgres/   # MetaStore + migrations
internal/store/qdrant/     # VectorStore
internal/store/neo4j/      # GraphStore: Neo4j adapter (optional)
internal/graph/            # GraphStore interface + nop impl
deploy/                    # Dockerfile, compose, init scripts
test/integration/          # roundtrip suite
test/eval/                 # recall@k fixture
```

## Architecture

Engram orchestrates a hybrid-retrieval pipeline across specialized backends:

```
┌────────────────────────────────────────────────────────┐
│ Transports:  MCP (stdio)  +  HTTP (JSON over POST)     │
└──────────────────────┬─────────────────────────────────┘
                       │ shared handlers
┌──────────────────────▼─────────────────────────────────┐
│ Memory Service (internal/memory)                       │
│   Ingestor   ·   Retriever   ·   Reconciler            │
└──┬────────────┬───────────┬────────────┬───────────────┘
   │            │           │            │
   │            │           │            │
   ▼            ▼           ▼            ▼
┌──────┐   ┌──────────┐  ┌────────┐  ┌──────────────────┐
│Qdrant│   │ Postgres │  │ Neo4j  │  │ Ollama | ONNX    │
│      │   │          │  │        │  │  embed (+ LLM    │
│vecs  │   │metadata, │  │chunk & │  │  rerank w/Ollama)│
│cosine│   │tsvector, │  │memory  │  └──────────────────┘
│search│   │BM25,     │  │graph   │       │
│      │   │pending_  │  │NEXT,   │       │ optional:
└──────┘   │vectors   │  │SIMILAR,│       │  cross-encoder
           │reconcile │  │OF      │       │  ONNX
           └──────────┘  └────────┘       │  remote rerank API

Ingest write paths:
  Postgres (canonical)  ── must succeed
  Qdrant upsert         ── failure → pending_vectors (reconciled)
  Neo4j (async, optional) ── best-effort; graph error ≠ ingest failure

Retrieve read paths:
  1. Embed query via Ollama
   2. Parallel fanout: Qdrant.Search + Postgres.SearchBM25
   3. RRF fusion → top-k
   4. Optional: Neo4j.ExpandRelated via NEXT|SIMILAR edges (depth=1)
   Note: step 1 uses Ollama OR local ONNX depending on `embedding.provider`.
  5. Reranker (crossenc / llm / remote / none)
  6. Return top-final_k
```

### Graph-based Memory Expansion (Optional)

If you enable Neo4j, Engram builds a semantic relationship graph of chunks and memories:

- **Sequential edges** (`NEXT`): chunks within the same memory, by order.
- **Similarity edges** (`SIMILAR`): cross-memory chunks with cosine similarity ≥ threshold.
- **Expansion on retrieve**: after fusion ranking, walk the graph (depth 1) to find related chunks.

This surfaces implicit connections between memories. For example, retrieving "What is the mitochondria?" may expand to include nearby notes on cell biology if they share SIMILAR edges.

To enable Neo4j in local dev:

```bash
export NEO4J_PASSWORD=engrampass
docker compose -f deploy/docker-compose.yml up
```

Then set `ENGRAM_GRAPH_PROVIDER=neo4j` in your config or environment. If Neo4j is unavailable, Engram logs a warning and continues with vector + BM25 retrieval only.

For details on schema, edge types, and failure semantics, see the Neo4j section of the configuration reference above.

## License

See `LICENSE`.
