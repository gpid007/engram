#!/usr/bin/env bash
set -euo pipefail

MODEL_DIR="${MODEL_DIR:-/models}"
BGE_URL="https://huggingface.co/BAAI/bge-reranker-base/resolve/main/onnx/model.onnx"
BGE_PATH="${MODEL_DIR}/bge-reranker-base.onnx"

mkdir -p "${MODEL_DIR}"

if [ -f "${BGE_PATH}" ]; then
    echo "Model already exists at ${BGE_PATH}"
    exit 0
fi

echo "Downloading bge-reranker-base ONNX model..."
curl -L --progress-bar -o "${BGE_PATH}" "${BGE_URL}"
echo "Downloaded to ${BGE_PATH}"
