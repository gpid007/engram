---
name: engram
description: Use Engram MCP tools to store and retrieve persistent memory across sessions. Covers when to call store_memory, retrieve_context, get_user_state, tag taxonomy, user_id conventions, local setup, and troubleshooting.
compatibility: opencode
metadata:
  user_id: greg
  project: engram
---

# Engram Skill

Engram is a hybrid-retrieval memory server for LLMs. It stores memories in Postgres (BM25) and Qdrant (dense vectors), fuses results with RRF, and serves them over MCP stdio to OpenCode.

## When to use Engram tools

**`store_memory`** — call when:
- User states a preference, pattern, or decision
- A workaround or solution to a known problem is found
- An architecture decision is made ("we use X because Y")
- A new coding pattern is established
- End of a significant task — capture what was learned

**`retrieve_context`** — call before:
- Making a recommendation (language, framework, library, pattern)
- Refactoring — check what patterns the user follows
- Architecture decisions — check prior decisions
- Any task where prior context could change the approach

**`get_user_state`** — call when:
- Starting a new session and need orientation
- User asks "what do we know about X" broadly
- Debugging memory gaps

## Tool signatures

```
store_memory(
  user_id:  "greg",          # always use "greg" for this user
  content:  string,          # the fact, pattern, or decision to remember
  metadata: {
    tags:   string[],        # e.g. ["preferences", "go", "error-handling"]
    source: string,          # e.g. "conversation", "code-review", "decision"
  }
)
→ { memory_id, chunks_stored, chunks_deduped, stored }

retrieve_context(
  user_id: "greg",
  query:   string,           # natural language query
  k:       5,                # number of results (default 5)
  rerank:  true,             # use cross-encoder reranker when available
)
→ { results: [{ content, score, source, created_at }], stats }

get_user_state(user_id: "greg")
→ { memory_count, chunk_count, first_memory, last_memory, top_sources }
```

## Tag taxonomy

Use these consistent tags for accurate retrieval:

| Category       | Tags                                                     |
| -------------- | -------------------------------------------------------- |
| Language prefs | `["preferences", "languages", "<lang>"]`                 |
| Framework      | `["preferences", "frameworks", "<framework>"]`           |
| Patterns       | `["patterns", "<lang>", "<pattern-type>"]`               |
| Architecture   | `["architecture", "decisions"]`                          |
| Workarounds    | `["workarounds", "<tech>"]`                              |
| Performance    | `["performance", "<tech>"]`                              |
| Testing        | `["testing", "patterns"]`                                |
| Build/tooling  | `["tooling", "<tool>"]`                                  |
| Project-specific | `["project", "engram"]`                                |

## Workflow patterns

### Before recommending anything

```
1. retrieve_context("greg", "<topic> preferences patterns")
2. Read results — surface relevant past decisions
3. Base recommendation on history + current context
4. After decision: store_memory with what was chosen and why
```

### After completing a task

```
store_memory("greg",
  "Fixed ONNX build: daulet/tokenizers needs Rust + CARGO_TARGET_DIR in writable dir",
  { tags: ["workarounds", "onnx", "build"], source: "conversation" }
)
```

### Starting a new session

```
get_user_state("greg")                          # orientation
retrieve_context("greg", "recent decisions")    # what was decided last
retrieve_context("greg", "<current task>")      # relevant context
```

## Running locally

```bash
# Ensure backends are up
docker compose -f deploy/docker-compose.yml up -d

# Start Engram (ONNX binary — no Ollama needed)
./run-local.sh

# Verify MCP connection in OpenCode
/mcp list              # should show "engram"
/mcp debug engram      # test ping
/mcp tools engram      # list store_memory, retrieve_context, get_user_state
```

## Health checks

```bash
curl -s http://localhost:8080/healthz    # liveness
curl -s http://localhost:8080/readyz     # readiness (checks Postgres + Qdrant)
curl -s http://localhost:8080/metrics    # Prometheus metrics
```

## Seed the knowledge-base

On first run or after wiping the DB, seed baseline memories:

```bash
bash scripts/seed-memories.sh
```

This stores project architecture, known workarounds, and preferences into Engram so context is available immediately.

## Config files

| File                          | Purpose                                  |
| ----------------------------- | ---------------------------------------- |
| `engram.local.yaml`           | Active config (ONNX provider, local paths) |
| `engram.yaml`                 | Reference config with all defaults       |
| `~/.config/opencode/opencode.json` | MCP server registration             |

## Key facts about this installation

- **Binary:** `bin/engram` (built with `-tags onnxembed`)
- **Embedding:** Local ONNX, `models/nomic-embed-text-v1.5/model.onnx` (~2–5ms/query)
- **user_id:** always `"greg"` for this user
- **Backends:** Qdrant (vectors), Postgres (BM25 + metadata), Neo4j (optional graph)
- **MCP:** stdio transport, launched by OpenCode from `opencode.json`

## Troubleshooting

| Symptom | Fix |
| ------- | --- |
| `engram` not in `/mcp list` | Restart OpenCode; check `opencode.json` binary path |
| `store_memory` fails | Check `docker ps` — Postgres or Qdrant may be down |
| Empty retrieve results | Run `bash scripts/seed-memories.sh` to seed baseline context |
| Slow embed (>50ms) | Binary may be stub build — run `make build-onnx` |
| `NEO4J_PASSWORD` error | `export NEO4J_PASSWORD=engrampass` before `./run-local.sh` |
