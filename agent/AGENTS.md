# gantry agent

The Go agent: HTTP API on a Docker host. General rules, the project map and
the security essentials are in the root `AGENTS.md`.

## Commands

Run from the repo root:

- `make test` – unit tests, no Docker needed (`cd agent && go test ./...`)
- `make test-integration` – unit + integration tests against the local
  Docker daemon (build tag `integration`)
- `make lint` – gofmt check + `go vet`, also with the integration tag (CI fails
  on either)
- `make generate` – regenerate `agent/internal/api/api.gen.go` from
  `api/openapi.yaml` (CI fails if it's out of date)
- `make run` – agent on 127.0.0.1:7070, stacks dir `dev/stacks`, token from
  `.env` (git-ignored, created with a random token on first run)
- `make docs` – dev-only Swagger UI on 127.0.0.1:8082, forwarding to the agent
  (`agent/tools/devdocs`, never part of the agent binary or image)
- `make image` – build the Docker image

## Code conventions

- Go standard library first; add a dependency only when it clearly pays off.
- **API changes start in `api/openapi.yaml`**, then `make generate`, then
  implement the generated `StrictServerInterface` in
  `agent/internal/api/handlers.go`. Never edit `*.gen.go` by hand.
- Package roles: `internal/docker` (Engine API client, stdlib only),
  `internal/compose` (`docker compose` CLI), `internal/stacks` (logic,
  depends on interfaces so tests use fakes), `internal/operations` (async
  actions, one per stack, output buffer for live streams), `internal/logs`
  (merged stack logs with re-attach after restarts), `internal/session`
  (in-memory sessions, login rate limit), `internal/stackfiles` (stack
  files: validated atomic writes, versions, layout-preserving `.env`),
  `internal/api` (HTTP wiring, auth and mapping to generated types).
- Container state comes from the Engine API; anything that is Compose logic
  goes through the `docker compose` CLI (see `docs/decisions.md`).
- Run Compose only through `compose.CLI` (`Run` / `Services` / `Validate`),
  which always passes `-p`, `-f` and `--project-directory` and removes
  `GANTRY_*` variables (the token) from Compose's environment. Never use
  `down -v` or any other volume removal; the only exception is the `delete`
  operation with `volumes=true` (decision 19).
- Read and write stack files only through `internal/stackfiles` (validated,
  atomic, versioned, no symlinks), and only for managed stacks.
- Anything that can take longer than a moment (Compose actions, pulls) runs
  as an operation (`internal/operations`), never synchronously inside a
  request. Actions only work on managed stacks (`stacks.Target`).
- Service actions (decision 23): the affected services and Compose commands
  come only from `planServiceAction` in `internal/stacks/serviceplan.go`;
  every command names its services explicitly, with `--no-deps` where Compose
  would add more. What the confirmation showed must be exactly what runs.
- Long or open-ended output (operation output, logs) is streamed as
  Server-Sent Events via `startEventStream` in `agent/internal/api`; never
  buffer it in a single response.
- Configuration through `GANTRY_*` environment variables (plus Docker's own
  `DOCKER_HOST`), documented in the README table.
- New API endpoints go under `/api/v1/`. The auth middleware denies by
  default: only the entries in `publicRoutes` are public (see Security).

## Tests

- Table-driven tests next to the code (`*_test.go`), using `httptest` for
  handlers. Integration tests use the `integration` build tag, name their
  stacks `gantry-test-<random>-…`, clean up with `t.Cleanup`, and assert only
  on their own stacks.
- Tests must not depend on timing: wait for the event they need (a channel,
  or polling with a deadline) instead of sleeping or assuming an order. For
  streams, the response headers reach the client before the handler's work
  has run.

## Security

The root `AGENTS.md` has the rules for both parts. In the agent:

- **Public routes are explicit:** the middleware denies by default; only the
  method + path entries in `publicRoutes` (`agent/internal/api/auth.go`) are
  public. Add one only deliberately.
- **Sessions live in memory** (`session.Store`); logout revokes them. Wrong
  tokens, by login or bearer header, count against one limiter; never let
  the right token past it while a client is blocked.
- **Request bodies are limited** in the auth middleware (`maxBody`,
  `maxLoginBody`); raise them only deliberately.
- **Errors:** unexpected errors (500) are logged, never returned with
  details. Never show raw Compose output where it isn't asked for: it can
  quote `.env` lines.
- **Never expose secrets from stacks:** return Compose files as stored, never
  the resolved configuration (`docker compose config` output). `.env` values
  only through the reveal endpoint, one value per explicit request
  (decision 18). Read Compose data with `--no-interpolate`.
- **Never log values:** audit logs name the stack, file and keys, never
  `.env` values or file contents.
- Validate stack names with `stacks.ValidName` before using them in paths or
  commands. Symlinked stack directories and Compose files are never managed;
  file access goes through `os.Root`.
- **Assume exposure to the internet** (decision 21): credentials over plain
  HTTP from internet addresses are refused (`network.insecure`), client
  addresses come from `network.client` (trusted proxies only), the binary
  listens on 127.0.0.1 by default. Keep new endpoints behind these checks.
