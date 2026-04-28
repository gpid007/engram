# OpenCode Integration - Quick Start

Connect Engram to OpenCode in 5 minutes.

## TL;DR

```bash
# 1. Build Engram
go build -o ./bin/engram ./cmd/engram

# 2. Run setup script
./scripts/setup-opencode.sh

# 3. Start Engram services
./run-local.sh &

# 4. Start OpenCode
opencode

# 5. Use in OpenCode
# Type: "use engram to store the memory 'I like Go'"
```

## Step-by-Step

### 1️⃣ Build Engram

```bash
cd /Users/greg/git/engram
go build -o ./bin/engram ./cmd/engram
```

### 2️⃣ Configure OpenCode

Automatic (recommended):
```bash
./scripts/setup-opencode.sh
```

Manual:
```bash
# Edit ~/.config/opencode/opencode.json
# Add this under "mcp":
{
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
```

### 3️⃣ Start Services

```bash
cd /Users/greg/git/engram
./run-local.sh
```

This starts:
- Postgres (port 5432)
- Qdrant (ports 6333, 6334)
- Ollama (port 11434)
- Engram MCP server (stdio)

### 4️⃣ Launch OpenCode

In a new terminal:
```bash
cd /your/project
opencode
```

### 5️⃣ Verify Connection

```
/mcp list
```

Look for `engram` in the list with status `active`.

### 6️⃣ Start Using

In OpenCode:

```
Store my preference: "I like async patterns in Go" using engram
```

Or:

```
What coding patterns do I prefer? Check engram for my memories.
```

## Available Commands

### Store Memory
```
use engram to store: "Python is great for data science"
```

### Retrieve Memories
```
What do we know about my programming preferences? use engram
```

### Check State
```
Show my user state in engram
```

## Troubleshooting

### "engram" not showing in `/mcp list`

1. Check config path:
   ```bash
   cat ~/.config/opencode/opencode.json | grep engram
   ```

2. Check Engram binary exists:
   ```bash
   ls -la /Users/greg/git/engram/bin/engram
   ```

3. Restart OpenCode:
   ```
   /quit
   opencode
   ```

### "Connection refused" when using Engram

1. Check services running:
   ```bash
   ps aux | grep engram
   docker ps | grep -E "postgres|qdrant|ollama"
   ```

2. Start services:
   ```bash
   ./run-local.sh
   ```

### Tools timing out

Increase timeout in `~/.config/opencode/opencode.json`:
```json
"timeout": 30000
```

## Next Steps

- Read [Full Integration Guide](./OPENCODE_INTEGRATION.md)
- Create [custom commands](https://opencode.ai/docs/commands/)
- Add rules to [AGENTS.md](../AGENTS.md)
- Check [Engram Architecture](../README.md)

## Need Help?

- **OpenCode docs**: https://opencode.ai/docs/
- **Engram docs**: See [README.md](../README.md)
- **Report issues**: https://github.com/anomalyco/opencode/issues
