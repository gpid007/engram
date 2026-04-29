# Engram: A Local Memory Layer for AI Coding Assistants

**An honest technical assessment**

---

## Executive Summary

Engram is a self-hosted, fully local persistent memory system designed for AI coding assistants. It stores developer knowledge — architecture decisions, preferences, workarounds, context — and makes it retrievable across sessions via the Model Context Protocol (MCP). Everything runs on-device: embeddings, reranking, and retrieval. No external API keys required.

The core technical approach is a multi-stage retrieval pipeline that combines semantic chunking, hybrid search (BM25 + vector via Qdrant, fused with Reciprocal Rank Fusion), optional Neo4j graph expansion, and cross-encoder reranking — all in a single Go binary. This is meaningfully more sophisticated than the vector-only or keyword-only approaches common in competing tools.

The project is genuinely unique in two ways: it is currently the only self-hosted MCP server for persistent AI memory (the only prior competitor, OpenMemory by mem0, was sunset in 2025), and it is the only tool in this space that runs the full embedding and reranking pipeline locally without depending on any cloud service.

The honest caveats: it is a small project, not battle-tested at scale, has a stub reconciler, and requires building ONNX libraries locally. It is not for everyone. But for developers who care about data ownership, latency, and a real retrieval pipeline, it is the most capable option available today.

---

## The Problem: AI Assistants That Forget Everything

Every developer who uses an AI coding assistant has hit the same wall. You explain your architecture on Monday. By Tuesday the model has no idea what you said. You tell it you prefer table-driven tests in Go. Next week it generates the same pattern you just told it to avoid. You debug a subtle database connection issue for an hour, document the fix in the chat, and three weeks later you are debugging it again from scratch because the model that helped you last time has no memory of the conversation.

This is not a model capability problem. It is a memory architecture problem.

Current AI coding tools handle memory in one of three ways. The most common is no memory at all — the context window is the only state, and when the session ends, everything is gone. GitHub Copilot and most IDE assistants work this way. The second approach is codebase indexing: tools like Cursor index your project locally, giving the model awareness of your current codebase structure, but this is read-only and does not persist learned knowledge about you as a developer across projects or time. The third approach is cloud memory — Claude.ai can remember facts about you, but that memory is proprietary, opaque, tied to Anthropic's servers, and not accessible via any open interface.

None of these approaches solve the core problem: a developer's accumulated knowledge — preferences, patterns, workarounds, architectural decisions and their rationale — cannot be stored, retrieved, and fed to any model, across any tool, at any time.

---

## What Engram Actually Does

Engram is a memory daemon. You run it locally alongside your AI assistant. When you want to remember something — a decision, a preference, a fact about a person or project — you write it to Engram. When the AI assistant needs context at the start of a session, or before making an architectural decision, it reads from Engram. The storage, retrieval, and serving all happen on your machine.

The MCP (Model Context Protocol) interface is the integration point. MCP is an open standard, originally developed by Anthropic, that allows AI tools to expose capabilities to models via a defined protocol. Engram implements an MCP server over stdio — meaning any MCP-compatible client (OpenCode, Claude Desktop, or any other compliant tool) can connect to it as a subprocess with zero network overhead.

The tools it exposes are deliberately simple:

- `write_memory` — store a chunk of text with optional metadata
- `read_memory` — retrieve relevant memories for a query
- `user_state` — get aggregate statistics about stored memories
- `erase_memory` — delete a memory by ID

Each tool has multiple registered names (`remember`, `recall`, `status`, `forget`) to reduce friction with natural-language prompts.

What happens under the hood is more interesting than the interface suggests.

---

## The Retrieval Pipeline

Most memory tools for LLM agents are thin wrappers around a vector database. You embed text, store the vector, and at query time you find the nearest neighbours. This works well enough for simple cases and fails in predictable ways for complex ones: exact keyword matches are missed because they are semantically distant in embedding space, older memories are ranked below newer ones regardless of relevance, and there is no way to represent that two memories are related to each other.

