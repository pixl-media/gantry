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
  configuration; defaults must be documented host paths that work in both
  modes (`GANTRY_STACKS_DIR` defaults to `/opt/stacks`, like Dockge), never
  container-only paths like `/stacks`. Don't assume root.
- **Platforms:** linux/amd64 and linux/arm64, `CGO_ENABLED=0`, no cgo
  dependencies.
- **Standalone and publishable:** no organization-internal hosts, services or
  secrets; gantry must work without any other system.

## Commands

Run from the repo root:

- `make test` – unit tests, no Docker needed (`cd agent && go test ./...`)
- `make test-integration` – unit + integration tests against the local
  Docker daemon (build tag `integration`)
- `make lint` – gofmt check + `go vet`, also with the integration tag (CI fails
  on either)
- `make generate` – regenerate `agent/internal/api/api.gen.go` from
  `api/openapi.yaml` (CI fails if it's out of date)
- `make run` – agent on 127.0.0.1:7070, token `dev`, stacks dir `dev/stacks`
- `make docs` – dev-only Swagger UI on 127.0.0.1:8082, forwarding to the agent
  (`agent/tools/devdocs`, never part of the agent binary or image)
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
- **API changes start in `api/openapi.yaml`**, then `make generate`, then
  implement the generated `StrictServerInterface` in
  `agent/internal/api/handlers.go`. Never edit `*.gen.go` by hand.
- Package roles: `internal/docker` (Engine API client, stdlib only),
  `internal/compose` (`docker compose` CLI), `internal/stacks` (logic,
  depends on interfaces so tests use fakes), `internal/api` (HTTP wiring and
  mapping to generated types).
- Container state comes from the Engine API; anything that is Compose logic
  goes through the `docker compose` CLI (see `docs/decisions.md`).
- Run Compose only through `compose.CLI` (`Run` / `Services`), which always
  passes `-p`, `-f` and `--project-directory`. Never use `down -v` or any
  other volume removal.
- Anything that can take longer than a moment (Compose actions, pulls) runs
  as an operation (`internal/operations`), never synchronously inside a
  request. Actions only work on managed stacks (`stacks.Target`).
- Package roles also include `internal/operations` (async actions, one per
  stack, output buffer for live streams).
- Configuration through `GANTRY_*` environment variables (plus Docker's own
  `DOCKER_HOST`), documented in the README table.
- Table-driven tests next to the code (`*_test.go`), using `httptest` for
  handlers. Integration tests use the `integration` build tag, name their
  stacks `gantry-test-<random>-…`, clean up with `t.Cleanup`, and assert only
  on their own stacks.
- New API endpoints go under `/api/v1/`. The token middleware denies by
  default: only paths listed in `publicPaths` are public.

## Security

The agent has access to the Docker socket, which is root-equivalent on the
host. Every `/api/` route must stay behind the bearer token, never log the
token, and keep the dev setup bound to 127.0.0.1.

- **Never expose secrets from stacks:** return Compose files as stored, never
  the resolved configuration (`docker compose config` output) or `.env`
  contents. Read Compose data with `--no-interpolate`.
- Validate stack names with `stacks.ValidName` before using them in paths or
  commands.
