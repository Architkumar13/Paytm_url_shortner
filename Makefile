.PHONY: run test test-race cover vet fmt build docker-up docker-down tidy help

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-12s %s\n", $$1, $$2}'

run: ## Run locally (in-memory store; no Postgres needed)
	go run ./cmd/server

test: ## Run the unit test suite
	go test ./...

test-race: ## Run tests with the race detector
	go test -race ./...

cover: ## Run tests and print total coverage
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

vet: ## Run go vet
	go vet ./...

fmt: ## Format all Go files
	gofmt -w .

build: ## Build a static server binary into bin/
	CGO_ENABLED=0 go build -trimpath -o bin/server ./cmd/server

docker-up: ## Build and start app + Postgres via docker compose
	docker compose up --build

docker-down: ## Stop containers and remove volumes
	docker compose down -v

tidy: ## Tidy go.mod / go.sum
	go mod tidy
