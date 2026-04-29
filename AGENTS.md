# Engram - Agent Rules

You have access to Engram MCP tools: `write_memory`, `read_memory`, `user_state`.
**Always use `user_id: "greg"`.**

---

## Act immediately — no permission needed

When the user provides facts, store them **right away** without asking. One `write_memory` call per distinct entity or fact. Never batch unrelated facts into a single memory.

### Storing people and relationships

Split every person and relationship into separate calls:

| Fact | content | tags |
|------|---------|------|
| Person | `"Zita, born 1985-07-25"` | `["people", "person", "zita"]` |
| Person | `"Karim, born 1981-12-12"` | `["people", "person", "karim"]` |
| Relationship | `"Zita and Karim are a couple"` | `["people", "relationship", "zita", "karim"]` |

### Store when you see

- A person's name, birthday, role, or relationship
- A preference: language, framework, tool, pattern
- An architecture or technology decision and its reasoning
- A workaround or solution to a recurring problem

### Retrieve before you

- Recommend a framework, library, or pattern
- Make an architecture decision
- Answer anything about people, dates, or past decisions

---

## Tool signatures

```
write_memory(user_id, content, metadata: { tags: string[], source: string })
read_memory(user_id, query, k=5, rerank=true)
user_state(user_id)

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

---

## Starting a new session

```
user_state("greg")                            # orientation
read_memory("greg", "recent decisions")        # what was last decided

---

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| Tools not available | `ctrl+p` → restart MCP server, or restart OpenCode |
| Store fails | `docker ps` — check Postgres/Qdrant are up |
| Empty results | Run `bash scripts/seed-memories.sh` |
