# Engram - Agent Rules

Guidelines for working with Engram as an MCP server in OpenCode.

## Memory Management

**When to use Engram memory tools:**

- Users mention preferences or configurations → `store_memory`
- Need to recall previous decisions or preferences → `retrieve_context`
- Tracking user state or context → `get_user_state`

**Example patterns:**

```
User: "I prefer TypeScript over JavaScript"
Action: Store this preference in Engram for future reference
Tool: engram.store_memory("user-1", "I prefer TypeScript over JavaScript", ["preferences", "languages"])

User: "What languages do I prefer?"
Action: Retrieve memories about language preferences
Tool: engram.retrieve_context("user-1", "language preferences")
```

## Tool Usage

### store_memory
Use when capturing user preferences, patterns, or important context.

```
- User preferences (languages, frameworks, patterns)
- Architecture decisions
- Known workarounds and solutions
- Performance optimizations
- Testing strategies
```

### retrieve_context
Use before:

```
- Making recommendations
- Suggesting code patterns
- Choosing libraries/frameworks
- Architecture decisions
- Refactoring suggestions
```

### get_user_state
Use to understand user's overall context and history.

## Best Practices

1. **Be Specific with Tags**
   - Use consistent, hierarchical tags: `["preferences", "languages"]`
   - Makes retrieval more accurate

2. **Before Major Decisions**
   ```
   Always check: "What does the user know about [topic]?" using engram
   ```

3. **Memory as Documentation**
   - Store important patterns found during coding
   - Capture "why" decisions were made
   - Document workarounds for known issues

4. **Regular Updates**
   - Update memories when preferences change
   - Store new patterns learned
   - Keep context current

## Examples

### Example 1: Framework Choice
```
User: "I'm starting a new project, what framework should I use?"
OpenCode should:
1. Use engram to retrieve user's framework preferences
2. Check for patterns they've used before
3. Consider what worked well in past projects
4. Make recommendation based on history
```

### Example 2: Code Pattern Recognition
```
User: "How should I handle errors in this Go function?"
OpenCode should:
1. Query engram for "Go error handling patterns"
2. Look for what they've done before
3. Suggest consistent with their previous patterns
4. Offer to store new pattern if they create one
```

### Example 3: Dependency Decision
```
User: "What's the best HTTP client library for Node.js?"
OpenCode should:
1. Ask engram what HTTP clients they've used
2. Check for any previous evaluations
3. Remember why previous choices were made
4. Store their decision for future reference
```

## Configuration

### OpenCode Config

Engram is configured in `~/.config/opencode/opencode.json`:

```json
{
  "mcp": {
    "engram": {
      "type": "local",
      "command": ["/path/to/bin/engram", "-config", "/path/to/engram.local.yaml"],
      "enabled": true,
      "timeout": 10000
    }
  }
}
```

### Setup

```bash
# Automatic setup
./scripts/setup-opencode.sh

# Manual config
~/.config/opencode/opencode.json
```

## Commands

### List Available MCP Servers
```
/mcp list
```

### Check Engram Status
```
/mcp debug engram
```

### View Tools
```
/mcp tools engram
```

## See Also

- [Full Integration Guide](docs/OPENCODE_INTEGRATION.md)
- [Quick Start](docs/OPENCODE_QUICKSTART.md)
- [OpenCode Documentation](https://opencode.ai/docs/)
- [Engram README](README.md)

## Troubleshooting

### Engram tools not available
```
1. Check: /mcp list
2. Ensure Engram is running: ./run-local.sh
3. Restart OpenCode
```

### Slow responses
- Increase timeout in config: `"timeout": 30000`
- Check Ollama is responsive: `curl http://localhost:11434/`

### Memory not being stored
- Verify Postgres is running: `docker ps | grep postgres`
- Check user_id is provided
- Review logs: `tail -f ~/.local/share/engram/logs/engram.log`

## Tips

1. **Use meaningful user_id** - helps group related memories
2. **Add multiple tags** - enables better retrieval
3. **Reference in context** - mention when retrieving: "based on your previous work..."
4. **Store decisions** - capture why something was chosen

---

For more on Engram architecture, see [README.md](README.md)
