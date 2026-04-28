#!/bin/bash
set -e

# start-stack.sh — Start Engram backend services via docker-compose
# Then run the local Engram binary separately (not in Docker)
#
# Usage:
#   bash start-stack.sh
#
# This starts Postgres, Qdrant, Neo4j, and downloads ONNX models.
# Engram binary is run locally as a separate process (not containerized),
# since the pre-built binary is darwin/arm64 and won't run in linux Docker containers.

cd "$(dirname "$0")"

echo "Starting Engram stack services (Postgres, Qdrant, Neo4j)..."
echo ""

# Start backend services (detached)
docker compose -f deploy/docker-compose.yml up -d postgres qdrant neo4j model-init

echo "Waiting for services to be ready..."
echo ""

# Wait for Postgres
echo "▸ Postgres..."
until docker exec deploy-postgres-1 pg_isready -U engram 2>/dev/null | grep "accepting connections" > /dev/null 2>&1; do
  sleep 1
done
echo "  ✓ Ready on localhost:5432"

# Wait for Qdrant
echo "▸ Qdrant..."
until curl -sf http://localhost:6333/ > /dev/null 2>&1; do
  sleep 1
done
echo "  ✓ Ready on localhost:6333"

# Wait for Neo4j
echo "▸ Neo4j..."
until curl -sf http://localhost:7474/ > /dev/null 2>&1; do
  sleep 1
done
echo "  ✓ Ready on localhost:7474"

# Wait for model download
echo "▸ ONNX models..."
while docker ps -a | grep "model-init" | grep -q "Up"; do
  sleep 1
done
echo "  ✓ Downloaded to models/nomic-embed-text-v1.5/"

echo ""
echo "=== All backend services ready ==="
echo ""
echo "Next: Run the Engram binary in a separate terminal:"
echo "  ./run-local.sh"
echo ""
echo "Or manually:"
echo "  ./bin/engram -config engram.local.yaml"
echo ""
echo "Then seed the knowledge-base:"
echo "  bash scripts/seed-memories.sh"
echo ""
echo "Health checks:"
echo "  curl http://localhost:8080/readyz      # Engram"
echo "  curl http://localhost:6333/           # Qdrant"
echo "  psql -h localhost -U engram -d engram -c 'SELECT 1'  # Postgres"
