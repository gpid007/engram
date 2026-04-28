# Engram + OpenCode Cheatsheet

## One-Time Setup

```bash
# 1. Build binary with ONNX (fast local inference — no Ollama needed)
make build-onnx

# 2. Seed knowledge-base with project context
bash scripts/seed-memories.sh

# Done! Config is at ~/.config/opencode/opencode.json
```

## Daily Workflow

```bash
# Terminal 1: Start backend services + Engram
./run-local.sh

# Terminal 2: Start OpenCode
opencode

# In OpenCode, use Engram:
use engram to store: "My coding preference"
use engram to retrieve memories about "error handling"
```

## Engram Tools

| Tool | Usage |
|------|-------|
| `store_memory` | Save user preference or pattern |
| `retrieve_context` | Find relevant memories |
| `get_user_state` | Check user's aggregated context |

## Common Commands

### Store Memory
```
use engram to store: "I prefer async/await patterns in JavaScript"
```

### Retrieve Memories
```
What do I know about my testing preferences? use engram
```

### Check Connection
```
/mcp list           # See engram active
/mcp debug engram   # Test connection
/mcp tools engram   # List available tools
```

## Configuration

**OpenCode Config**: `~/.config/opencode/opencode.json`
**Engram Config**: `/Users/greg/git/engram/engram.local.yaml`

Quick edit:
```bash
nano ~/.config/opencode/opencode.json
# Change "enabled": true/false
# Change "timeout": 10000 (milliseconds)
```

## Services

Started by `./run-local.sh` (via docker compose):

| Service | Port | Purpose |
|---------|------|---------|
| Postgres | 5432 | Metadata + BM25 store |
| Qdrant | 6333 | Vector search (HTTP dashboard) |
| Qdrant | 6334 | Vector search (gRPC) |
| Neo4j | 7687 | Graph memory (optional) |
| Engram | 8080 | HTTP API + MCP stdio |

Note: Ollama is **not required** — embeddings run locally via ONNX (`bin/engram -tags onnxembed`).

## Health Checks

```bash
# Check Engram (liveness + readiness)
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz

# Check Qdrant
curl http://localhost:6333/

# Check Postgres
psql -h localhost -U engram -d engram -c "SELECT 1"

# Check all docker services
docker compose -f deploy/docker-compose.yml ps
```

## Common Issues & Fixes

### "engram not found in `/mcp list`"
```bash
# Verify binary exists
ls bin/engram

# Verify config
cat ~/.config/opencode/opencode.json | grep engram

# Restart OpenCode
# Press Ctrl+C, then: opencode
```

### "Connection refused"
```bash
# Start services
./run-local.sh

# Or check if already running
docker ps
```

### Tools timeout
```bash
# Edit config
nano ~/.config/opencode/opencode.json

# Change timeout (in milliseconds)
"timeout": 30000

# Restart OpenCode
```

### Embedding fails or is slow (>50ms)
```bash
# Binary may be stub build (without ONNX). Rebuild:
make build-onnx

# Verify model files exist
ls -lh models/nomic-embed-text-v1.5/
# Expected: model.onnx (~522MB) + tokenizer.json (~695KB)

# Re-download if missing
MODEL_DIR=models/nomic-embed-text-v1.5 bash scripts/fetch-models.sh
```

## Useful Prompts

### Store a Pattern
```
I just found a great pattern for error handling. 
Store this in my memory using engram:
[code snippet here]
```

### Retrieve Before Changes
```
Before I refactor, use engram to show me:
- My preferred patterns for database queries
- Any related architectural decisions
- Known issues I've documented
```

### Build on History
```
What do I know about this project's architecture?
Use engram to retrieve my previous decisions,
then suggest how to implement the new feature.
```

### Create New Memory
```
Store this in my knowledge for future reference:
"We use [technology] because [specific reasons]"
Tags: [tag1, tag2]
```

## Docs

Quick links to documentation:

- **5-Minute Setup**: `docs/OPENCODE_QUICKSTART.md`
- **Full Guide**: `docs/OPENCODE_INTEGRATION.md`
- **Summary**: `docs/OPENCODE_SETUP_SUMMARY.md`
- **Best Practices**: `AGENTS.md`
- **Architecture**: `README.md`

## Key Files

```
/Users/greg/git/engram/
├── bin/engram                  ← Binary
├── run-local.sh                ← Start services
├── engram.local.yaml           ← Config
├── scripts/setup-opencode.sh   ← Auto-setup
└── docs/OPENCODE_*.md          ← Guides
```

## Keyboard Shortcuts in OpenCode

| Key | Action |
|-----|--------|
| `Tab` | Toggle Plan/Build mode |
| `Ctrl+K` | Search |
| `/` | Commands |
| `Ctrl+C` | Exit |
| `@` | Search files |

## Remember

- **Engram stores memories**: Preferences, patterns, decisions
- **OpenCode retrieves context**: Before making recommendations
- **Tools add context tokens**: Use specific queries
- **Memories are local**: By default, stored in local Postgres
- **MCP protocol**: Engram connects via stdio (no network needed)

---

**Quick Reference**: `cat OPENCODE_CHEATSHEET.md`