Engram's retrieval pipeline has five stages.

**Stage 1: Semantic chunking.** When a memory is written, it is not stored as a single blob. The text is split into semantic chunks using a sliding-window approach with a cosine similarity threshold (default 0.6). Chunks that are semantically continuous stay together; topic shifts create new chunks. This preserves coherent context rather than splitting sentences arbitrarily by token count.

**Stage 2: Dual-index storage.** Each chunk is stored in two places simultaneously. The vector representation (nomic-embed-text-v1.5, 768 dimensions, ONNX runtime, fully local) goes into Qdrant. The raw text goes into Postgres with full-text indexing for BM25 retrieval.

**Stage 3: Parallel hybrid retrieval.** At query time, the system fires two searches in parallel: a vector search returning the top 20 results by cosine similarity, and a BM25 full-text search returning the top 20 results by keyword relevance. These two result sets frequently do not overlap — the vector search finds semantically similar content that may not share keywords with the query, while BM25 finds exact matches that may be semantically distant.

**Stage 4: RRF fusion.** The two ranked lists are merged using Reciprocal Rank Fusion (RRF-60), a well-established algorithm that rewards documents ranking well in both lists and tolerates differences in score magnitude between retrieval methods. This combined ranking is significantly more reliable than either method alone.

**Stage 5: Cross-encoder reranking.** The top results from RRF are then passed to a cross-encoder reranker (BGE-reranker-base, also running locally via ONNX). Unlike the bi-encoder embeddings used in vector search — which encode the query and document independently — a cross-encoder sees both the query and the candidate document simultaneously, producing a relevance score that accounts for their interaction. This is slower but dramatically more accurate. The final output is the top 5 results after reranking.

Optionally, between stages 4 and 5, a graph expansion step queries Neo4j to find memories connected to the retrieved results by entity relationships. This allows the system to surface indirectly related memories — for example, if you asked about a deployment issue and there is a memory connecting that service to a particular database configuration decision, graph expansion can bring it in.

This pipeline is not unique to Engram in concept — Zep's Graphiti and enterprise search systems use similar approaches. What is unusual is that every stage of this pipeline runs entirely locally, in a single binary, with no external service dependencies beyond the three local databases (Qdrant, Postgres, Neo4j optional).

---

## The Competitive Landscape

### mem0

mem0 is the most popular open-source memory layer for LLM agents, with over 54,000 GitHub stars. It supports hybrid search combining vector, BM25, and entity-based retrieval. It is genuinely good at what it does.

The limitations are practical. By default it requires an OpenAI API key for both the extraction LLM and the embedding model. The self-hosted version can be configured to use Ollama for local models, but the extraction step — where an LLM reads your raw text and decides what facts to store — still requires a capable model, and running that locally adds substantial overhead. More fundamentally, mem0's approach is ADD-only extraction: an LLM reads each input and extracts atomic facts to store. This is powerful for structured fact extraction but loses the original context and introduces a dependency on extraction quality. Memories accumulate without a pruning or revision strategy.

There is no native MCP support. There is no graph memory. And at roughly one second p50 latency for retrieval, the overhead of the extraction pipeline is noticeable.

### Zep

Zep is technically the most sophisticated project in this space. Its Graphiti framework implements a temporal knowledge graph that tracks not just facts but how those facts change over time — entities have `valid_at` and `invalid_at` timestamps, so the system knows when a fact was superseded. This is genuinely impressive and solves a problem that pure vector memory cannot.

The problem is that Zep deprecated its self-hosted Community Edition. The full platform is now cloud-only. If your priority is data ownership, Zep is no longer an option. The Graphiti library itself remains open source, but operating it independently requires significant integration work.

Zep also claims sub-200ms p95 retrieval latency from its cloud service. That is excellent for a managed cloud product. It is less impressive when compared to a local system where retrieval round-trips never leave the machine.

### Letta / MemGPT

Letta is the most research-oriented project in the space. Its ideas — continual learning in token space, background memory subagents, sleep-time compute — are genuinely novel. For building persistent agents that learn and evolve, it is the most ambitious option.

