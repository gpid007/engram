#!/usr/bin/env bash
# fetch-models.sh — downloads nomic-embed-text-v1.5 ONNX model files
# from HuggingFace into MODEL_DIR (default: /models/nomic-embed-text-v1.5).
#
# Uses -z (time-conditional) so existing up-to-date files are not re-downloaded.
# Set HF_REVISION to a pinned commit SHA for reproducibility.
#
# Usage:
#   MODEL_DIR=/models/nomic-embed-text-v1.5 HF_REVISION=<sha> bash scripts/fetch-models.sh
set -euo pipefail

MODEL_DIR="${MODEL_DIR:-/models/nomic-embed-text-v1.5}"
HF_REPO="nomic-ai/nomic-embed-text-v1.5"
HF_REVISION="${HF_REVISION:-16999335555c8808544a0344d2d4d9834ba70404}"

if [ "$HF_REVISION" = "PLACEHOLDER_PIN_ME" ]; then
    echo "ERROR: HF_REVISION must be set to a pinned commit SHA" >&2
    exit 1
fi

echo "Fetching nomic-embed-text-v1.5 into ${MODEL_DIR} @ ${HF_REVISION}"
mkdir -p "$MODEL_DIR"

# -f: fail on HTTP errors
# -L: follow redirects (HuggingFace uses LFS redirect)
# -z: skip download if local file is newer than remote
# --retry 3: transient network failures
# ?download=true: required for HuggingFace LFS files
curl -fL --retry 3 \
    -z "${MODEL_DIR}/model.onnx" \
    -o "${MODEL_DIR}/model.onnx" \
    "https://huggingface.co/${HF_REPO}/resolve/${HF_REVISION}/onnx/model.onnx?download=true"

curl -fL --retry 3 \
    -z "${MODEL_DIR}/tokenizer.json" \
    -o "${MODEL_DIR}/tokenizer.json" \
    "https://huggingface.co/${HF_REPO}/resolve/${HF_REVISION}/tokenizer.json?download=true"

echo "Done. Model files in ${MODEL_DIR}:"
ls -lh "${MODEL_DIR}"
