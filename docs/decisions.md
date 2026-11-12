# Architecture decisions

Settled decisions and the reasons behind them. Change one only deliberately,
and record the new decision here instead of editing history.

## 1. Agent in Go

**Decided 2026-11-12.** The agent is written in Go.

- A single static binary, no runtime: ~20 MB image, ~10 MB RAM idle, trivial
  cross-compilation. "Lightweight" is the core promise of gantry.
- Docker, Compose and the Docker SDK are Go; the ecosystem fits.
- The standard library covers HTTP, JSON and logging, so there are few
  dependencies to maintain.

Rejected: TypeScript/Node for the agent (one language with the UI, but a
5–10× larger image and memory footprint and constant dependency churn).

## 2. systemd service first, container optional

**Decided 2026-11-12.** The same binary ships two ways; the systemd service is
the primary, recommended mode, the container image is an option.

- Access to the Docker socket is root-equivalent either way, so a container
  adds no real isolation for this agent.
- On the host, the agent sees stack paths as Docker does. In a container the
  stacks directory must be mounted at the identical path, or relative bind
  mounts in stacks resolve to the wrong place.
- A host service keeps running when Docker is down and can report that; a
  containerized agent goes down with Docker.
- Self-update is simple (replace binary, restart unit) and systemd offers
  sandboxing (dedicated user, `ProtectSystem`, …).
- The container stays for NAS/appliance systems (Synology, Unraid, TrueNAS)
  and quick trials. Its image must bundle the Docker CLI and Compose plugin.

## 3. Platforms: linux/amd64 and linux/arm64

**Decided 2026-11-12.** Binaries, packages and images are built for both.
No 32-bit ARM: Docker itself is phasing out 32-bit Raspberry Pi OS. macOS is
for development only.

## 4. API-first

**Decided 2026-11-12.** The agent's HTTP API is the product boundary.

- Every feature exists as an API endpoint first; UIs only consume the API.
- The API is versioned (`/api/v1/`) and described by an OpenAPI spec, from
  which a TypeScript client is generated and shared by every consumer: the
  gantry web UI and a future infrastructure app (Next.js).
- Consumers that manage many agents keep agent tokens server-side; browsers
  never receive agent tokens from such an app.

## 5. Web UI: React + Vite SPA

**Decided 2026-11-12.** The optional web UI is a static React SPA built with
Vite, served by a small web server container that proxies `/api` to the agent.

- The agent is the backend, so the UI needs no server runtime.
- Next.js was considered for consistency with the infrastructure app, but its
  server features are unused here, and a static export can't serve runtime
  dynamic routes such as `/stacks/[name]`.
- React keeps components and knowledge portable to the Next.js app.
- The UI is built after the API, once its scope is clear.

## 6. Standalone and publishable

**Decided 2026-11-12.** gantry must be fully usable on its own (agent plus
optional UI), without the future infrastructure app, and stay publishable as
open source: no organization-internal hosts, services or secrets, all
configuration via `GANTRY_*` variables, all docs in English.

## 7. Docker access: Engine API for state, Compose CLI for Compose

**Decided 2026-11-12.** Container state is read from the Docker Engine API
over the socket, with a small standard-library client (`agent/internal/docker`):
one request returns every Compose container with labels, state and health.
Everything that is Compose logic (evaluating Compose files, later up/down)
runs through the `docker compose` CLI (`agent/internal/compose`), so it
behaves exactly as in a terminal.

Rejected: CLI only (one process per query, dependence on CLI output formats,
awkward for live events later) and Go libraries only (the Compose library
pulls in a very large dependency tree and may behave differently from the
host's `docker compose`).

Consequence: hosts need the Compose plugin (part of Docker's official
packages); the container image bundles the Docker CLI and the plugin.

## 8. Stack model: stacks directory plus discovery

**Decided 2026-11-12.** Managed stacks are the subdirectories of
`GANTRY_STACKS_DIR` that contain a Compose file; the directory name is the
Compose project name. Other Compose projects on the host are discovered
through their container labels and reported with `managed: false`, read-only.
This shows the whole host without gantry silently taking over stacks it
doesn't own.

Stack names must be valid Compose project names (`^[a-z0-9][a-z0-9_-]*$`),
which also rules out path traversal.

`GANTRY_STACKS_DIR` defaults to `/opt/stacks`, the same directory and layout
as Dockge, so existing Dockge setups work without configuration. A missing
directory is reported as a warning, not a startup error: the agent still
reports unmanaged stacks and Docker's state.

## 9. OpenAPI spec-first with oapi-codegen

**Decided 2026-11-12.** `api/openapi.yaml` is the contract. oapi-codegen
generates Go types and a strict server interface for `net/http` into
`agent/internal/api/api.gen.go`; handlers implement that interface, so code
that drifts from the spec doesn't compile. CI fails if the generated code is
out of date. API clients (web UI, infrastructure app) are generated from the
same file.

Rejected: hand-written spec and handlers (drift only caught by tests, types
maintained twice) and code-first (the contract becomes a by-product, and it
ties us to a framework).

## 10. The API never exposes secrets from stacks

**Decided 2026-11-12.** Stack details return the Compose file exactly as
stored (`${DB_PASSWORD}` stays unresolved). The API never returns the resolved
configuration (`docker compose config` output) or `.env` contents, and the
agent reads service names with `--no-interpolate`. Managing `.env` values
needs its own feature with its own protection.

## 11. `/healthz` is a liveness check

**Decided 2026-11-12.** `/healthz` (public) returns 200 as long as the agent
serves requests, also when Docker is down, so monitors don't restart the agent
while it reports the outage. Docker's state is part of the API responses
(`dockerAvailable`). The `z` follows the Go/Kubernetes convention and avoids
collisions with application routes.

## Open questions

- **Standalone UI authentication:** how the UI authenticates users without
  handing the agent token to the browser.
- **Release distribution:** GoReleaser for binaries, `.deb`/`.rpm` and images.
  While the repo is private, downloads need a token.
- **License**, before publishing.
