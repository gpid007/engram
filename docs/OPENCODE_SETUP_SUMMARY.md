# Engram + OpenCode Setup Summary

## What You Have

✅ **Engram MCP Server** — A memory harness for LLM agents with durable hybrid retrieval
✅ **OpenCode Integration** — Complete setup to use Engram tools directly in OpenCode
✅ **Local Development** — Docker-compose stack (Postgres, Qdrant, Ollama)
✅ **Documentation** — Quick-start + full integration guides

---

## Files Created

| File | Purpose |
|------|---------|
| `docs/OPENCODE_INTEGRATION.md` | Full integration guide with troubleshooting |
| `docs/OPENCODE_QUICKSTART.md` | 5-minute setup guide |
| `AGENTS.md` | Best practices for using Engram in OpenCode |
| `scripts/setup-opencode.sh` | Automatic OpenCode configuration |
| `run-local.sh` | Start all local services (Postgres, Qdrant, Ollama) |
| `engram.local.yaml` | Config for local development |
| `deploy/docker-compose.yml` | Updated with port mappings |

---

## Quick Start

### 1. Build Engram
```bash
cd /Users/greg/git/engram
go build -o ./bin/engram ./cmd/engram
```

### 2. Configure OpenCode (Automatic)
```bash
./scripts/setup-opencode.sh
```

This creates/updates `~/.config/opencode/opencode.json` with Engram MCP config.

### 3. Start Services
```bash
./run-local.sh
```

Starts:
- Postgres 16 (localhost:5432)
- Qdrant (localhost:6333-6334)
- Ollama (localhost:11434) with models
- Engram MCP stdio server

### 4. Launch OpenCode
```bash
cd /your/project
opencode
```

### 5. Verify & Use
```
/mcp list              # See "engram" active
use engram to store: "I prefer async/await in Go"
```

---

## Architecture

```
OpenCode (TUI/CLI)
    ↓ (MCP stdio)
Engram MCP Server (stdio transport)
    ↓ (HTTP/gRPC)
┌─────────────────────────────┐
│  Engram Services            │
├─────────────────────────────┤
│ • Postgres (metadata+BM25)  │
│ • Qdrant (vector search)    │
│ • Ollama (embeddings+LLM)   │
└─────────────────────────────┘
```

---

## Available Tools

Once connected, these MCP tools are available in OpenCode:

### `store_memory`
Store a memory for a user
```
use engram to store: "I prefer TypeScript" for user john_doe
```

### `retrieve_context`
Retrieve memories for a query
```
use engram to find memories about "my coding preferences"
```

### `get_user_state`
Get aggregated user context
```
use engram to show state for user john_doe
```

---

## Configuration

### OpenCode Config (`~/.config/opencode/opencode.json`)

```json
{
  "mcp": {
    "engram": {
      "type": "local",
      "command": [
        "/Users/greg/git/engram/bin/engram",
        "-config",
        "/Users/greg/git/engram/engram.local.yaml"
      ],
      "enabled": true,
      "timeout": 10000
    }
  }
}
```

### Engram Config (`engram.local.yaml`)

```yaml
server:
  mcp_stdio: true           # Enable MCP stdio
  http_addr: ":8080"        # Also expose HTTP API

embedding:
  provider: ollama          # Use local Ollama
  base_url: http://localhost:11434
  model: nomic-embed-text

vector:
  provider: qdrant          # Vector database
  addr: localhost:6334

meta:
  provider: postgres        # Metadata database
  dsn: postgres://engram:engram@localhost:5432/engram
```

---

## Use Cases

### 1. Store Code Patterns
```
I just learned this pattern for error handling in Go. 
Can you store it in my memories using engram?

```go
if err != nil {
  return fmt.Errorf("wrap: %w", err)
}
```
```

### 2. Retrieve Context Before Changes
```
Use engram to check: What patterns have I established for 
REST API error handling? Then review this endpoint 
implementation to ensure consistency.
```

### 3. Track Architecture Decisions
```
Store this decision in my memory: "We use event sourcing 
for the order service because [reasons]"
```

### 4. Build on Previous Work
```
What do I know about this project's dependencies?
Use engram to recall relevant memories, then suggest 
the best HTTP client library.
```

---

## Troubleshooting

### "engram" not in `/mcp list`
- Check binary exists: `ls -la bin/engram`
- Check config path: `cat ~/.config/opencode/opencode.json | grep engram`
- Restart OpenCode after running `setup-opencode.sh`

### "Connection refused" errors
- Services not running: `./run-local.sh`
- Check services: `docker ps | grep -E "postgres|qdrant|ollama"`
- Test health: `curl http://localhost:8080/healthz`

### Tools timing out
- Increase timeout: `"timeout": 30000` in OpenCode config
- Check Ollama: `curl http://localhost:11434/`
- Verify Postgres: `docker logs deploy-postgres-1`

### Memory embedding fails (HTTP 404)
- Models not pulled: `curl http://localhost:11434/api/tags`
- Pull manually: `curl -X POST http://localhost:11434/api/pull -d '{"name":"nomic-embed-text"}'`

---

## Development Workflow

### Daily Setup
```bash
# 1. Start services (first terminal)
cd /Users/greg/git/engram
./run-local.sh

# 2. Start OpenCode (second terminal)
cd /your/project
opencode

# 3. Use Engram in your workflow
# Type: use engram tools...
```

### After Changes to Engram
```bash
# 1. Stop services
# Press Ctrl+C in run-local.sh terminal

# 2. Rebuild
go build -o ./bin/engram ./cmd/engram

# 3. Restart
./run-local.sh
```

---

## Testing

### Unit Tests
```bash
go test ./...
```

### Integration Tests
```bash
DOCKER_HOST=unix://$HOME/.colima/default/docker.sock \
TESTCONTAINERS_RYUK_DISABLED=true \
go test -tags integration ./test/integration/... -v
```

### CI/CD
```bash
# GitHub Actions automatically runs on push
# Check: https://github.com/gpid007/engram/actions
```

---

## Next Steps

1. **Read Full Docs**: `docs/OPENCODE_INTEGRATION.md`
2. **Understand Architecture**: `README.md`
3. **Create Custom Commands**: https://opencode.ai/docs/commands/
4. **Add to AGENTS.md**: Project-specific memory rules
5. **Deploy Production**: Set up remote Engram instance for team

---

## Key Files to Remember

```
/Users/greg/git/engram/
├── bin/engram                    # Compiled binary
├── run-local.sh                  # Start everything
├── engram.local.yaml             # Local config
├── scripts/setup-opencode.sh     # Auto-config
├── docs/OPENCODE*.md             # Documentation
├── AGENTS.md                     # Best practices
├── README.md                     # Architecture
└── deploy/docker-compose.yml     # Services
```

---

## Support

- **OpenCode Docs**: https://opencode.ai/docs/
- **Engram Docs**: `README.md` in this repo
- **Report Issues**: https://github.com/anomalyco/opencode/issues
- **OpenCode Discord**: https://opencode.ai/discord

---

**You're all set!** 🎉

Next: Run `./scripts/setup-opencode.sh` then `./run-local.sh`
