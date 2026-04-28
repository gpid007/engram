# Engram Documentation Index

Welcome to Engram! This is your guide to understanding, setting up, and using Engram with OpenCode.

## 🚀 Getting Started

Start here if you're new to Engram:

1. **[OpenCode Quick Start](./OPENCODE_QUICKSTART.md)** ⭐ (5 min read)
   - 3 commands to get running
   - Verify connection
   - Start using Engram tools

2. **[Engram README](../README.md)** (Project Overview)
   - What is Engram?
   - Architecture overview
   - Tech stack

3. **[OpenCode Cheatsheet](../OPENCODE_CHEATSHEET.md)** (Quick Reference)
   - Common commands
   - Health checks
   - Troubleshooting quick links

## 📚 Comprehensive Guides

Detailed documentation for deeper understanding:

### OpenCode Integration
- **[OpenCode Integration Guide](./OPENCODE_INTEGRATION.md)** — Full setup guide
  - Prerequisites
  - Configuration details
  - Tool descriptions
  - Troubleshooting with solutions
  - Security considerations
  - Debugging techniques

- **[OpenCode Setup Summary](./OPENCODE_SETUP_SUMMARY.md)** — Architecture & patterns
  - What you have
  - Files created
  - Architecture diagram
  - Use cases
  - Development workflow

### Best Practices
- **[AGENTS.md](../AGENTS.md)** — Memory management rules
  - When to use each tool
  - Best practices
  - Examples
  - Troubleshooting

## 🛠️ Development & Operations

### Local Setup
- **[run-local.sh](../run-local.sh)** — Start all services
  - Postgres 16
  - Qdrant vector DB
  - Ollama LLM server
  - Engram MCP server

- **[engram.local.yaml](../engram.local.yaml)** — Local development config
  - Service endpoints
  - Embedding settings
  - Database configuration

### Automation
- **[setup-opencode.sh](../scripts/setup-opencode.sh)** — Auto-configure OpenCode
  - One command setup
  - Backs up existing config
  - Creates proper MCP config

### Docker
- **[docker-compose.yml](../deploy/docker-compose.yml)** — Service definitions
  - All required containers
  - Port mappings
  - Environment variables

## 📖 API Documentation

