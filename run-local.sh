#!/bin/bash
set -e

export DOCKER_HOST=unix://$HOME/.colima/default/docker.sock

# Neo4j credentials. Override in your shell to use a different password.
export NEO4J_PASSWORD="${NEO4J_PASSWORD:-engrampass}"

# onnxruntime shared library — auto-detected by the binary, but can override here.
# export ENGRAM_EMBEDDING_LIB_PATH=/opt/homebrew/lib/libonnxruntime.dylib

echo "Starting Engram locally (ONNX embedding mode)..."
echo ""

# Check if Postgres is ready
echo "Waiting for Postgres..."
until docker exec deploy-postgres-1 pg_isready -U engram 2>/dev/null | grep "accepting connections" > /dev/null; do
  sleep 1
done
echo "✓ Postgres ready"

# Check if Qdrant is ready
echo "Waiting for Qdrant..."
until curl -sf http://localhost:6333/ > /dev/null 2>&1; do
  sleep 1
done
echo "✓ Qdrant ready"

# Verify ONNX binary exists
if [ ! -f "./bin/engram" ]; then
  echo "ERROR: bin/engram not found. Run: make build-onnx"
  exit 1
fi

# Verify model files exist
if [ ! -f "./models/nomic-embed-text-v1.5/model.onnx" ]; then
  echo "WARNING: ONNX model not found at models/nomic-embed-text-v1.5/model.onnx"
  echo "         Run: MODEL_DIR=models/nomic-embed-text-v1.5 bash scripts/fetch-models.sh"
  echo "         Or:  make build-onnx (downloads models automatically)"
fi

echo ""
echo "All services ready. Starting Engram with ONNX embeddings..."
echo ""

# Run the Engram binary with local config
exec ./bin/engram -config engram.local.yaml
