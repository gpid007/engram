📦 HANDOFF: Memory Harness (Go, MCP, Hybrid RAG)
🎯 Objective

Design and implement a portable, MCP-native memory harness that enables LLMs to:

persist memory across sessions
retrieve context via hybrid search (vector + BM25)
rerank results for high relevance
run fully locally (using Ollama for embeddings/reranking)
🧭 Core Principles (DO NOT VIOLATE)
Keep the system thin
No heavy frameworks
No dynamic pipelines
Prefer explicit flows over abstraction
Separation of concerns
retrieval ≠ storage ≠ embedding ≠ ranking
Portability first
Must run via Docker Compose
No cloud dependencies
MCP-first interface
System is consumed via MCP tools (primary)
HTTP is secondary
Extensibility without complexity
Design for Neo4j, but do not require it
Use interfaces everywhere
🏗️ System Architecture
High-level
LLM Client (OpenCode)
        │
        ▼
MCP Server (Go)
        │
        ▼
Memory Harness (Go modules)
        │
   ┌────┼───────────────┐
   │    │               │
Qdrant  Postgres     Ollama
(vector) (BM25/meta) (embeddings + rerank)
🧩 Required Components
1. Embedding Layer
Provider: Ollama
Model: nomic-embed-text
Tasks:
Implement HTTP client
Add batching support
Add timeout + retry logic
2. Vector Store
DB: Qdrant
Tasks:
Implement:
Upsert(id, vector, payload)
Search(vector, k, filters)
Use:
payload filtering (user_id)
Ensure:
id consistency with Postgres
3. Metadata + BM25
DB: PostgreSQL
Tasks:
Schema:
memories table
tsvector column
Implement:
SaveMemory
SearchBM25
Add:
GIN index
trigger for tsvector
4. Hybrid Retrieval Layer
Tasks:
A. Vector search
retrieve top K (semantic)
B. BM25 search
retrieve top K (keyword)
C. Merge strategy
deduplicate by ID
normalize scores
combine scores
D. Reranking
call Ollama (LLM scoring)
rerank top ~20 results
5. Memory Ingestion Pipeline
Steps:
Normalize input
Chunk (configurable)
Embed
Store in:
Qdrant (vector)
Postgres (metadata)
Assign:
importance score (simple heuristic)
6. MCP Interface
Required tools:
store_memory(input, user_id)
retrieve_context(query, user_id, k)
get_user_state(user_id)
Requirements:
clean JSON responses
structured outputs (not plain text)
7. Config System
Must support:
embedding:
  provider: ollama
  model: nomic-embed-text

retrieval:
  vector_k: 20
  bm25_k: 20
  rerank_k: 20
  final_k: 5
8. Docker Setup
Must include:
Qdrant
Postgres
Ollama
Go service
Requirements:
single docker-compose up → system runs
no manual setup
⚙️ Critical Design Decisions (to elaborate deeply)
1. Score fusion strategy

Ask Claude to:

compare:
additive scoring
weighted scoring
reciprocal rank fusion
recommend one
2. Chunking strategy

Ask Claude to:

define:
chunk size
overlap
consider:
semantic vs fixed chunking
3. Reranking approach

Ask Claude to:

compare:
LLM scoring (Ollama)
cross-encoder models
define latency vs quality tradeoff
4. Memory lifecycle

Ask Claude to design:

memory decay
deduplication
summarization triggers
5. Query processing

Ask Claude to evaluate:

query expansion (LLM rewrite)
when to apply it
🧪 Testing Requirements

Claude should define:

unit tests:
embedding client
merge logic
integration tests:
full retrieval pipeline
load test:
concurrent MCP calls
📏 Non-Functional Requirements
Performance
<100ms retrieval (no rerank)
<1s with rerank
Memory
target: <3GB total system
Reliability
retries on embedding calls
graceful DB failure handling
🚫 Explicitly Avoid
LangChain / heavy frameworks
dynamic pipeline engines
agent orchestration
premature graph DB usage
🔮 Future Extensions (design hooks only)
Graph DB (Neo4j)
Multi-user isolation
Memory weighting in ranking
Session-aware retrieval
🧭 Deliverables expected from Claude

Ask Claude to return:

Refined architecture (with diagrams)
Concrete Go interfaces (final form)
Detailed retrieval algorithm
Score fusion formula (final choice)
Chunking + ingestion strategy
Failure handling strategy
Performance optimization plan
⚡ Final instruction to Claude

Optimize for:

clarity over abstraction
performance over flexibility
simplicity over completeness

This system must be understandable by a single engineer in one sitting.

🧠 Why this plan works

This forces Claude to:

go deep where it matters (retrieval + scoring)
not derail into frameworks
produce something you can actually build
