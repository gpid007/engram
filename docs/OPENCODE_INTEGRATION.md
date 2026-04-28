# Connecting Engram to OpenCode

This guide walks you through setting up Engram as an MCP (Model Context Protocol) server in OpenCode, allowing you to use Engram's memory tools directly from OpenCode.

## Prerequisites

- **OpenCode** installed and configured (see [OpenCode Intro](https://opencode.ai/docs/))
- **Engram** running locally or remotely with HTTP API accessible
- **OpenCode config file** at `~/.config/opencode/opencode.json` or `opencode.jsonc`

## Quick Start

### 1. Start Engram Locally

```bash
cd /Users/greg/git/engram
./run-local.sh
```

This starts Engram with:
- HTTP API on `http://localhost:8080`
- MCP stdio server enabled
- All required services (Postgres, Qdrant, Ollama)

### 2. Configure OpenCode

Add Engram to your OpenCode config:

**~/.config/opencode/opencode.json**

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "engram": {
      "type": "local",
      "command": ["/Users/greg/git/engram/bin/engram", "-config", "/Users/greg/git/engram/engram.local.yaml"],
      "enabled": true,
      "timeout": 10000
    }
  }
}
```

Or use **opencode.jsonc** for comments:

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "engram": {
      // Local MCP server pointing to Engram's stdio transport
      "type": "local",
      "command": [
        "/Users/greg/git/engram/bin/engram",
        "-config", 
        "/Users/greg/git/engram/engram.local.yaml"
      ],
      "enabled": true,
      "timeout": 10000,  // 10 seconds for embedding operations
      "environment": {
        // Optional: set log level for debugging
        "ENGRAM_LOGGING_LEVEL": "debug"
      }
    }
  }
}
```

### 3. Verify Connection

List MCP servers and check Engram is active:

```bash
opencode mcp list
```

You should see `engram` in the list with status `active`.

## Available Tools

Once connected, Engram exposes these MCP tools in OpenCode:

### store_memory
Store a memory for a user.

**Parameters:**
- `user_id` (string) - User identifier
- `content` (string) - Memory content to store
- `tags` (array of strings, optional) - Tags for categorization

**Example usage in OpenCode:**
```
Store a note that "I prefer TypeScript over JavaScript" for user john_doe using engram tools
```

### retrieve_context
Retrieve relevant memories for a user query.

**Parameters:**
- `user_id` (string) - User identifier
- `query` (string) - Query to search against memories
- `limit` (integer, optional) - Number of results (default: 5)

**Example usage in OpenCode:**
```
Retrieve memories about "programming preferences" for user john_doe, use engram
```

### get_user_state
Get aggregated state/context for a user.

**Parameters:**
- `user_id` (string) - User identifier

**Example usage in OpenCode:**
```
Show me the current state for user john_doe using engram tools
```

## Integration Patterns

### 1. In AGENTS.md

Add Engram guidance to your project's agent rules:

```markdown
## Memory Management

When working with user-specific context or preferences:
- Use `engram` tools to store user memories with `store_memory`
- Retrieve relevant context with `retrieve_context` before making recommendations
- Check user state with `get_user_state` for important context

Example: If a user mentions a preference, store it:
"Remember that the user prefers async/await over promises"
```

### 2. Direct Tool Usage

Reference Engram tools directly in your prompts:

```
Use the engram tools to:
1. Store the user's API key preference
2. Retrieve all their previous configuration choices
3. Summarize their typical workflow
```

### 3. Custom Commands

Create custom OpenCode commands that leverage Engram:

**~/.config/opencode/opencode.jsonc**

```jsonc
{
  "commands": {
    "remember": {
      "description": "Store memory in Engram for current user",
      "prompt": "Store the following in Engram memory: {input}",
      "tools": ["engram"]
    },
    "recall": {
      "description": "Retrieve memory from Engram",
      "prompt": "What do we know about: {input}? Use engram tools to search memories.",
      "tools": ["engram"]
    }
  }
}
```

Then use in OpenCode:
```
/remember I prefer dark theme for all interfaces
/recall previous theme preferences
```

## Configuration Reference

### Local Server Configuration

```jsonc
{
  "mcp": {
    "engram": {
      "type": "local",
      "command": [
        "/path/to/bin/engram",
        "-config",
        "/path/to/engram.local.yaml"
      ],
      "enabled": true,
      "timeout": 10000,
      "environment": {
        "ENGRAM_LOGGING_LEVEL": "info",
        "ENGRAM_EMBED_TIMEOUT_MS": "5000"
      }
    }
  }
}
```

**Options:**
- `type` - Must be `"local"` for running Engram locally
- `command` - Array of command and arguments to start Engram
- `enabled` - Enable/disable on startup
- `timeout` - Request timeout in milliseconds (default: 5000ms)
- `environment` - Environment variables for the Engram process

### Remote Server Configuration

For Engram running on a remote server:

