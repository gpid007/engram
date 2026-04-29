# Global Agent Rules for Engram

Copy this file to `~/.config/opencode/AGENTS.md` to enable Engram memory in every
OpenCode session, regardless of which project or model you are using.

```bash
cp docs/global-agents.md ~/.config/opencode/AGENTS.md
```

---

## Engram Memory — always active

You have access to Engram MCP tools: `store_memory`, `retrieve_context`, `get_user_state`.
**Always use `user_id: "greg"`.**

### On session start — do this automatically

Call these two tools at the start of every new session without being asked:

```
get_user_state("greg")                          # how many memories, top sources
retrieve_context("greg", "recent decisions", 5) # what was last worked on
```

Use the results to orient yourself before responding to the user's first message.

### Store immediately — no permission needed

When the user provides any fact, store it right away. One call per distinct entity or fact.

**People — always split into separate calls:**

| Fact | content | tags |
|------|---------|------|
| Person | `"Zita, born 1985-07-25"` | `["people", "person", "zita"]` |
| Person | `"Karim, born 1981-12-12"` | `["people", "person", "karim"]` |
| Relationship | `"Zita and Karim are a couple"` | `["people", "relationship", "zita", "karim"]` |

**Store when you see:**
- A person's name, birthday, role, or relationship
- A preference: language, framework, tool, pattern
- An architecture or technology decision and its reasoning
- A workaround or solution to a recurring problem

### Retrieve before you act

Call `retrieve_context` before:
- Recommending a framework, library, or pattern
- Making an architecture decision
- Answering anything about people, dates, or past decisions

### Tag taxonomy

| Category | Tags |
|----------|------|
| People | `["people", "person", "<name>"]` |
| Relationships | `["people", "relationship", "<name1>", "<name2>"]` |
| Language prefs | `["preferences", "languages", "<lang>"]` |
| Framework prefs | `["preferences", "frameworks", "<framework>"]` |
| Patterns | `["patterns", "<lang>", "<type>"]` |
| Architecture | `["architecture", "decisions"]` |
| Workarounds | `["workarounds", "<tech>"]` |

### Troubleshooting

| Symptom | Fix |
|---------|-----|
| Tools not available | `ctrl+p` → restart MCP server, or restart OpenCode |
| Store/retrieve fails | `docker ps` — check Postgres/Qdrant are up |
