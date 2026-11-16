# gantry

Lightweight Docker Compose manager. A headless Go **agent** (`agent/`) runs on
a Docker host and exposes an HTTP API; an optional **web UI** (`web/`, React +
Vite + MUI, served by Caddy) runs as a separate container and talks only to
that API.

This file holds what applies everywhere. Details for one part are in
`agent/AGENTS.md` and `web/AGENTS.md`, read when working there.

## Project map

| Where | What |
|---|---|
| `api/openapi.yaml` | The API spec; every API change starts here |
| `agent/` | Go agent, see `agent/AGENTS.md` |
| `agent/cmd/gantry-agent/main.go` | Configuration (`GANTRY_*`), token check, startup |
| `agent/internal/api/` | HTTP wiring: `server.go`, `auth.go` (auth middleware, `publicRoutes`), `clients.go` (client address, plain-HTTP guard), `handlers.go`, `api.gen.go` (generated) |
| `agent/internal/*/` | `docker`, `compose`, `stacks`, `operations`, `logs`, `session`, `stackfiles`, `tlscert` (roles in `agent/AGENTS.md`) |
| `agent/tools/devdocs/` | Dev-only Swagger UI (`make docs`) |
| `web/` | Web UI, see `web/AGENTS.md` |
| `web/src/` | `api/` (generated types, hooks), `components/`, `pages/`, `lib/`, `i18n/`, `theme.ts` |
| `web/Caddyfile` | Static files, proxy to the agent, CSP, plain-HTTP guard |
| `docs/decisions.md` | Settled decisions and their reasons (numbered) |
| `README.md` | User docs: configuration table (`GANTRY_*`), exposing gantry, compose example |
| `compose.yaml`, `.env.example` | Container setup of agent and web UI |
| `api.http` | Example requests (API token from `.env`) |
| `dev/stacks/` | Example stacks for `make run` |
| `.github/` | CI (`workflows/ci.yml`) and Dependabot |
| `LICENSE` | AGPLv3 (decision 22) |

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

- Agent: `make test`, `make test-integration` (needs Docker), `make lint`,
  `make generate`, `make run`, `make docs`, `make image`; details in
  `agent/AGENTS.md`.
- Web UI: `make web`, `make web-test`, `make web-lint`, `make web-generate`,
  `make web-build`, `make web-image`; details in `web/AGENTS.md`.

## Git workflow

- GitHub Flow. `main` must always be releasable.
- **Never commit or push directly to `main`.** Create a branch
  (`feature/…`, `fix/…`, `chore/…`) and open a pull request.
- Releases are tags `vX.Y.Z` on `main`; CI publishes the images. Don't create
  tags unless asked.
- Commit messages, code, comments and docs in English.
- **No outside code contributions** without a contributor license agreement;
  every line comes from the copyright holder (decision 22). Copy code or add
  runtime dependencies only under permissive licenses (MIT, BSD, ISC,
  Apache-2.0), never GPL or similar.

## Security

The agent has access to the Docker socket, which is root-equivalent on the
host. These rules apply to both parts; the part-specific ones are repeated
in short here so they're never missing, with details in `agent/AGENTS.md`
and `web/AGENTS.md`.

- Every `/api/` route stays behind authentication (bearer token or session
  cookie). **Public routes are explicit:** only the entries in `publicRoutes`
  (`agent/internal/api/auth.go`) are public; add one only deliberately.
- **Two tokens, strictly separate** (decision 26): `GANTRY_TOKEN` only as
  bearer for API clients, `GANTRY_UI_TOKEN` only for the web login.
- Never log tokens; keep the dev setup bound to 127.0.0.1.
- **Never store tokens in the browser** (`localStorage`, `sessionStorage`,
  JS-readable cookies). The web UI only uses the `HttpOnly` session cookie.
- **Cookie auth keeps its CSRF check:** changing requests with the session
  cookie need a same-origin `Origin` header. Don't weaken `SameSite=Strict`
  or `HttpOnly`.
- **Never expose secrets from stacks:** `.env` values only through the reveal
  endpoint, one value per explicit request (decision 18); never return the
  resolved Compose configuration; never log values.
- **Never use `down -v`** or any other volume removal, except the `delete`
  operation with `volumes=true` (decision 19).
- **Assume exposure to the internet** (decision 21): credentials over plain
  HTTP from internet addresses are refused, client addresses come from
  trusted proxies only, the binary listens on 127.0.0.1 by default.
- Settled hardening decisions are in `docs/decisions.md` (20, 21).
