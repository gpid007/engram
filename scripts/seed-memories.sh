#!/usr/bin/env bash
# seed-memories.sh — Bootstrap Engram with project knowledge and preferences
# Run once after a clean DB or after wiping memories.
#
# Usage:
#   bash scripts/seed-memories.sh
#   ENGRAM_URL=http://localhost:8080 bash scripts/seed-memories.sh

set -euo pipefail

ENGRAM_URL="${ENGRAM_URL:-http://localhost:8080}"
USER_ID="${ENGRAM_USER_ID:-greg}"

store() {
  local content="$1"
  local tags="$2"
  local source="${3:-seed}"

  local body
  body=$(printf '{"content":%s,"user_id":"%s","source":"%s","metadata":{"tags":%s}}' \
    "$(echo "$content" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read().strip()))')" \
    "$USER_ID" \
    "$source" \
    "$tags")

  local resp
  resp=$(curl -sf -X POST "$ENGRAM_URL/v1/memories" \
    -H "Content-Type: application/json" \
    -d "$body" 2>&1) || {
    echo "  FAIL: $content" >&2
    echo "  Error: $resp" >&2
    return 1
  }

  local id
  id=$(echo "$resp" | python3 -c 'import json,sys; print(json.loads(sys.stdin.read()).get("memory_id","?")[:8])' 2>/dev/null || echo "?")
  echo "  stored [$id...]: ${content:0:60}"
}

echo "=== Seeding Engram knowledge-base ==="
echo "    URL:     $ENGRAM_URL"
echo "    user_id: $USER_ID"
echo ""

# Check Engram is reachable
curl -sf "$ENGRAM_URL/readyz" > /dev/null || {
  echo "ERROR: Engram not reachable at $ENGRAM_URL"
  echo "Run: docker compose -f deploy/docker-compose.yml up -d && ./run-local.sh"
  exit 1
}
echo "✓ Engram is ready"
echo ""

# --- Project architecture ---
echo "▸ Project: Engram architecture"

store \
  "Engram is a hybrid-retrieval memory server for LLMs. It combines Qdrant (dense vector search) and Postgres (BM25 full-text search), fuses results with Reciprocal Rank Fusion (RRF k=60), and optionally reranks with a cross-encoder. Serves via MCP stdio and HTTP API." \
  '["project","engram","architecture"]'

store \
  "Engram data model: memories table (parent doc), chunks table (semantic splits, 100-512 tokens, BM25 via tsvector), pending_vectors table (retry queue for Qdrant failures). Dedup by (user_id, sha256(content)). All operations use user_id='greg'." \
  '["project","engram","data-model"]'

store \
  "Engram retrieval pipeline: embed query (ONNX nomic-embed-text-v1.5, 768-dim) → parallel Qdrant cosine search + Postgres BM25 → RRF fusion with vector floor 0.25 → optional cross-encoder rerank (bge-reranker-base ONNX) → top-5 results." \
  '["project","engram","retrieval","architecture"]'

store \
  "Engram ingest pipeline: normalize → semantic chunk (sentence cosine boundary walk, centroid-based) → embed batch (ONNX) → tx insert memories+chunks → Qdrant upsert → on Qdrant failure: enqueue pending_vectors for background reconciler." \
  '["project","engram","ingest","architecture"]'

# --- ONNX build ---
echo ""
echo "▸ Build: ONNX setup"

store \
  "Engram ONNX build: requires Rust toolchain (daulet/tokenizers Rust crate via CGO). Build with: CGO_ENABLED=1 CGO_LDFLAGS='-L.tokenizers-build' go build -tags onnxembed -o bin/engram ./cmd/engram/. Or: make build-onnx. First build ~5min (Rust compile); subsequent builds <30s." \
  '["build","onnx","tooling","engram"]'

store \
  "ONNX build workaround: Go module cache is read-only, so cargo build must use CARGO_TARGET_DIR pointing to a writable directory (.tokenizers-build/ in repo root, gitignored). The Makefile libtokenizers target handles this automatically." \
  '["workarounds","onnx","build","rust","go"]'

store \
  "Engram ONNX model: nomic-embed-text-v1.5 from HuggingFace, SHA e9b6763023c676ca8431644204f50c2b100d9aab. Files: model.onnx (522MB) + tokenizer.json (695KB) in models/nomic-embed-text-v1.5/. Download: make build-onnx triggers fetch-models.sh." \
  '["project","engram","onnx","models"]'

