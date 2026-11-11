# gantry

Lightweight Docker Compose manager: a small headless **agent** that runs on a
Docker host and exposes an HTTP API for managing Compose stacks, plus an
optional **web UI** that runs as its own container.

> Early stage. The agent currently serves a health check and its version.

## Architecture

```
            ┌──────────────┐        ┌────────────────────┐
 browser ──▶│ gantry web   │──/api─▶│ gantry agent       │──▶ Docker socket
            │ (optional)   │        │ HTTP API, Go       │    + stacks dir
            └──────────────┘        └────────────────────┘
 other API clients ─────────────────────────▲
```

- **Agent:** a single Go binary per Docker host. Recommended as a **systemd
  service**; also available as a **container image**.
- **API-first:** everything the agent does is available via its versioned
  HTTP API (`/api/v1/`), to be described by an OpenAPI spec.
- **Web UI (optional, planned):** a static React app in its own container that
  only talks to the agent's API.
- **Platforms:** linux/amd64 and linux/arm64.
- **Standalone:** gantry works on its own; it needs no other service.

Reasons for these choices: [docs/decisions.md](docs/decisions.md).

## Layout

| Path | What |
|---|---|
| `agent/` | Go agent (API server), its own Go module and Dockerfile |
| `web/` | Optional web UI (planned: React + Vite, separate container) |
| `docs/` | Architecture decisions |
| `compose.yaml` | Local development setup (agent as container) |
| `.github/workflows/ci.yml` | Tests, image builds and releases |

## Development

Requirements: Go 1.27+, Docker.

```bash
make run     # agent on http://127.0.0.1:7070 with token "dev"
make test    # Go tests
make lint    # gofmt + go vet
make image   # build the Docker image locally
```

```bash
curl http://127.0.0.1:7070/healthz
curl -H "Authorization: Bearer dev" http://127.0.0.1:7070/api/v1/version
```

Or in Docker: `cp .env.example .env && docker compose up --build`.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `GANTRY_TOKEN` | (required) | Bearer token for all `/api/` requests |
| `GANTRY_LISTEN` | `:7070` | Listen address |

## Workflow and releases

GitHub Flow: `main` is always releasable. Work happens on short-lived branches
(`feature/…`, `fix/…`) that are merged into `main` via pull request.

| Event | Image |
|---|---|
| Pull request | built, not pushed |
| Merge to `main` | `ghcr.io/pixl-media/gantry-agent:edge` |
| Tag `vX.Y.Z` | `:X.Y.Z`, `:X.Y`, `:latest` |

Release: test `:edge`, then `git tag v0.1.0 && git push origin v0.1.0`.

Planned: release binaries and `.deb`/`.rpm` packages with a systemd unit
for amd64 and arm64.
