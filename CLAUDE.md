# gantry

Lightweight Docker Compose manager. A headless Go **agent** (`agent/`) runs on
a Docker host and exposes an HTTP API; an optional **web UI** (`web/`, React +
Vite + MUI, served by Caddy) runs as a separate container and talks only to
that API.

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
- `make run` – agent on 127.0.0.1:7070, stacks dir `dev/stacks`, token from
  `.env` (git-ignored, created with a random token on first run)
- `make docs` – dev-only Swagger UI on 127.0.0.1:8082, forwarding to the agent
  (`agent/tools/devdocs`, never part of the agent binary or image)
- `make image` – build the Docker image

Web UI (`web/`, pnpm, Node 24):

- `make web` – Vite dev server on 127.0.0.1:5173, `/api` forwarded to the agent
- `make web-test` – Vitest; `make web-lint` – ESLint, Prettier, typecheck
- `make web-generate` – regenerate `web/src/api/schema.gen.ts` from the spec
  (CI fails if it's out of date)
- `make web-build`, `make web-image`

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
- Long or open-ended output (operation output, logs) is streamed as
  Server-Sent Events via `startEventStream` in `agent/internal/api`; never
  buffer it in a single response.
- Configuration through `GANTRY_*` environment variables (plus Docker's own
  `DOCKER_HOST`), documented in the README table.
- New API endpoints go under `/api/v1/`. The auth middleware denies by
  default: only the entries in `publicRoutes` are public (see Security).
- Table-driven tests next to the code (`*_test.go`), using `httptest` for
  handlers. Integration tests use the `integration` build tag, name their
  stacks `gantry-test-<random>-…`, clean up with `t.Cleanup`, and assert only
  on their own stacks.
- Tests must not depend on timing: wait for the event they need (a channel,
  or polling with a deadline) instead of sleeping or assuming an order. For
  streams, the response headers reach the client before the handler's work
  has run.

## Web UI conventions

- API access only through the generated types (`web/src/api/schema.gen.ts`,
  never edited by hand) and the hooks in `web/src/api/queries.ts`.
- **Every UI text goes through i18n** (`t('…')`) and exists in both
  `web/src/i18n/locales/en.json` and `de.json`; a test checks that the keys
  match, and the typecheck rejects unknown keys.
- MUI components and the theme in `web/src/theme.ts`; no external fonts or
  scripts (the CSP in `web/Caddyfile` allows only the own origin).
- Action rules (what's possible when) live in `web/src/lib/actions.ts` and
  mirror the agent's rules.
- The app uses a data router (`createBrowserRouter` in `main.tsx`, routes in
  `App.tsx`), which `useBlocker` needs; tests for such components render with
  `renderWithDataRouter`. The CodeMirror editor (`components/CodeEditor.tsx`)
  is loaded lazily; tests replace it with `test/codeEditorStub.tsx`.
- Prettier style: single quotes, trailing commas (as in the user's other
  projects).

## Security

The agent has access to the Docker socket, which is root-equivalent on the
host. Every `/api/` route must stay behind authentication (bearer token or
session cookie), never log the token, and keep the dev setup bound to
127.0.0.1.

- **Public routes are explicit:** the middleware denies by default; only the
  method + path entries in `publicRoutes` (`agent/internal/api/auth.go`) are
  public. Add one only deliberately.
- **Cookie auth keeps its CSRF check:** changing requests with the session
  cookie need a same-origin `Origin` header. Don't weaken `SameSite=Strict`
  or `HttpOnly`.
- **Never store tokens in the browser** (`localStorage`, `sessionStorage`,
  JS-readable cookies). The web UI only uses the `HttpOnly` session cookie.
  Mutations whose variables hold the token or `.env` values use `gcTime: 0`.
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
- Settled hardening decisions are in `docs/decisions.md` (20, 21).
