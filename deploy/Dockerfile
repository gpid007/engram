# Stage 1: build
FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Default build (no crossenc). Add --build-arg TAGS=crossenc for ONNX variant.
ARG BUILD_TAGS=""
RUN CGO_ENABLED=0 go build ${BUILD_TAGS:+-tags $BUILD_TAGS} -ldflags="-s -w" -o /engram ./cmd/engram/

# Stage 2: runtime
FROM gcr.io/distroless/static:nonroot
COPY --from=builder /engram /engram
COPY engram.yaml /engram.yaml
ENTRYPOINT ["/engram"]
CMD ["--config", "/engram.yaml"]
