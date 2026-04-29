TOKENIZERS_BUILD_DIR ?= $(CURDIR)/.tokenizers-build
TOKENIZERS_MODULE    := $(shell go env GOPATH)/pkg/mod/github.com/daulet/tokenizers@$(shell go list -m -f '{{.Version}}' github.com/daulet/tokenizers 2>/dev/null)

# Ensure ~/.cargo/bin is on PATH for non-interactive shells (rustup installs here).
export PATH := $(HOME)/.cargo/bin:$(PATH)

.PHONY: build build-onnx libtokenizers test lint race coverage up down daemon-install daemon-uninstall

build:
	go build ./...

libtokenizers:
	@mkdir -p $(TOKENIZERS_BUILD_DIR)
	cd $(TOKENIZERS_MODULE) && CARGO_TARGET_DIR=$(TOKENIZERS_BUILD_DIR) cargo build --release -p tokenizers-ffi
	cp $(TOKENIZERS_BUILD_DIR)/release/libtokenizers_ffi.a $(TOKENIZERS_BUILD_DIR)/libtokenizers.a

build-onnx: libtokenizers
	CGO_ENABLED=1 CGO_LDFLAGS="-L$(TOKENIZERS_BUILD_DIR)" go build -tags onnxembed -o bin/engram ./cmd/engram/

test:
	go test ./...

lint:
	golangci-lint run ./...

race:
	go test -race ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

up:
	docker compose -f deploy/docker-compose.yml up -d

down:
	docker compose -f deploy/docker-compose.yml down

daemon-install:
	bash scripts/install-daemon.sh

daemon-uninstall:
	bash scripts/uninstall-daemon.sh
