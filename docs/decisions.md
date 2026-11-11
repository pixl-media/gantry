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

## Open questions

- **Compose operations:** call the `docker compose` CLI or use the Go
  libraries? Decide with the first feature; leaning towards the CLI for full
  compatibility.
- **Standalone UI authentication:** how the UI authenticates users without
  handing the agent token to the browser.
- **Release distribution:** GoReleaser for binaries, `.deb`/`.rpm` and images.
  While the repo is private, downloads need a token.
- **License**, before publishing.
