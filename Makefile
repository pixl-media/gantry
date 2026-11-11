GANTRY_TOKEN ?= dev

.PHONY: run test lint build image

run: ## Run the agent locally on 127.0.0.1:7070
	cd agent && GANTRY_TOKEN=$(GANTRY_TOKEN) GANTRY_LISTEN=127.0.0.1:7070 go run ./cmd/gantry-agent

test: ## Run all Go tests
	cd agent && go test ./...

lint: ## Check formatting and vet
	cd agent && test -z "$$(gofmt -l .)" && go vet ./...

build: ## Build the agent binary into agent/bin/
	cd agent && CGO_ENABLED=0 go build -o bin/gantry-agent ./cmd/gantry-agent

image: ## Build the agent Docker image locally
	docker build -t gantry-agent:dev agent
