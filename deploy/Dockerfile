# syntax=docker/dockerfile:1

# ─── Stage 1: Build the Rust tokenizer shim ────────────────────────────────
FROM rust:1-bookworm AS rust-builder
WORKDIR /rust

# Copy only the Rust source from the daulet/tokenizers vendored crate.
# The path inside the Go module vendor directory is:
#   vendor/github.com/daulet/tokenizers/tokenizers/
# We copy the whole vendor dir and build in-place.
COPY vendor/github.com/daulet/tokenizers/tokenizers/ ./
RUN cargo build --release 2>&1
# Output: target/release/libtokenizers.a (static) and/or libtokenizers.so (shared)

# ─── Stage 2: Build the Go binary ──────────────────────────────────────────
FROM golang:1.23-bookworm AS go-builder
WORKDIR /src

# Install onnxruntime shared library (required at link time for onnxruntime_go).
# Use the same version pinned in go.mod: v1.28.0
ARG ONNXRUNTIME_VERSION=1.20.1
RUN apt-get update && apt-get install -y --no-install-recommends wget && \
    wget -q "https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}/onnxruntime-linux-x64-${ONNXRUNTIME_VERSION}.tgz" \
        -O /tmp/ort.tgz && \
    tar -xzf /tmp/ort.tgz -C /usr/local/lib --strip-components=2 \
        "onnxruntime-linux-x64-${ONNXRUNTIME_VERSION}/lib/" && \
    ldconfig && rm /tmp/ort.tgz

# Copy Rust static lib from Stage 1.
COPY --from=rust-builder /rust/target/release/libtokenizers.a /usr/local/lib/libtokenizers.a

# Copy Go source.
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Build default binary (no CGO, no onnxembed) — keeps the standard image small.
ARG BUILD_TAGS=""
RUN if [ -z "$BUILD_TAGS" ]; then \
        CGO_ENABLED=0 go build -ldflags="-s -w" -o /engram ./cmd/engram/; \
    else \
        CGO_ENABLED=1 \
        CGO_LDFLAGS="-L/usr/local/lib -ltokenizers -lonnxruntime" \
        go build -tags "$BUILD_TAGS" -ldflags="-s -w" -o /engram ./cmd/engram/; \
    fi

# ─── Stage 3: Runtime ──────────────────────────────────────────────────────
# Use cc-debian12 (includes libc) for CGO builds; static for default builds.
FROM gcr.io/distroless/cc-debian12 AS runtime
COPY --from=go-builder /engram /engram
COPY engram.yaml /engram.yaml

# Copy onnxruntime .so for runtime linking (only needed for onnxembed builds;
# harmless if present in non-onnxembed builds).
COPY --from=go-builder /usr/local/lib/libonnxruntime.so* /usr/local/lib/

ENTRYPOINT ["/engram"]
CMD ["--config", "/engram.yaml"]
