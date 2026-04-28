.PHONY: build test lint up down

build:
	go build ./...

test:
	go test ./...

lint:
	go vet ./...

up:
	docker compose -f deploy/docker-compose.yml up -d

down:
	docker compose -f deploy/docker-compose.yml down
