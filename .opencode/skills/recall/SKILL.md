---
name: recall
description: Retrieve relevant memories from Engram for the current task or question. Surfaces past decisions, preferences, and facts.
compatibility: opencode
---

# recall

Retrieve memories from Engram and use them to answer or orient.

## When the user invokes this skill

1. Identify the topic from the user's message or current task
2. Call `read_memory` with that topic
3. Read the tool output — each result has a `content` field with the stored text
4. Quote the `content` values directly in your response — do NOT summarize or paraphrase
5. If the results array is empty or all content fields are empty strings, say so and suggest `engram get "<query>"` as a fallback
6. Never claim no memories exist if you haven't called the tool first

## Signature

```
read_memory(
  user_id: "greg",
  query:   "<topic or question>",   # natural language
  k:       5,                        # increase to 10 for broad topics
  rerank:  true
)
```

Tool output format — read the `content` field from each result:
```json
{
  "results": [
    { "content": "Zita, born 1985-07-25", "score": 0.91, "source": "conversation" },
    { "content": "Zita and Karim are a couple", "score": 0.87, "source": "conversation" }
  ]
}
```

**Always read and quote `result.content` in your response.**

## Common queries

| Goal | Query |
|------|-------|
| Session orientation | `"recent decisions"` |
| Person lookup | `"Zita"` / `"Karim"` |
| Language/framework prefs | `"language framework preferences"` |
| Architecture context | `"architecture decisions"` |
| Known workarounds | `"workarounds <tech>"` |
| Past patterns | `"<lang> error handling patterns"` |

## Orientation on session start

```
user_state("greg")                               # total memories, top sources
read_memory("greg", "recent decisions", 5)       # last worked on
```

## CLI alternative (when MCP tools unavailable)

If `read_memory` is not in your tool list, use the `engram` binary directly:

```bash
engram -config /Users/greg/git/engram/engram.local.yaml get "<query>"
engram -config /Users/greg/git/engram/engram.local.yaml status
# or if ENGRAM_CONFIG is set:
engram get "<query>"
engram status
```

## If results are empty

- Engram may not be running: `curl -s http://localhost:8080/healthz`
- Backends may be down: `docker ps`
- Restart daemon: `make daemon-install` in `/Users/greg/git/engram`
