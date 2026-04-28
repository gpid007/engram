# Engram + OpenCode Cheatsheet

## One-Time Setup

```bash
# 1. Build binary
go build -o ./bin/engram ./cmd/engram

# 2. Auto-configure OpenCode
./scripts/setup-opencode.sh

# Done! Config is saved to ~/.config/opencode/opencode.json
```

## Daily Workflow

```bash
# Terminal 1: Start services
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

Started by `./run-local.sh`:

| Service | Port | Purpose |
|---------|------|---------|
| Postgres | 5432 | Metadata store |
| Qdrant | 6333 | Vector search (HTTP) |
| Qdrant | 6334 | Vector search (gRPC) |
| Ollama | 11434 | Embeddings + LLM |
| Engram | stdio | MCP protocol |

## Health Checks

```bash
# Check Engram
curl http://localhost:8080/healthz

# Check Ollama
curl http://localhost:11434/

# Check Qdrant
curl http://localhost:6333/

# Check Postgres
psql -h localhost -U engram -d engram -c "SELECT 1"

# Check all services
docker ps | grep -E "postgres|qdrant|ollama"
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

### Embedding fails (HTTP 404)
```bash
# Check models
curl http://localhost:11434/api/tags

# Pull models manually
curl -X POST http://localhost:11434/api/pull \
  -H "Content-Type: application/json" \
  -d '{"name": "nomic-embed-text"}'
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
