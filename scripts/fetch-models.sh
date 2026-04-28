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
HF_REVISION="${HF_REVISION:-e9b6763023c676ca8431644204f50c2b100d9aab}"

if [ "$HF_REVISION" = "PLACEHOLDER_PIN_ME" ]; then
    echo "ERROR: HF_REVISION must be set to a pinned commit SHA" >&2
    exit 1
fi

echo "Fetching nomic-embed-text-v1.5 into ${MODEL_DIR} @ ${HF_REVISION}"
mkdir -p "$MODEL_DIR"

# -f: fail on HTTP errors
# -L: follow redirects (HuggingFace uses LFS redirect)
# -z: skip download if local file is newer than remote (only when file exists)
# --retry 3: transient network failures
# ?download=true: required for HuggingFace LFS files
_z_flag() { [ -f "$1" ] && echo "-z $1" || echo ""; }

curl -fL --retry 3 \
    $([ -f "${MODEL_DIR}/model.onnx" ] && echo "-z ${MODEL_DIR}/model.onnx") \
    -o "${MODEL_DIR}/model.onnx" \
    "https://huggingface.co/${HF_REPO}/resolve/${HF_REVISION}/onnx/model.onnx?download=true"

curl -fL --retry 3 \
    $([ -f "${MODEL_DIR}/tokenizer.json" ] && echo "-z ${MODEL_DIR}/tokenizer.json") \
    -o "${MODEL_DIR}/tokenizer.json" \
    "https://huggingface.co/${HF_REPO}/resolve/${HF_REVISION}/tokenizer.json?download=true"

echo "Done. Model files in ${MODEL_DIR}:"
ls -lh "${MODEL_DIR}"
