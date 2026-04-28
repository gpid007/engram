#!/usr/bin/env bash
set -euo pipefail

OLLAMA_HOST="${OLLAMA_HOST:-http://ollama:11434}"

echo "Waiting for Ollama at ${OLLAMA_HOST}..."
until curl -sf "${OLLAMA_HOST}/api/tags" > /dev/null 2>&1; do
  sleep 2
done
echo "Ollama is ready."

echo "Pulling nomic-embed-text..."
curl -sf -X POST "${OLLAMA_HOST}/api/pull" \
  -H "Content-Type: application/json" \
  -d '{"name":"nomic-embed-text"}' | tail -1

echo "Pulling llama3.2:1b..."
curl -sf -X POST "${OLLAMA_HOST}/api/pull" \
  -H "Content-Type: application/json" \
  -d '{"name":"llama3.2:1b"}' | tail -1

echo "Models ready."
