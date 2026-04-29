#!/usr/bin/env bash
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
BINARY="$REPO/bin/engram"
PLIST_SRC="$REPO/deploy/ai.engram.plist"
PLIST_DST="$HOME/Library/LaunchAgents/ai.engram.plist"
COMPOSE="$REPO/deploy/docker-compose.yml"
LOG_DIR="/usr/local/var/log/engram"

if [ ! -f "$BINARY" ]; then
  echo "Error: binary not found at $BINARY. Run 'make build-onnx' first."
  exit 1
fi

mkdir -p "$LOG_DIR"
cp "$PLIST_SRC" "$PLIST_DST"
launchctl unload "$PLIST_DST" 2>/dev/null || true
launchctl load "$PLIST_DST"

docker compose -f "$COMPOSE" up -d postgres qdrant neo4j

echo "Waiting for Postgres..."
for i in $(seq 1 30); do
  nc -z localhost 5432 2>/dev/null && break
  [ "$i" -eq 30 ] && echo "Error: Postgres did not become ready." && exit 1
  sleep 1
done

echo "Waiting for Qdrant..."
for i in $(seq 1 30); do
  curl -sf http://localhost:6333/healthz >/dev/null 2>&1 && break
  [ "$i" -eq 30 ] && echo "Error: Qdrant did not become ready." && exit 1
  sleep 1
done

echo "Engram daemon installed. HTTP: http://localhost:8080"
echo "Logs: tail -f $LOG_DIR/engram.err.log"
