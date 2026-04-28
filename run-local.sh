#!/bin/bash
set -e

export DOCKER_HOST=unix://$HOME/.colima/default/docker.sock

# Neo4j credentials. Override in your shell to use a different password.
export NEO4J_PASSWORD="${NEO4J_PASSWORD:-engrampass}"

echo "Starting Engram locally..."
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

# Start Ollama container if not running
if ! docker ps --filter "name=ollama" --filter "status=running" | grep -q ollama; then
  echo "Starting Ollama..."
  docker run -d \
    --name ollama \
    -p 11434:11434 \
    -v ollama-data:/root/.ollama \
    --network deploy_default \
    ollama/ollama:latest
  
  # Wait for Ollama to be ready
  echo "Waiting for Ollama..."
  until curl -sf http://localhost:11434/ > /dev/null 2>&1; do
    sleep 2
  done
  
  # Pull required models
  echo "Pulling embeddings model (nomic-embed-text)..."
  docker exec ollama ollama pull nomic-embed-text > /dev/null 2>&1
  
  echo "Pulling LLM model (llama3.2:1b)..."
  docker exec ollama ollama pull llama3.2:1b > /dev/null 2>&1
  
  echo "✓ Ollama ready with models"
else
  echo "✓ Ollama already running"
fi

echo ""
echo "All services ready! Starting Engram binary..."
echo ""

# Run the Engram binary with local config
exec ./bin/engram -config engram.local.yaml
