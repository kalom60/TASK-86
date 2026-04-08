.PHONY: build run docker-build docker-up docker-down docker-logs test

build:
	CGO_ENABLED=1 go build -o bin/server ./cmd/server

run:
	go run ./cmd/server

docker-build:
	docker compose build

docker-up:
	docker compose up -d

docker-dev:
	docker compose -f docker-compose.yml -f docker-compose.dev.yml up

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f portal

test:
	go test ./...

test-verbose:
	go test -v ./...

lint:
	go vet ./...