- **Engram HTTP API** (available at http://localhost:8080)
  - `GET /healthz` — Health check
  - `GET /readyz` — Readiness check
  - `POST /v1/memories` — Store memory
  - `POST /v1/retrieve` — Query memories
  - `GET /v1/users/{id}/state` — User state
  - `GET /metrics` — Prometheus metrics

## 🏗️ Architecture

### System Design
```
OpenCode (TUI/CLI)
    ↓ (MCP stdio)
Engram MCP Server
    ↓ (HTTP/gRPC)
┌─────────────────────┐
│ Postgres + Qdrant   │
│ Ollama (local LLM)  │
└─────────────────────┘
```

### Data Flow
1. **Ingest**: Content → Chunk → Embed → Store
2. **Retrieve**: Query → Embed → Search (Vector + BM25) → Rerank → Return

See [README.md](../README.md) for detailed architecture.

## 🔑 Key Concepts

### Memory Tools

| Tool | Purpose | When to Use |
|------|---------|------------|
| `store_memory` | Save information | User mentions preferences, patterns, decisions |
| `retrieve_context` | Find relevant memories | Before making recommendations |
| `get_user_state` | Get aggregated context | Need full user profile |

### Services

| Service | Port | Purpose |
|---------|------|---------|
| Postgres | 5432 | Metadata store (BM25 search) |
| Qdrant | 6333-6334 | Vector search |
| Ollama | 11434 | Embeddings + LLM models |
| Engram | stdio | MCP protocol server |

### Configuration

- **OpenCode Config**: `~/.config/opencode/opencode.json`
  - MCP server definition
  - Timeouts and settings
  - Can be auto-configured with `setup-opencode.sh`

- **Engram Config**: `engram.local.yaml`
  - Service endpoints
  - Model settings
  - Logging configuration

## 🔍 Troubleshooting

### Quick Links by Issue

- **Engram not in `/mcp list`** → [setup-opencode.sh](../scripts/setup-opencode.sh)
- **Connection refused** → [run-local.sh](../run-local.sh)
- **Tools timeout** → Increase `timeout` in [opencode.json](https://opencode.ai/docs/config/)
- **Embedding fails** → Check [engram.local.yaml](../engram.local.yaml)
- **Full troubleshooting** → [OPENCODE_INTEGRATION.md](./OPENCODE_INTEGRATION.md#troubleshooting)

## 📝 Workflow Examples

### 1. Store a Code Pattern
```
In OpenCode:
"I learned a great pattern for error handling in Go. 
Use engram to store this with tags: go, error-handling"
```

### 2. Retrieve Context Before Changes
```
"Before refactoring, use engram to show me:
- My error handling patterns
- Related architectural decisions"
```

### 3. Build on History
```
"What do I know about this project's architecture?
Use engram to retrieve my previous decisions,
then suggest implementation approach."
```

## 🧪 Testing

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
- GitHub Actions automatically runs on push
- Check status: https://github.com/gpid007/engram/actions

## 🚢 Deployment

### Local Development
```bash
./run-local.sh        # Postgres + Qdrant + Ollama
```

### Production
- Deploy Engram binary on server
- Configure remote OpenCode MCP server in `opencode.json`
- Use OAuth or API keys for security
- See [OPENCODE_INTEGRATION.md](./OPENCODE_INTEGRATION.md#production-deployment)

## 🔗 External Resources

- **OpenCode Documentation**: https://opencode.ai/docs/
- **Model Context Protocol**: https://modelcontextprotocol.io/
- **Qdrant Vector DB**: https://qdrant.tech/
- **Ollama LLM**: https://ollama.ai/
- **PostgreSQL**: https://www.postgresql.org/

## 💬 Getting Help

### In This Repo
- Check [OPENCODE_CHEATSHEET.md](../OPENCODE_CHEATSHEET.md) for quick answers
- Search [OPENCODE_INTEGRATION.md](./OPENCODE_INTEGRATION.md) for detailed solutions
- Review [AGENTS.md](../AGENTS.md) for best practices

### OpenCode Community
- **Discord**: https://opencode.ai/discord
- **GitHub Issues**: https://github.com/anomalyco/opencode/issues
- **Docs**: https://opencode.ai/docs/

### Engram Project
- **GitHub**: https://github.com/gpid007/engram
- **Issues**: https://github.com/gpid007/engram/issues

## 📋 File Structure

```
engram/
├── README.md                       Main project documentation
├── AGENTS.md                       Memory best practices
├── OPENCODE_CHEATSHEET.md         Quick reference
├── run-local.sh                   Start services
├── engram.local.yaml              Local config
├── go.mod / go.sum               Go dependencies
├── cmd/
│   └── engram/main.go            Entry point
├── internal/                       Core implementation
│   ├── mcp/                       MCP stdio transport
│   ├── httpapi/                   HTTP API
│   ├── memory/                    Memory operations
│   ├── store/                     Postgres + Qdrant
│   ├── embed/                     Ollama embeddings
│   ├── rerank/                    Result reranking
│   └── ...
├── scripts/
│   └── setup-opencode.sh         Auto-configure OpenCode
├── deploy/
│   └── docker-compose.yml        Service definitions
└── docs/
    ├── INDEX.md                   This file
    ├── OPENCODE_QUICKSTART.md     5-minute setup
    ├── OPENCODE_INTEGRATION.md    Full integration guide
    └── OPENCODE_SETUP_SUMMARY.md  Architecture & patterns
```

## 🎯 Next Steps

1. **First Time?** → Read [OPENCODE_QUICKSTART.md](./OPENCODE_QUICKSTART.md)
2. **Ready to Setup?** → Run `./scripts/setup-opencode.sh`
3. **Want Details?** → Check [OPENCODE_INTEGRATION.md](./OPENCODE_INTEGRATION.md)
4. **Quick Lookup?** → See [OPENCODE_CHEATSHEET.md](../OPENCODE_CHEATSHEET.md)
5. **Deeper Dive?** → Explore [README.md](../README.md) and [AGENTS.md](../AGENTS.md)

---

**Happy coding!** 🚀

Start with: `./scripts/setup-opencode.sh && ./run-local.sh`