It is also the most complex. Running Letta locally requires substantially more setup than Engram. There is no MCP interface. The reconciler-style background processing is more sophisticated but also less transparent. For a developer who wants to plug memory into an existing coding assistant workflow, the overhead is high.

### OpenMemory (mem0) — Sunset

OpenMemory was the one direct competitor to Engram: a self-hosted MCP memory server built on mem0's stack. It had an MCP server, Docker Compose deployment, and local-first operation.

It was sunset in 2025. mem0 is redirecting users to their main self-hosted server, which does not include MCP support. This left a gap in the ecosystem that Engram currently occupies alone.

### Claude, Cursor, Copilot

These tools have no persistent memory interface accessible to external tools. Claude.ai remembers facts about you, but you cannot query that memory from a Go program, a CLI, or any other AI tool. It is proprietary, non-portable, and not a memory infrastructure component — it is a user-facing feature.

Cursor's codebase indexing is excellent for project context but is not a general memory layer. You cannot write to it, it does not persist decisions and preferences, and it does not cross projects.

---

## What Engram Does Well

**The pipeline depth.** The combination of semantic chunking, hybrid retrieval, RRF fusion, optional graph expansion, and local cross-encoder reranking in a single binary is the most complete local retrieval pipeline available in any open-source memory tool. Each stage addresses a real failure mode of the previous one.

**Full local operation.** Nothing leaves the machine. The nomic-embed-text-v1.5 model runs via ONNX runtime. The BGE-reranker runs via ONNX. The three data stores (Qdrant, Postgres, Neo4j) run in Docker. There are no API keys, no rate limits, no cloud bills, no data sharing. For developers working on proprietary systems, client work, or anything where context confidentiality matters, this is not a nice-to-have — it is a requirement.

**MCP stdio transport.** The `-mcp` flag starts Engram in stdio-only mode, making it a direct subprocess of the MCP client with no network overhead. The client connects over stdin/stdout, which is the lowest-latency MCP transport possible. There is no HTTP round-trip, no port to manage, no separate process to keep alive.

**The AGENTS.md pattern.** The project ships with a well-designed `AGENTS.md` that instructs any AI assistant to automatically call `user_state` and `read_memory` at the start of every session. This is a genuinely useful pattern that most memory tools do not have — they provide a storage API but leave the integration to the developer. Engram provides a working prompt engineering layer out of the box.

**Go, single binary.** The choice of Go means a statically compiled binary with no runtime dependencies beyond the ONNX shared library. Deployment is a binary and a config file. There is no Python environment to manage, no npm install, no dependency hell. This is underrated.

**HTTP API + Prometheus metrics.** When running in HTTP mode (alongside or instead of MCP), Engram exposes a clean REST API and Prometheus-compatible metrics at `/metrics`. For teams wanting to observe memory retrieval patterns, latency, and error rates, this is immediately useful.

---

## What Needs Improvement

Being honest about the gaps matters. This is not a polished product. It is a capable prototype with real rough edges.

**The reconciler is a stub.** When a vector upsert to Qdrant fails during ingestion, the chunk ID is queued in Postgres as a pending retry. The reconciler process is meant to drain this queue by re-embedding and re-upserting. Currently, the retry function does nothing — it logs a warning and returns nil. Under adverse conditions (Qdrant restart, network blip in containerised environments), memories can be silently lost from vector search while remaining in Postgres. This is a real data reliability issue that needs to be addressed before the project is used for anything important.

**Graph writes are disabled by default.** The Neo4j graph integration exists and works, but `write_similar: false` in the default config means the graph component is read-only during retrieval expansion. It will not build a graph of your memories unless you opt in and tune the similarity threshold. Most users will never turn this on because the documentation does not explain the tradeoff clearly.

**ONNX build complexity.** To run with local embeddings (the whole point), you need to build the ONNX runtime and the tokenizers FFI library from source. This is documented but non-trivial. On a fresh machine it takes meaningful effort. Until there are pre-built release binaries with the ONNX libraries included, the barrier to entry is higher than it needs to be.

