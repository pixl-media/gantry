# gantry

Lightweight Docker Compose manager: a small headless **agent** that runs on a
Docker host and exposes an HTTP API for managing Compose stacks, plus an
optional **web UI** that runs as its own container.

> Early stage. The agent currently serves a health check and its version.

## Layout

| Path | What |
|---|---|
| `agent/` | Go agent (API server), its own Go module and Dockerfile |
| `web/` | Optional web UI (planned, separate container) |
| `compose.yaml` | Local development setup |
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
