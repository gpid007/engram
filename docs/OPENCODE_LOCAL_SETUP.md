# Using Engram with OpenCode (Local ONNX Setup)

This guide shows how to integrate Engram's local ONNX embedder with OpenCode on your machine.

## Prerequisites

- Engram built with ONNX: `make build-onnx` ✓
- Docker Compose for backend services (Qdrant, Postgres, Neo4j)
- OpenCode installed locally

## 1. Start the Backend Stack

```bash
cd /path/to/engram
docker compose -f deploy/docker-compose.yml up -d
```

This starts:
- **Qdrant** (localhost:6334) — vector database
- **Postgres** (localhost:5432) — metadata + BM25
- **Neo4j** (localhost:7687) — optional graph memory
- **Ollama** (localhost:11434) — optional, not used (ONNX is default now)

Verify health:
```bash
curl -s http://localhost:8080/readyz | jq .
# Should show: {"status":"ready","checks":{...}}
```

## 2. Configure OpenCode MCP

Edit `~/.config/opencode/opencode.json`:

```json
{
  "mcp": {
    "engram": {
      "type": "local",
      "command": ["/path/to/engram/bin/engram", "-config", "/path/to/engram/engram.local.yaml"],
      "enabled": true,
      "timeout": 10000
    }
  }
}
```

Replace `/path/to/engram` with your actual path, e.g. `/Users/greg/git/engram`.

## 3. Restart OpenCode

Close and reopen OpenCode, or use:
```
ctrl+p → "Restart MCP Servers"
```

Check that Engram loads:
```
ctrl+p → "List MCP Servers"
```

You should see `engram` in the list with status `ready`.

## 4. Use Engram Tools in OpenCode

In any prompt to Claude, you can now use these tools:

### `store_memory`

Store a fact or observation:
```
Store this insight: "ONNX local inference is 5-10x faster than Ollama"
```

OpenCode will call:
```
engram.store_memory(
  user_id="user-1",
  content="ONNX local inference is 5-10x faster than Ollama",
  metadata={tags: ["optimization", "performance"]}
)
```

The memory is saved to Postgres + indexed in Qdrant.

### `retrieve_context`

Recall related memories:
```
What performance improvements have we discussed?
```

OpenCode will call:
```
engram.retrieve_context(
  user_id="user-1",
  query="performance improvements",
  k=5
)
```

Returns top-5 memories ranked by relevance (hybrid BM25 + vector).

### `get_user_state`

Summarize your interaction history:
```
What have we worked on together?
```

OpenCode will call:
```
engram.get_user_state(user_id="user-1")
```

Returns aggregate stats: total memories, chunks, interactions, etc.

## 5. Example Workflow

**Session 1: Store preferences**

```
User: "I prefer TypeScript for backend work."
OpenCode → store_memory(content="...", metadata={lang: "typescript", context: "backend"})
```

**Session 2: Recall and act on it**

```
User: "What backend languages do I prefer?"
OpenCode → retrieve_context(query="backend language preferences")
→ Returns: "I prefer TypeScript for backend work"
OpenCode suggests TypeScript for your new backend service.
```

## 6. Verify It's Working

Check the HTTP API directly:

```bash
# Store a memory via HTTP
curl -s -X POST http://localhost:8080/v1/memories \
  -H "Content-Type: application/json" \
  -d '{
    "content": "Test memory via HTTP",
    "user_id": "test-user",
    "source": "manual"
  }' | jq .

# Retrieve it
curl -s -X POST http://localhost:8080/v1/retrieve \
  -H "Content-Type: application/json" \
  -d '{
    "query": "test memory",
    "user_id": "test-user",
    "k": 3
  }' | jq .
```

## 7. Monitor Performance

ONNX embeddings run locally — watch stats:

```bash
# Metrics endpoint (Prometheus format)
curl -s http://localhost:8080/metrics | grep engram

# Look for:
# engram_embed_duration_ms — embedding latency (should be <10ms for ONNX)
# engram_retrieve_duration_ms — full retrieve time
# engram_memories_stored — cumulative count
```

## 8. Troubleshooting

### OpenCode can't connect to Engram

```bash
# Check if Engram binary exists
ls -lh /path/to/engram/bin/engram

# Check if HTTP API is running
curl -s http://localhost:8080/readyz

# Check OpenCode logs
tail -f ~/.opencode/logs/*.log
```

### Memory operations fail silently

Ensure backend is up:
```bash
docker compose ps
# All services should show "Up"
```

Check Engram logs:
```bash
# If running as binary
# Check stdout/stderr

# If running in Docker
docker compose logs engram
```

### ONNX embedding errors

Verify model files exist:
```bash
ls -lh /path/to/engram/models/nomic-embed-text-v1.5/
# Should show: model.onnx + tokenizer.json
```

If missing, rebuild:
```bash
cd /path/to/engram
make build-onnx
```

## Performance Notes

| Operation              | Latency (ONNX)          | Notes                                      |
| ---------------------- | ----------------------- | ------------------------------------------ |
| Embed single query     | ~2–5ms                  | CPU-local, no network overhead             |
| Embed batch (32 chunks) | ~15–20ms               | Mean-pooled sentence embeddings            |
| Retrieve (vector+BM25) | ~20–50ms               | Parallel Qdrant + Postgres, then RRF fuse |
| Rerank (20 docs)       | ~80–120ms              | ONNX cross-encoder (optional)              |
| Store (with chunks)    | ~50–100ms              | Includes semantic chunking + all indexes   |

## Next Steps

- **Customize:** Edit `engram.local.yaml` to tune `embedding.batch`, `retrieval.final_k`, etc.
- **Backup:** Postgres data is in Docker volume; backup with `docker compose exec postgres pg_dump ...`
- **Scale:** For production, use managed Qdrant/Postgres cloud services.
- **Extend:** Add custom rerankers or graph logic in `internal/rerank/` or `internal/store/neo4j/`

---

**Need help?** See `ONNX_BUILD_COMPLETE.md` for build details or `docs/architecture.md` for internals.
