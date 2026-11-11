# gantry

Lightweight Docker Compose manager. A headless Go **agent** (`agent/`) runs on
a Docker host and exposes an HTTP API; an optional **web UI** (`web/`, planned,
React + Vite) runs as a separate container and talks only to that API.

## Architecture rules

Settled decisions and their reasons are in `docs/decisions.md`; don't reopen
them without the user. In short:

- **API-first:** every feature is an `/api/v1/` endpoint first; the web UI
  only consumes the API. Keep the OpenAPI spec in sync once it exists.
- **One binary, two modes:** the agent runs as a systemd service (primary) and
  as a container (option). No container-only assumptions: paths come from
  configuration, never hardcoded (no fixed `/stacks`), and don't assume root.
- **Platforms:** linux/amd64 and linux/arm64, `CGO_ENABLED=0`, no cgo
  dependencies.
- **Standalone and publishable:** no organization-internal hosts, services or
  secrets; gantry must work without any other system.

## Commands

Run from the repo root:

- `make test` – Go tests (`cd agent && go test ./...`)
- `make lint` – gofmt check + `go vet` (CI fails on either)
- `make run` – agent on 127.0.0.1:7070, token `dev`
- `make image` – build the Docker image

## Git workflow

- GitHub Flow. `main` must always be releasable.
- **Never commit or push directly to `main`.** Create a branch
  (`feature/…`, `fix/…`, `chore/…`) and open a pull request.
- Releases are tags `vX.Y.Z` on `main`; CI publishes the images. Don't create
  tags unless asked.
- Commit messages, code, comments and docs in English.

## Code conventions

- Go standard library first; add a dependency only when it clearly pays off.
- HTTP routing with `net/http` method patterns (`"GET /api/v1/…"`), JSON via
  `encoding/json`, logging via `log/slog`.
- Configuration only through `GANTRY_*` environment variables, documented in
  the README table.
- Table-driven tests next to the code (`*_test.go`), using `httptest` for
  handlers.
- New API endpoints go under `/api/v1/` and are token-protected by default.

## Security

The agent will have access to the Docker socket, which is root-equivalent on
the host. Every `/api/` route must stay behind the bearer token, never log the
token, and keep the dev setup bound to 127.0.0.1.
