GANTRY_TOKEN ?= dev
STACKS_DIR ?= $(CURDIR)/dev/stacks

.PHONY: run docs test test-integration lint generate build image \
	web web-install web-generate web-test web-lint web-build web-image

run: ## Run the agent locally on 127.0.0.1:7070, managing dev/stacks
	cd agent && GANTRY_TOKEN=$(GANTRY_TOKEN) GANTRY_STACKS_DIR=$(STACKS_DIR) GANTRY_LISTEN=127.0.0.1:7070 go run ./cmd/gantry-agent

docs: ## Swagger UI for api/openapi.yaml on http://127.0.0.1:8082, forwarding requests to the agent on :7070
	cd agent && go run ./tools/devdocs -spec $(CURDIR)/api/openapi.yaml -listen 127.0.0.1:8082 -agent http://127.0.0.1:7070

test: ## Run unit tests (no Docker needed)
	cd agent && go test ./...

test-integration: ## Run unit and integration tests against the local Docker daemon
	cd agent && go test -tags integration -race -count=1 ./...

lint: ## Check formatting and vet (including integration tests)
	cd agent && test -z "$$(gofmt -l .)" && go vet ./... && go vet -tags integration ./...

generate: ## Regenerate Go code from api/openapi.yaml
	cd agent && go generate ./...

build: ## Build the agent binary into agent/bin/
	cd agent && CGO_ENABLED=0 go build -o bin/gantry-agent ./cmd/gantry-agent

image: ## Build the agent Docker image locally
	docker build -t gantry-agent:dev agent

web: ## Web UI dev server on http://127.0.0.1:5173, forwarding /api to the agent on :7070
	cd web && pnpm dev

web-install: ## Install the web UI's dependencies
	cd web && pnpm install

web-generate: ## Regenerate web/src/api/schema.gen.ts from api/openapi.yaml
	cd web && pnpm generate

web-test: ## Run the web UI's tests
	cd web && pnpm test

web-lint: ## ESLint, Prettier and typecheck for the web UI
	cd web && pnpm lint && pnpm typecheck

web-build: ## Build the web UI into web/dist
	cd web && pnpm build

web-image: ## Build the web UI Docker image locally
	docker build -t gantry-web:dev web
