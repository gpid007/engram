#!/usr/bin/env bash
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
PLIST_DST="$HOME/Library/LaunchAgents/ai.engram.plist"
COMPOSE="$REPO/deploy/docker-compose.yml"

launchctl unload "$PLIST_DST" 2>/dev/null || true
rm -f "$PLIST_DST"

read -rp "Stop Docker backend stack too? [y/N] " answer
case $answer in
  y|Y) docker compose -f "$COMPOSE" down ;;
esac

echo "Engram daemon removed."
