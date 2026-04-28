# syntax=docker/dockerfile:1

# ─── Stage 1: Runtime (use pre-built binary) ───────────────────────────────
# The engram binary is built locally on the host with full ONNX support.
# We package it into the container with necessary runtime libraries.
FROM gcr.io/distroless/cc-debian12 AS runtime

# Copy the pre-built ONNX-enabled binary from the host.
COPY bin/engram /engram

# Copy the default config.
COPY engram.yaml /engram.yaml

# Install onnxruntime shared library for runtime (v1.20.1 matches go.mod v1.28.0).
# Note: distroless images don't have apt, so we copy the lib from a builder stage.
FROM debian:12 AS onnx-downloader
RUN apt-get update && apt-get install -y --no-install-recommends wget ca-certificates && \
    wget -q "https://github.com/microsoft/onnxruntime/releases/download/v1.20.1/onnxruntime-linux-x64-1.20.1.tgz" \
        -O /tmp/ort.tgz && \
    tar -xzf /tmp/ort.tgz -C /tmp && \
    ls -la /tmp/onnxruntime-linux-x64-1.20.1/lib/

FROM runtime
COPY --from=onnx-downloader /tmp/onnxruntime-linux-x64-1.20.1/lib/libonnxruntime.so* /lib/
# Ensure LD_LIBRARY_PATH includes /lib for runtime linking.
ENV LD_LIBRARY_PATH=/lib:${LD_LIBRARY_PATH}

ENTRYPOINT ["/engram"]
CMD ["--config", "/engram.yaml"]
