#!/usr/bin/env bash
# uninstall-daemon.sh — Remove the Engram launchd service.
#
# What this does:
#   1. Unloads the launchd service (stops the running binary)
#   2. Removes the plist from ~/Library/LaunchAgents/
#   3. Optionally stops the Docker backend stack (Postgres, Qdrant, Neo4j)
#
# Usage:
#   make daemon-uninstall
#   # or directly:
#   bash scripts/uninstall-daemon.sh
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
PLIST_DST="$HOME/Library/LaunchAgents/ai.engram.plist"
COMPOSE="$REPO/deploy/docker-compose.yml"

# Unload and remove the launchd plist (|| true so it's safe if never loaded)
launchctl unload "$PLIST_DST" 2>/dev/null || true
rm -f "$PLIST_DST"

# Optionally stop the Docker backend stack
read -rp "Stop Docker backend stack too? [y/N] " answer
case $answer in
  y|Y) docker compose -f "$COMPOSE" down ;;
esac

echo "Engram daemon removed."
