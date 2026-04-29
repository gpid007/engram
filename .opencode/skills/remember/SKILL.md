---
name: remember
description: Immediately store one or more facts into Engram memory. Splits people, relationships, and facts into separate store_memory calls.
compatibility: opencode
---

# remember

Store facts into Engram now. No confirmation needed.

## Rules

- One `store_memory` call per distinct fact or entity
- Never batch unrelated facts together
- Always `user_id: "greg"`

## People — always split

User says: _"Zita ist mit Karim zusammen. Karim ist am 12.12.1981 geboren. Zita ist am 25.7.1985 geboren."_

```
store_memory("greg", "Zita, born 1985-07-25",       { tags: ["people", "person", "zita"],                    source: "conversation" })
store_memory("greg", "Karim, born 1981-12-12",      { tags: ["people", "person", "karim"],                   source: "conversation" })
store_memory("greg", "Zita and Karim are a couple", { tags: ["people", "relationship", "zita", "karim"],     source: "conversation" })
```

## Tag cheatsheet

| What | Tags |
|------|------|
| Person | `["people", "person", "<name>"]` |
| Relationship | `["people", "relationship", "<name1>", "<name2>"]` |
| Language pref | `["preferences", "languages", "<lang>"]` |
| Framework pref | `["preferences", "frameworks", "<name>"]` |
| Pattern | `["patterns", "<lang>", "<type>"]` |
| Architecture decision | `["architecture", "decisions"]` |
| Workaround | `["workarounds", "<tech>"]` |

## Signature

```
store_memory(
  user_id:  "greg",
  content:  "<fact>",
  metadata: { tags: string[], source: "conversation" | "code-review" | "decision" }
)
```

## CLI alternative (when MCP tools unavailable)

If `store_memory` is not in your tool list, use the `engram` binary directly:

```bash
engram -config /Users/greg/git/engram/engram.local.yaml put "<raw text>"
```

The binary calls a local Ollama model to split and tag automatically.
Falls back to storing raw text as a single fact if Ollama is unavailable.
