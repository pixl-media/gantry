STACKS_DIR ?= $(CURDIR)/dev/stacks
# Local development reads GANTRY_TOKEN (API) and GANTRY_UI_TOKEN (web login)
# from .env (git-ignored), which `make run` creates or completes with random
# tokens; docker compose and api.http use it too. Tokens set in the
# environment win.
dev_token = $${GANTRY_TOKEN:-$$(sed -n 's/^GANTRY_TOKEN=//p' $(CURDIR)/.env)}
dev_ui_token = $${GANTRY_UI_TOKEN:-$$(sed -n 's/^GANTRY_UI_TOKEN=//p' $(CURDIR)/.env)}

# The version `make build` puts into the binary.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# Release tooling, at fixed versions (the CI uses the same).
GORELEASER_IMAGE = goreleaser/goreleaser:v2.20.2@sha256:5d30bb36091b1a6d275ef450be05ee9c0d8c8b22955efe53dfe0974dd583cdc2
GIT_CLIFF_VERSION = 2.13.1

.PHONY: run dev-env docs test test-integration lint generate build image \
	release-snapshot changelog \
	web web-install web-generate web-test web-lint web-build web-image

run: dev-env ## Run the agent locally on 127.0.0.1:7070, managing dev/stacks (tokens in .env)
	cd agent && GANTRY_TOKEN="$(dev_token)" GANTRY_UI_TOKEN="$(dev_ui_token)" GANTRY_STACKS_DIR=$(STACKS_DIR) GANTRY_LISTEN=127.0.0.1:7070 go run ./cmd/gantry-agent

dev-env: ## Create .env with random tokens, or add a missing one
	@[ -f .env ] || (umask 077 && touch .env)
	@for v in GANTRY_TOKEN GANTRY_UI_TOKEN; do \
		grep -q "^$$v=" .env && continue; \
		[ -z "$$(tail -c1 .env)" ] || echo >> .env; \
		echo "$$v=$$(openssl rand -hex 24)" >> .env; \
		echo "Added a random $$v to .env."; \
	done

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
	cd agent && CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=$(VERSION)" -o bin/gantry-agent ./cmd/gantry-agent

release-snapshot: ## Build release binaries and .deb/.rpm packages into dist/ (Docker), without releasing
	docker run --rm -v $(CURDIR):/src -w /src --entrypoint sh $(GORELEASER_IMAGE) \
		-c 'git config --global --add safe.directory /src && goreleaser release --snapshot --clean'

changelog: ## Write CHANGELOG.md for the release TAG (make changelog TAG=v0.1.0)
	@test -n "$(TAG)" || { echo "usage: make changelog TAG=vX.Y.Z"; exit 1; }
	npx --yes git-cliff@$(GIT_CLIFF_VERSION) --tag $(TAG) -o CHANGELOG.md

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
	cd web && GANTRY_VERSION=$(VERSION) pnpm build

web-image: ## Build the web UI Docker image locally
	docker build --build-arg VERSION=$(VERSION) -t gantry-web:dev web
