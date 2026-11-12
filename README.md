# gantry

Lightweight Docker Compose manager: a small headless **agent** that runs on a
Docker host and exposes an HTTP API for managing Compose stacks, plus an
optional **web UI** that runs as its own container.

> Early stage. The agent lists stacks and shows their details; managing them
> (start, stop, edit) comes next.

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
  HTTP API (`/api/v1/`), described by [`api/openapi.yaml`](api/openapi.yaml).
- **Web UI (optional, planned):** a static React app in its own container that
  only talks to the agent's API.
- **Platforms:** linux/amd64 and linux/arm64.
- **Standalone:** gantry works on its own; it needs no other service.

Reasons for these choices: [docs/decisions.md](docs/decisions.md).

## Stacks

A stack is a Compose project.

- **Managed stacks** live in `GANTRY_STACKS_DIR`, one directory per stack
  with a `compose.yaml` (or `compose.yml`, `docker-compose.yaml`,
  `docker-compose.yml`). The directory name is the project name and must be a
  valid Compose project name: lowercase letters, digits, `-` and `_`.
- **Unmanaged stacks** are other Compose projects running on the host,
  found through the labels Compose sets on their containers. gantry shows
  them read-only with `managed: false`.

```
/opt/stacks/
  whoami/compose.yaml
  nextcloud/compose.yaml
  nextcloud/.env
```

A stack's status is `running` (every service has a running container),
`partial`, `stopped` or `unknown` (Docker unreachable).

## API

All routes under `/api/` require `Authorization: Bearer <GANTRY_TOKEN>`.

| Route | Description |
|---|---|
| `GET /healthz` | Liveness check, public. 200 while the agent runs, even if Docker is down |
| `GET /api/v1/version` | Agent version |
| `GET /api/v1/stacks` | All stacks with status; `dockerAvailable` and `warnings` |
| `GET /api/v1/stacks/{name}` | Services, containers (state, health, ports) and the raw Compose file |

The full contract is [`api/openapi.yaml`](api/openapi.yaml). Stack details
return the Compose file as stored: variables like `${DB_PASSWORD}` stay
unresolved, and `.env` files are never returned.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `GANTRY_TOKEN` | (required) | Bearer token for all `/api/` requests |
| `GANTRY_STACKS_DIR` | (required) | Directory with the managed stacks |
| `GANTRY_LISTEN` | `:7070` | Listen address |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | Docker socket (only `unix://` is supported) |

The agent needs `docker compose` (the Compose plugin of Docker's official
packages) on the host. The container image includes it.

### Running as a container

Mount the Docker socket and the stacks directory **at the same path** inside
the container as on the host. Docker resolves relative bind mounts of stacks
(e.g. `./data`) on the host, using the path the agent passes:

```yaml
services:
  gantry-agent:
    image: ghcr.io/pixl-media/gantry-agent:edge
    environment:
      GANTRY_TOKEN: ${GANTRY_TOKEN}
      GANTRY_STACKS_DIR: /opt/stacks
    ports:
      - "7070:7070"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /opt/stacks:/opt/stacks
    restart: unless-stopped
```

## Layout

| Path | What |
|---|---|
| `agent/` | Go agent: its own Go module and Dockerfile |
| `api/openapi.yaml` | API contract; Go code is generated from it |
| `web/` | Optional web UI (planned: React + Vite, separate container) |
| `dev/stacks/` | Example stacks for local development |
| `docs/` | Architecture decisions |
| `compose.yaml` | Local development with the agent as a container |
| `api.http` | Example requests for the VS Code REST Client |
| `.github/workflows/ci.yml` | Tests, image builds and releases |

## Development

Requirements: Go 1.27+, Docker with the Compose plugin.

```bash
make run               # agent on http://127.0.0.1:7070, token "dev", managing dev/stacks
make test              # unit tests, no Docker needed
make test-integration  # unit + integration tests against the local Docker daemon
make lint              # gofmt + go vet
make generate          # regenerate Go code after changing api/openapi.yaml
make image             # build the Docker image locally
```

Try it: start an example stack with `docker compose up -d` in
`dev/stacks/whoami`, run `make run`, and send the requests in
[`api.http`](api.http) (VS Code extension "REST Client"), or:

```bash
curl -H "Authorization: Bearer dev" http://127.0.0.1:7070/api/v1/stacks
```

The agent in a container: `cp .env.example .env && docker compose up --build`
(run from the repo root; uses the token from `.env`).

Integration tests create their own stacks named `gantry-test-…` and remove
them afterwards; other containers on the host are not touched.

### Changing the API

1. Edit `api/openapi.yaml`.
2. `make generate` updates `agent/internal/api/api.gen.go` (never edit it by
   hand).
3. Implement the new methods in `agent/internal/api/handlers.go`; the
   compiler shows what's missing.

## Known limitations

- One-shot services (e.g. init containers that exit with code 0) make a stack
  show as `partial`.
- For unmanaged stacks, only services that currently have containers are
  known.

## Workflow and releases

GitHub Flow: `main` is always releasable. Work happens on short-lived branches
(`feature/…`, `fix/…`) that are merged into `main` via pull request.

| Event | CI |
|---|---|
| Pull request | tests, integration tests, image built and smoke-tested, not pushed |
| Merge to `main` | same, then `ghcr.io/pixl-media/gantry-agent:edge` is pushed |
| Tag `vX.Y.Z` | same, then `:X.Y.Z`, `:X.Y`, `:latest` are pushed |

Release: test `:edge`, then `git tag v0.1.0 && git push origin v0.1.0`.

Planned: release binaries and `.deb`/`.rpm` packages with a systemd unit
for amd64 and arm64.
