# Engram + OpenCode

Quick reference for using Engram memory with OpenCode.

## Setup

```bash
# 1. Build binary with ONNX (local inference — no Ollama needed)
make build-onnx

# 2. Download embedding models (~520 MB, one-time)
bash scripts/download-model.sh

# 3. Start backend services
docker compose -f deploy/docker-compose.yml up -d

# 4. Seed baseline memories (optional but recommended)
bash scripts/seed-memories.sh
```

OpenCode config is at `~/.config/opencode/opencode.jsonc`. The MCP server launches automatically when OpenCode starts.

## Daily Workflow

```bash
# Terminal 1: Start Engram (MCP stdio mode — started automatically by OpenCode)
./bin/engram -mcp -config engram.local.yaml

# Terminal 2: Start OpenCode
opencode
```

## MCP Tools

| Canonical name  | Aliases                              | What it does                |
| --------------- | ------------------------------------ | --------------------------- |
| `write_memory`  | `store_memory`, `remember`           | Store a memory              |
| `read_memory`   | `retrieve_context`, `recall`         | Retrieve relevant memories  |
| `user_state`    | `get_user_state`, `status`           | Memory stats for a user     |
| `erase_memory`  | `forget`                             | Delete a memory by ID       |

## Example Prompts

```
# Store a preference
Remember: I prefer table-driven tests in Go over individual test functions.

# Retrieve before a task
Before you refactor, recall my patterns for database queries and error handling.

# Check what's stored
What do you know about my architecture decisions? Use user_state then recall.

# Forget a memory
forget memory ID: <id>
```

## Services

| Service  | Port | Purpose                         |
| -------- | ---- | ------------------------------- |
| Postgres | 5432 | Metadata + BM25 full-text       |
| Qdrant   | 6333 | Vector search (HTTP dashboard)  |
| Qdrant   | 6334 | Vector search (gRPC)            |
| Neo4j    | 7687 | Graph memory (optional)         |
| Engram   | 8080 | HTTP API (when not in MCP mode) |

## Health Checks

```bash
curl http://localhost:8080/healthz    # liveness
curl http://localhost:8080/readyz     # readiness (Postgres + Qdrant)
curl http://localhost:8080/metrics    # Prometheus metrics
docker compose -f deploy/docker-compose.yml ps
```

## OpenCode Config

`~/.config/opencode/opencode.jsonc`:

```jsonc
{
  "mcp": {
    "engram": {
      "type": "local",
      "command": ["/path/to/engram/bin/engram", "-mcp", "-config", "/path/to/engram/engram.local.yaml"],
      "enabled": true,
      "timeout": 10000
    }
  }
}
```

The `-mcp` flag runs stdio-only (no HTTP port conflicts with the daemon).

## Troubleshooting

| Symptom                        | Fix                                                              |
| ------------------------------ | ---------------------------------------------------------------- |
| `engram` not in `/mcp list`    | Restart OpenCode; check binary path in `opencode.jsonc`         |
| `write_memory` fails           | `docker ps` — Postgres or Qdrant may be down                    |
| Empty retrieve results         | Run `bash scripts/seed-memories.sh`                             |
| Slow embed (>50ms)             | Stub build — run `make build-onnx` and check `models/` exists  |
| `NEO4J_PASSWORD` error         | `export NEO4J_PASSWORD=engrampass` before starting              |
| MCP not connected in OpenCode  | `ctrl+p` → restart MCP server                                   |