```jsonc
{
  "mcp": {
    "engram_prod": {
      "type": "remote",
      "url": "http://engram.mycompany.com/mcp",
      "enabled": true,
      "timeout": 15000,
      "headers": {
        "Authorization": "Bearer {env:ENGRAM_API_KEY}"
      }
    }
  }
}
```

**Options:**
- `type` - Must be `"remote"` for HTTP endpoint
- `url` - HTTP endpoint of Engram MCP server
- `headers` - HTTP headers (supports `{env:VAR_NAME}` for env vars)
- `timeout` - Request timeout in milliseconds

## Troubleshooting

### Engram Not Appearing in MCP List

1. Check the command path exists:
   ```bash
   ls -la /Users/greg/git/engram/bin/engram
   ```

2. Test Engram starts manually:
   ```bash
   /Users/greg/git/engram/bin/engram -config /Users/greg/git/engram/engram.local.yaml
   ```

3. Check OpenCode config syntax:
   ```bash
   opencode config validate
   ```

### "Connection refused" Error

1. Verify Engram is running:
   ```bash
   curl http://localhost:8080/healthz
   ```

2. Check ports are correct in `engram.local.yaml`:
   - Postgres: `localhost:5432`
   - Qdrant: `localhost:6334`
   - Ollama: `localhost:11434`

3. Verify services are started:
   ```bash
   docker ps | grep -E "postgres|qdrant|ollama"
   ```

### Tool Calls Timing Out

1. Increase timeout in OpenCode config:
   ```json
   "timeout": 30000
   ```

2. Check Ollama is responsive:
   ```bash
   curl http://localhost:11434/api/tags
   ```

3. Monitor Engram logs (add to config):
   ```json
   "environment": {
     "ENGRAM_LOGGING_LEVEL": "debug"
   }
   ```

### Memory Embedding Failures

If you see "HTTP 404" errors from embeddings:

1. Verify models are pulled:
   ```bash
   curl http://localhost:11434/api/tags | jq '.models[].name'
   ```

2. Manually pull models:
   ```bash
   curl -X POST http://localhost:11434/api/pull \
     -H "Content-Type: application/json" \
     -d '{"name": "nomic-embed-text"}'
   ```

3. Check Engram config points to correct Ollama:
   ```yaml
   embedding:
     provider: ollama
     base_url: http://localhost:11434
     model: nomic-embed-text
   ```

## Examples

### Example 1: Store a Code Pattern

In OpenCode, chat with an agent:

```
I just learned a great pattern for error handling in Go. 
Can you store this in my Engram memory?

```go
if err != nil {
  log.WithError(err).Errorf("operation failed")
  return nil, fmt.Errorf("wrap: %w", err)
}
```

Use engram to save this under "go-error-handling" pattern
```

### Example 2: Retrieve Project Context

```
Show me what we know about this project's dependencies using engram tools.
Then suggest the best HTTP client library based on what you find.
```

### Example 3: Per-Project Configuration

Create project-specific Engram rules in `AGENTS.md`:

```markdown
# Memory Rules

Use Engram to track:
- Architecture decisions (store with `architecture-decision` tag)
- Testing patterns we've established
- Known issues and their workarounds
- Performance optimizations

Before making major changes, always:
1. Query engram for related context
2. Check if similar patterns exist in our memory
```

## Security Considerations

### Local Development

When running Engram locally with OpenCode:

1. **Data Privacy**: Memory data is stored locally in Postgres
   ```bash
   # Postgres data location (docker-compose)
   /var/lib/postgresql/data
   ```

2. **Environment Variables**: Keep API keys in `.env` or secure vaults:
   ```bash
   # Don't commit these
   export ENGRAM_API_KEY=...
   ```

3. **MCP Timeout**: Set reasonable timeouts to prevent hanging:
   ```json
   "timeout": 10000
   ```

### Production Deployment

For production Engram instances:

```jsonc
{
  "mcp": {
    "engram_prod": {
      "type": "remote",
      "url": "https://engram.mycompany.com/mcp",  // HTTPS only
      "headers": {
        "Authorization": "Bearer {env:ENGRAM_PROD_API_KEY}",
        "X-API-Version": "v1"
      },
      "oauth": {
        "clientId": "{env:ENGRAM_OAUTH_CLIENT_ID}",
        "clientSecret": "{env:ENGRAM_OAUTH_CLIENT_SECRET}"
      }
    }
  }
}
```

## Debugging

### Check MCP Connection

```bash
opencode mcp debug engram
```

### View Engram Logs

If running locally:

```bash
# Follow Engram logs in real-time
tail -f ~/.local/share/engram/logs/engram.log
```

### Test Tools Manually

List available tools:
```bash
opencode mcp tools engram
```

## See Also

- [Engram Architecture](./README.md)
- [OpenCode MCP Documentation](https://opencode.ai/docs/mcp-servers/)
- [Engram HTTP API](./API.md)
- [Engram Local Development](./LOCAL_SETUP.md)