**No memory management UI.** There is no way to browse, search, or manage stored memories without using the CLI or directly querying the APIs. For a tool whose value proposition is accumulated developer knowledge, the ability to audit, correct, and prune that knowledge is important. Right now you can `engram find <query>` and `engram rm <id>` but there is no higher-level view.

**No pruning or revision strategy.** Memories accumulate. There is no automatic detection of contradicted or superseded memories. If you store "I prefer Postgres" in January and "I switched to CockroachDB" in March, both memories exist and will be retrieved. A conflict resolution or temporal invalidation strategy — something Zep's Graphiti handles well — is missing.

**Small project, no community.** There is no documentation site, no community forum, no issue tracker activity. The bus factor is one. This is not a criticism of the project's quality, but it is a real consideration for anyone evaluating it for a team or production use.

**Neo4j AGPL dependency.** Neo4j Community Edition is AGPL licensed. If you build a product on Engram with the graph component enabled, you need to be aware of the license implications. This is not a blocker for individual developer use, but it is worth flagging for commercial contexts.

---

## Who This Is For

Engram is the right tool for a developer who:

- Values data ownership and wants memory stored locally
- Uses an MCP-compatible AI assistant (OpenCode, Claude Desktop, or similar)
- Wants a retrieval pipeline that goes beyond naive vector search
- Is comfortable with Docker, Go toolchains, and some initial setup complexity
- Is willing to accept that the project is early-stage and has rough edges

It is the wrong tool for someone who:

- Wants a polished, supported product with a UI and an SLA
- Needs memory to work across cloud devices and mobile
- Is not comfortable running Qdrant, Postgres, and optionally Neo4j locally
- Needs the graph and reconciler features to be production-grade today

---

## The Bigger Picture

The problem Engram is solving is real and largely unsolved. The AI coding assistant ecosystem has invested enormous effort into making models smarter, context windows larger, and code generation more capable. Almost no effort has gone into persistent developer memory — the accumulated knowledge of how a specific developer thinks, what they have tried, what their systems look like, and what they have learned.

The closest analogy is the difference between hiring a brilliant generalist consultant every time you have a problem, versus working with someone who has been embedded in your team for a year. The consultant might be more capable in raw terms. But they do not know why that service was refactored six months ago, or why you chose that library over the alternative, or that the production database has a specific quirk that tripped you up twice. The embedded colleague does.

Engram is an attempt to build that embedded colleague's memory layer for AI tools. The retrieval pipeline is serious. The local-first architecture is principled. The MCP integration is correct. The gaps are real but fixable.

The fact that it currently occupies this space alone — after OpenMemory was sunset and Zep moved to cloud-only — is itself meaningful. There is a gap in the ecosystem for a self-hosted, MCP-native, full-pipeline memory layer. Engram fills it imperfectly but genuinely.

---

## Conclusion

Engram is not the most popular memory tool, the most polished, or the easiest to set up. It is, however, the most technically rigorous local-first option currently available, and the only self-hosted project with native MCP support.

The five-stage retrieval pipeline — semantic chunking, hybrid BM25+vector search, RRF fusion, graph expansion, cross-encoder reranking — represents a coherent and well-reasoned approach to the known failure modes of simpler memory systems. Running all of it locally, with no external API dependencies, is a meaningful differentiator in a market where most competitors either require cloud services or lean on OpenAI for the hard parts.

The project needs a working reconciler, a memory management interface, clearer documentation of the graph features, and pre-built binaries to lower the setup barrier. These are solvable problems.

For developers who care about owning their context, Engram is worth the setup effort. For everyone else, it is worth watching.

---

*Word count: approximately 2,800 words*
*Research conducted April 2026*
*Competitors referenced: mem0 v1.x, Zep Cloud, Letta/MemGPT, OpenMemory (sunset), Claude.ai memory, Cursor, GitHub Copilot*
