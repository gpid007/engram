# Changelog

All notable changes to Engram are documented here. Format based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

## [0.2.0] - 2026-04-29

### Added
- **CLI commands for memory management**
  - `engram add` — Store memories from text, files, or directories with progress logging
    - `-f, --file` — Ingest single `.txt` or `.md` file
    - `-d, --dir` — Recursively ingest all text/markdown files from a directory
    - `--dry-run` — Preview stored content without persisting
  - `engram find` — Query memories with semantic search, includes ID column for deletion
    - `-k` — Configurable result count (default: 5)
  - `engram rm` / `engram remove` — Delete memories by ID or semantic query
    - `-q, --query` — Find and delete via semantic search
    - `--force` — Skip confirmation prompt
    - `--dry-run` — Preview deletions without executing
  - `engram status` — Display server statistics and connection info
- File ingest helpers: `SplitParagraphs`, `ReadFileText` for batch file processing
- Progress logging during file and directory ingestion
- HTTP DELETE endpoint: `DELETE /v1/memories/:id` with proper error handling (204/404/500)
- Transactional deletion in Postgres and best-effort deletion in Qdrant

### Fixed
- Hydrate content for vector-only retrieval results
- Dynamic sequence length in ONNX embedder
- PascalCase JSON handling in CLI client
- Recall skill explicit instruction to read content field from tool output

### Changed
- Binary size: 47MB with embedded ONNX embedder support

## [0.1.0] - 2026-04-15

### Added
- MCP (Model Context Protocol) support with three core tools:
  - `store_memory` — Persist memories with optional metadata
  - `retrieve_context` — Hybrid retrieval (vector + BM25 + reranking)
  - `get_user_state` — Query memory statistics
- HTTP/JSON API for REST clients
- Local ONNX inference support for embeddings (2–5ms per embedding)
- Hybrid retrieval: Qdrant (vector search) + Postgres (BM25 + metadata)
- Pluggable rerankers: LLM-based, cross-encoder, or remote API
- Neo4j graph store for relationship tracking (optional)
- Docker Compose stack: Postgres, Qdrant, Neo4j
- ONNX embedder extension with local nomic-embed-text-v1.5 model
- CLI daemon via launchd for macOS background execution
- `ENGRAM_CONFIG` environment variable for config path
- OpenCode MCP integration with automatic skill setup
- Initial CLI subcommands: `put`, `get`, `status`

### Infrastructure
- Docker Compose orchestration with health checks
- Makefile with `build-onnx` target for local inference builds
- GitHub Actions CI integration
- Comprehensive README with quickstart and MCP setup
