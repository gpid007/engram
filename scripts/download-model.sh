#!/usr/bin/env bash
# download-model.sh: fetch the embedding model from GitHub Releases
#
# The model is NOT stored in git. It is attached as a release asset on:
#   https://github.com/gregdhill/engram/releases
#
# Usage:
#   bash scripts/download-model.sh           # downloads latest tagged release
#   bash scripts/download-model.sh v0.2.0    # downloads specific version
#
# Requires: curl (or gh CLI for private repos)

set -e

REPO="gregdhill/engram"
MODEL_DIR="models/nomic-embed-text-v1.5"
TAG="${1:-latest}"

# Resolve "latest" to an actual tag via GitHub API
if [ "$TAG" = "latest" ]; then
  TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
  if [ -z "$TAG" ]; then
    echo "Could not resolve latest release tag. Check https://github.com/${REPO}/releases"
    exit 1
  fi
fi

echo "Downloading model for release ${TAG}..."

BASE_URL="https://github.com/${REPO}/releases/download/${TAG}"
mkdir -p "$MODEL_DIR"

# Download model.onnx
if [ ! -f "$MODEL_DIR/model.onnx" ]; then
  echo "  -> model.onnx"
  curl -fL --progress-bar \
    "${BASE_URL}/nomic-embed-text-v1.5-model.onnx" \
    -o "$MODEL_DIR/model.onnx"
else
  echo "  -> model.onnx already present, skipping"
fi

# Download tokenizer.json
if [ ! -f "$MODEL_DIR/tokenizer.json" ]; then
  echo "  -> tokenizer.json"
  curl -fL --progress-bar \
    "${BASE_URL}/nomic-embed-text-v1.5-tokenizer.json" \
    -o "$MODEL_DIR/tokenizer.json"
else
  echo "  -> tokenizer.json already present, skipping"
fi

echo "Model ready at $MODEL_DIR/"
