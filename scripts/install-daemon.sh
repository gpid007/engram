#!/usr/bin/env bash
# install-daemon.sh — Install Engram as a persistent macOS launchd service.
#
# What this does:
#   1. Copies deploy/ai.engram.plist to ~/Library/LaunchAgents/
#   2. Loads it with launchctl (auto-starts on login, restarts on crash)
#   3. Starts the Docker backend stack (Postgres, Qdrant, Neo4j)
#   4. Waits up to 30s for each backend to be ready
#
# Prerequisites:
#   - bin/engram binary must exist (run: make build-onnx)
#   - Docker Desktop must be running
#   - NEO4J_PASSWORD must be set in your environment or shell profile
#
# Usage:
#   make daemon-install
#   # or directly:
#   bash scripts/install-daemon.sh
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
BINARY="$REPO/bin/engram"
PLIST_SRC="$REPO/deploy/ai.engram.plist"
PLIST_DST="$HOME/Library/LaunchAgents/ai.engram.plist"
COMPOSE="$REPO/deploy/docker-compose.yml"
LOG_DIR="$HOME/Library/Logs/engram"

# Ensure the binary exists before proceeding
if [ ! -f "$BINARY" ]; then
  echo "Error: binary not found at $BINARY. Run 'make build-onnx' first."
  exit 1
fi

# Create log directory for stdout/stderr captured by launchd
mkdir -p "$LOG_DIR"

# Install and (re)load the launchd plist
cp "$PLIST_SRC" "$PLIST_DST"
launchctl unload "$PLIST_DST" 2>/dev/null || true  # unload if already running
launchctl load "$PLIST_DST"

# Start backend containers (idempotent — safe to run if already up)
docker compose -f "$COMPOSE" up -d postgres qdrant neo4j

# Wait for Postgres to accept connections (TCP check)
echo "Waiting for Postgres..."
for i in $(seq 1 30); do
  nc -z localhost 5432 2>/dev/null && break
  [ "$i" -eq 30 ] && echo "Error: Postgres did not become ready in 30s." && exit 1
  sleep 1
done

# Wait for Qdrant HTTP healthcheck
echo "Waiting for Qdrant..."
for i in $(seq 1 30); do
  curl -sf http://localhost:6333/healthz >/dev/null 2>&1 && break
  [ "$i" -eq 30 ] && echo "Error: Qdrant did not become ready in 30s." && exit 1
  sleep 1
done

# Symlink binary to ~/bin for PATH access
mkdir -p "$HOME/bin"
ln -sf "$BINARY" "$HOME/bin/engram"
if ! grep -q 'HOME/bin' "$HOME/.zshenv" 2>/dev/null; then
  echo 'export PATH="$HOME/bin:$PATH"' >> "$HOME/.zshenv"
fi

echo "Engram daemon installed. HTTP: http://localhost:8080"
echo "Binary available as: engram (restart shell if not found)"
echo "Logs: tail -f $LOG_DIR/engram.err.log"
