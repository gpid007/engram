# Next Steps

## Decision: Stay with Engram, take mem0's best idea

Do not integrate mem0. Do not abandon Engram. mem0 requires an external LLM for
extraction and has no native stdio MCP transport. Engram's local-first, zero-API-key
architecture is worth keeping.

The one genuinely superior idea in mem0: **LLM-based fact extraction** — sending raw
text through a local Ollama model to extract atomic facts before storage. This produces
a cleaner, more queryable memory store than verbatim chunking over time.

This is being added to Engram as an optional ingestion mode (see roadmap below).

---

## Roadmap (priority order)

### P0 — Fix before trusting data

**1. Implement reconciler retryFn** (`cmd/engram/main.go`)

The retry loop exists but does nothing (logged as a TODO). Failed Qdrant upserts
are queued as pending rows in Postgres. The reconciler should:
- Drain pending rows periodically
- Re-embed each chunk (call embedder)
- Re-upsert to Qdrant
- Call `meta.DeletePending(ctx, chunkID)` on success

Until this is fixed, Qdrant restarts or transient failures cause silent data loss
from vector search (memories survive in Postgres BM25 only).

**2. Memory conflict detection** (new feature)

After extraction, query existing memories for semantic near-duplicates. If a new fact
contradicts an existing one (cosine similarity > 0.92, opposing polarity), surface
the conflict to the caller rather than silently appending both. This prevents the
`"I prefer Postgres"` + `"I switched to CockroachDB"` problem of accumulating
contradictory memories.

---

### P1 — Quality of life

**3. Optional LLM fact extraction** (implemented in this branch)

Enable via config:

```yaml
extraction:
  enabled: true
  model: "llama3.2:1b"
  ollama_addr: "http://localhost:11434"
```

Or per-call via `write_memory` with `extract_mode: true`.

When enabled, raw text is sent to a local Ollama model which returns a JSON array
of atomic facts. Each fact is stored as a separate memory. Fails open — if extraction
fails, raw content is stored unchanged.

**4. Pre-built release binaries with ONNX**

Current setup requires building ONNX runtime and tokenizers FFI from source. This
is the single biggest barrier to adoption. Package the ONNX shared library into
GitHub Release assets alongside the binary. Extend `scripts/download-model.sh` to
also fetch the correct binary for the current platform.

**5. Memory management TUI/CLI**

```bash
engram list                # paginated view: date, source, preview
engram edit <id>           # open in $EDITOR
engram stats               # per-source, per-tag breakdowns
engram export              # dump all memories as JSON or markdown
```

The `erase_memory`/`forget` MCP tool exists but there is no way to browse what is
stored. A developer cannot audit or prune their memory without querying the HTTP API
directly.

---

### P2 — Architecture

**6. Temporal invalidation (Zep/Graphiti approach)**

When conflict detection (P0 #2) identifies a contradiction, mark the older memory
as superseded with an `invalidated_at` timestamp rather than leaving both. Add
`valid_until` column to Postgres memories table. Retrieval pipeline should filter
out invalidated memories by default, with an opt-in flag to include them.

**7. Enable graph writes by default**

`write_similar: false` in the default config disables graph population entirely.
The Neo4j integration exists and works but most users will never discover it.
Enable writes at a conservative threshold (0.85), document the Neo4j requirement
clearly, provide a `--no-graph` flag for users who do not want the dependency.

**8. Extraction quality benchmarks**

Run the retrieval pipeline against LoCoMo or LongMemEval to produce a number
comparable to mem0's published 91.6. Without a benchmark, quality claims are
unverifiable and the project cannot be compared objectively to alternatives.

**9. Multi-user auth hardening**

The `user_id` field is present throughout but not enforced. The HTTP API has no
authentication. Add an optional bearer token per `user_id` in config, enforced in
the HTTP middleware. Low priority for single-developer local use; required for any
team deployment.

---

### P3 — Ecosystem

**10. Published docs site**

Docusaurus or plain GitHub Pages, auto-deployed from `main`. The current README
is comprehensive but not searchable or navigable as a reference. A docs site
makes the project credible to external evaluators.

**11. VS Code / Cursor extension**

A thin extension that:
- Calls `read_memory` on workspace open (surfaces relevant context)
- Offers a command palette action to `write_memory` from selected text
- Calls `write_memory` at session end with a summary of what was worked on

This completes the loop: memory in, memory out, without leaving the editor.

**12. mem0 extraction benchmark comparison**

Once LLM extraction is stable and benchmarks exist, publish a side-by-side
comparison of Engram vs mem0 on LoCoMo. This is the clearest way to demonstrate
whether the local pipeline is competitive with mem0's cloud-optimised approach.

---

## What mem0 does that Engram does not (honest gap list)

| Feature                     | mem0         | Engram       | Priority |
| --------------------------- | ------------ | ------------ | -------- |
| LLM fact extraction         | Yes (default)| Optional (P1)| P1 ✓ done|
| Conflict/dedup detection    | Yes          | No           | P0       |
| Temporal invalidation       | Partial      | No           | P2       |
| Memory management UI        | Yes          | No           | P1       |
| Published benchmarks        | Yes (91.6)   | No           | P2       |
| Pre-built binaries          | Yes          | No           | P1       |
| Python/TS SDK               | Yes          | No           | P3       |
| Multi-user auth             | Yes          | Partial      | P2       |

## What Engram does that mem0 does not

| Feature                     | Engram            | mem0              |
| --------------------------- | ----------------- | ----------------- |
| Zero external API keys      | Yes               | No (OpenAI default)|
| Local cross-encoder rerank  | Yes (BGE ONNX)    | Platform only     |
| MCP stdio transport         | Yes               | HTTP/cloud only   |
| Graph expansion in retrieval| Yes (Neo4j)       | Platform only     |
| Single Go binary            | Yes               | Python stack      |
| RRF fusion                  | Yes               | Yes (new algo)    |
| Semantic chunking           | Yes               | No (fact extract) |