store \
  "ONNX embedding performance: 2-5ms per query, 15-20ms for batch of 32 chunks. ~5-10x faster than Ollama HTTP. No network hop. ONNX Runtime is bundled via yalue/onnxruntime_go (no separate install needed on darwin/arm64)." \
  '["performance","onnx","embedding","engram"]'

# --- Stack and config ---
echo ""
echo "▸ Stack: services and config"

store \
  "Engram local stack: Qdrant (localhost:6334 gRPC, 6333 HTTP dashboard), Postgres (localhost:5432, user=engram, db=engram, pass=engram), Neo4j (optional, localhost:7687, NEO4J_PASSWORD=engrampass). Started with: docker compose -f deploy/docker-compose.yml up -d." \
  '["project","engram","stack","config"]'

store \
  "Engram config files: engram.local.yaml (active local config, uses ONNX provider, model_dir=models/nomic-embed-text-v1.5), engram.yaml (reference defaults, uses ollama provider). Run with: ./bin/engram -config engram.local.yaml." \
  '["project","engram","config"]'

store \
  "Engram OpenCode MCP config in ~/.config/opencode/opencode.json: type=local, command=[/Users/greg/git/engram/bin/engram, -config, /Users/greg/git/engram/engram.local.yaml], enabled=true, timeout=10000. Restart OpenCode to reload after binary changes." \
  '["project","engram","opencode","config","mcp"]'

store \
  "run-local.sh starts Engram for local dev: waits for Postgres+Qdrant (via docker compose), sets NEO4J_PASSWORD=engrampass, then execs ./bin/engram -config engram.local.yaml. Uses ONNX now — Ollama is not required." \
  '["project","engram","workflow"]'

# --- Known workarounds ---
echo ""
echo "▸ Known workarounds"

store \
  "HuggingFace model download: original SHA 16999335... was stale/404. Fixed to e9b6763... in fetch-models.sh. Also fixed curl -z flag bug: -z <file> only passed when file already exists on disk (avoids curl treating filename as date string on first download)." \
  '["workarounds","engram","models","huggingface"]'

store \
  "Neo4j requires NEO4J_PASSWORD env var set before Engram starts. If missing, Engram exits with config error at startup. Export: export NEO4J_PASSWORD=engrampass. This is set automatically in run-local.sh." \
  '["workarounds","engram","neo4j","config"]'

# --- Preferences ---
echo ""
echo "▸ Preferences: Greg"

store \
  "Greg prefers local-first tooling: ONNX over Ollama for embeddings, local Postgres/Qdrant over managed services, local LLMs where feasible. Avoids unnecessary network dependencies in the hot path." \
  '["preferences","greg","tooling","philosophy"]'

store \
  "Greg uses parallel agent execution with lo-coder-q4 (Qwen2.5 Coder 7B Q4) as the default workhorse agent for boilerplate and well-defined tasks. Claude Sonnet for architecture and complex reasoning. Agents run in parallel wherever dependency graph allows." \
  '["preferences","greg","agents","workflow"]'

store \
  "Greg's repo: /Users/greg/git/engram. Go 1.26.2, darwin/arm64 (Apple Silicon). Docker via Colima (DOCKER_HOST=unix://~/.colima/default/docker.sock). Rust 1.95.0 installed at ~/.cargo." \
  '["preferences","greg","environment"]'

store \
  "Greg values concise, high-signal output. Prefers short answers that solve the task, minimal noise, no emojis unless asked. Plans should be detailed enough for agents to execute independently without follow-up questions." \
  '["preferences","greg","communication"]'

echo ""
echo "=== Seed complete ==="

# Count stored memories
state=$(curl -sf "$ENGRAM_URL/v1/users/$USER_ID/state" 2>/dev/null || echo '{}')
count=$(echo "$state" | python3 -c 'import json,sys; d=json.loads(sys.stdin.read()); print(d.get("memory_count",0))' 2>/dev/null || echo "?")
echo "Total memories for user '$USER_ID': $count"
echo ""
echo "Test retrieval:"
echo "  curl -s -X POST $ENGRAM_URL/v1/retrieve \\"
echo "    -H 'Content-Type: application/json' \\"
echo "    -d '{\"query\":\"ONNX build\",\"user_id\":\"$USER_ID\",\"k\":3}' | python3 -m json.tool"
