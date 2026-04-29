---
name: recall
description: Retrieve relevant memories from Engram for the current task or question. Surfaces past decisions, preferences, and facts.
compatibility: opencode
---

# recall

Retrieve memories from Engram and use them to answer or orient.

## When the user invokes this skill

1. Identify the topic from the user's message or current task
2. Call `retrieve_context` with that topic
3. Surface relevant results directly in your response
4. If nothing useful comes back, say so — don't hallucinate

## Signature

```
retrieve_context(
  user_id: "greg",
  query:   "<topic or question>",   # natural language
  k:       5,                        # increase to 10 for broad topics
  rerank:  true
)
→ results[{ content, score, source, created_at }]
```

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
get_user_state("greg")                          # total memories, top sources
retrieve_context("greg", "recent decisions", 5) # last worked on
```

## CLI alternative (when MCP tools unavailable)

If `retrieve_context` is not in your tool list, use the `engram` binary directly:

```bash
engram -config /Users/greg/git/engram/engram.local.yaml get "<query>"
engram -config /Users/greg/git/engram/engram.local.yaml status
```

## If results are empty

- Engram may not be running: `curl -s http://localhost:8080/healthz`
- Backends may be down: `docker ps`
- Restart daemon: `make daemon-install` in `/Users/greg/git/engram`
