# gantry

Lightweight Docker Compose manager: a small headless **agent** that runs on a
Docker host and exposes an HTTP API for managing Compose stacks, plus an
optional **web UI** that runs as its own container.

> Early stage. The agent lists stacks, shows their details and starts, stops,
> restarts, updates and removes them; the web UI does all of that in the
> browser, in English and German. Editing stacks comes next.

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
- **Web UI (optional):** a static React app in its own container (Caddy) that
  only talks to the agent's API.
- **Platforms:** linux/amd64 and linux/arm64.
- **Standalone:** gantry works on its own; it needs no other service.

Reasons for these choices: [docs/decisions.md](docs/decisions.md).

## Stacks

A stack is a Compose project.

- **Managed stacks** live in `GANTRY_STACKS_DIR` (default `/opt/stacks`), one directory per stack
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

## Web UI

The web UI lists all stacks with their status and runs actions with live
output; a stack's page shows services, containers, the Compose file and
recent operations. Its **Logs** tab shows the logs of all containers merged
and live, with colors, a service filter, search, timestamps and a download;
each container's logs button opens it already filtered. It is in English and
German (from the browser's language, switchable) and follows the system's
light or dark mode.

**Login:** enter the agent token once. The agent exchanges it for a session
cookie that JavaScript can't read (`HttpOnly`, `SameSite=Strict`, 12 hours);
the token itself is never stored in the browser.

## API

Routes under `/api/` require authentication: `Authorization: Bearer
<GANTRY_TOKEN>` for API clients, or the session cookie for the web UI. With
the cookie, POST and DELETE requests must come from the same origin
(`Origin` header), which protects against cross-site request forgery.

| Route | Description |
|---|---|
| `GET /healthz` | Liveness check, public. 200 while the agent runs, even if Docker is down |
| `POST /api/v1/session` | Log in with `{"token": …}`; sets the session cookie (public) |
| `GET /api/v1/session` | Whether and how the request is authenticated |
| `DELETE /api/v1/session` | Log out; clears the cookie (public) |
| `GET /api/v1/version` | Agent version |
| `GET /api/v1/stacks` | All stacks with status; `dockerAvailable` and `warnings` |
| `GET /api/v1/stacks/{name}` | Services, containers (state, health, ports) and the raw Compose file |
| `GET /api/v1/stacks/{name}/logs` | Logs of all containers, merged and live, as Server-Sent Events |
| `POST /api/v1/stacks/{name}/actions/{action}` | Run `start`, `stop`, `restart`, `update` or `down` as an operation |
| `GET /api/v1/operations` | Recent operations (`?stack=` to filter) |
| `GET /api/v1/operations/{id}` | Operation status and output |
| `GET /api/v1/operations/{id}/events` | Live output as Server-Sent Events |
| `POST /api/v1/operations/{id}/cancel` | Cancel a running operation |

The full contract is [`api/openapi.yaml`](api/openapi.yaml). Stack details
return the Compose file as stored: variables like `${DB_PASSWORD}` stay
unresolved, and `.env` files are never returned. Errors carry a
machine-readable `code` (e.g. `stack_busy`, `stack_unmanaged`).

### Logs

`GET /api/v1/stacks/{name}/logs` works like `docker compose logs -f`: first
the last `tail` lines (default 200, at most 5000) of every container, merged
by time, then new lines as they appear. Containers that are started or
recreated meanwhile (e.g. by `restart` or `update`) are picked up without
repeating lines. `service` and `container` filter; `follow=false` ends after
the past lines with an `end` event. Unmanaged stacks can be read too.

```bash
curl -N -H "Authorization: Bearer dev" "http://127.0.0.1:7070/api/v1/stacks/whoami/logs?tail=20"
# event: log
# data: {"time":"2026-11-12T20:15:03.112Z","service":"whoami","container":"whoami-whoami-1","stream":"stdout","line":"Starting up on port 80"}
```

Lines come as the programs wrote them, including ANSI color codes (the web UI
renders them), and may contain anything the programs print. If Docker can't
be read, a `failed` event ends the stream.

### Actions

Only managed stacks can be changed; unmanaged ones answer `409
stack_unmanaged`.

| Action | Compose | Effect |
|---|---|---|
| `start` | `up -d --remove-orphans` | Creates or updates containers to match the Compose file |
| `stop` | `stop` | Stops containers; they are kept |
| `restart` | `stop`, then `start` | Restarts existing containers, **without** applying Compose file changes (use `start` for that) |
| `update` | `pull --ignore-buildable`, then `up -d --remove-orphans` | Pulls new images and recreates changed containers |
| `down` | `down --remove-orphans` | Removes containers and networks. **Volumes are never removed** |

### Operations

Actions can take minutes (image pulls), so they run as operations: the
request returns `202` with the operation at once, and you follow it.

```bash
curl -X POST -H "Authorization: Bearer dev" http://127.0.0.1:7070/api/v1/stacks/whoami/actions/update
# {"id":"op_3f9c2a71b0de","stack":"whoami","action":"update","status":"running",...}

curl -N -H "Authorization: Bearer dev" http://127.0.0.1:7070/api/v1/operations/op_3f9c2a71b0de/events
# event: output
# data: $ docker compose pull --ignore-buildable
# ...
# event: done
# data: {"id":"op_3f9c2a71b0de","status":"succeeded","exitCode":0,...}
```

- **One operation per stack.** `start`, `restart` and `update` are rejected
  with `409 stack_busy` (naming the running operation) while another
  operation runs. **`stop` and `down` take precedence**: they cancel the
  running operation and then run, as an emergency brake. They run their
  command twice in that case, to settle Docker calls the canceled operation
  had already made. Other stacks are not affected.
- **Cancel** sends Compose an interrupt, like Ctrl+C. The stack may be left
  partly changed; the next `start` or `stop` brings it back to a consistent
  state.
- Each operation is limited to **30 minutes**. Output is kept up to 1 MiB
  (the end).
- Operations are kept **in memory** for an hour; they are gone after an agent
  restart. The stacks themselves are unaffected, since their state comes from
  Docker.
- Browsers' `EventSource` can't send an `Authorization` header; read the event
  stream with `fetch`.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `GANTRY_TOKEN` | (required) | Token for the API and the web UI login; use at least 24 random characters (`openssl rand -hex 24`) |
| `GANTRY_STACKS_DIR` | `/opt/stacks` | Directory with the managed stacks (same default as Dockge) |
| `GANTRY_LISTEN` | `:7070` | Listen address |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | Docker socket (only `unix://` is supported) |

The stacks directory uses the same default and layout as Dockge, so gantry
picks up existing Dockge stacks without configuration. If the directory
doesn't exist, the agent still starts, lists unmanaged stacks and reports the
missing directory as a warning.

The agent needs `docker compose` (the Compose plugin of Docker's official
packages) on the host. The container image includes it.

**Private registries:** Compose uses the Docker credentials of the user the
agent runs as (`~/.docker/config.json`, from `docker login`). In container
mode, mount that file into the container (e.g.
`/root/.docker/config.json:/root/.docker/config.json:ro`).

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

  # Optional web UI on port 8080. It forwards /api to the agent.
  gantry-web:
    image: ghcr.io/pixl-media/gantry-web:edge
    environment:
      GANTRY_AGENT_URL: http://gantry-agent:7070
    ports:
      - "8080:8080"
    restart: unless-stopped
```

The web UI speaks plain HTTP on port 8080. For access beyond your own
machine, put it behind a reverse proxy with HTTPS (e.g. Caddy, Traefik);
proxies in private networks are trusted for `X-Forwarded-Proto`, so the
session cookie is then marked `Secure`. The web container also works with an
agent running as a systemd service: point `GANTRY_AGENT_URL` at it.

## Layout

| Path | What |
|---|---|
| `agent/` | Go agent: its own Go module and Dockerfile |
| `api/openapi.yaml` | API contract; Go code is generated from it |
| `web/` | Optional web UI: React, Vite, MUI; Dockerfile and Caddyfile |
| `dev/stacks/` | Example stacks for local development |
| `docs/` | Architecture decisions |
| `compose.yaml` | Local development with agent and web UI as containers |
| `api.http` | Example requests for the VS Code REST Client |
| `agent/tools/devdocs/` | Dev-only Swagger UI server (`make docs`) |
| `.github/workflows/ci.yml` | Tests, image builds and releases |

## Development

Requirements: Go 1.27+, Docker with the Compose plugin; for the web UI
Node 24 and pnpm 10.

```bash
make run               # agent on http://127.0.0.1:7070, token "dev", managing dev/stacks
make web-install       # once: install the web UI's dependencies
make web               # web UI on http://127.0.0.1:5173 (in a second terminal), log in with "dev"
make docs              # Swagger UI on http://127.0.0.1:8082 (in a second terminal)
make test              # unit tests, no Docker needed
make test-integration  # unit + integration tests against the local Docker daemon
make lint              # gofmt + go vet
make generate          # regenerate Go code after changing api/openapi.yaml
make image             # build the agent's Docker image locally
make web-test          # web UI tests (Vitest)
make web-lint          # ESLint, Prettier, typecheck
make web-generate      # regenerate the web UI's API types after changing the spec
make web-image         # build the web UI's Docker image locally
```

The web dev server forwards `/api` to the agent (`GANTRY_AGENT_URL`, default
`http://127.0.0.1:7070`), so the app and the API share one origin, as behind
Caddy in the container.

Try it: start an example stack with `docker compose up -d` in
`dev/stacks/whoami`, run `make run`, and send the requests in
[`api.http`](api.http) (VS Code extension "REST Client"), or:

```bash
curl -H "Authorization: Bearer dev" http://127.0.0.1:7070/api/v1/stacks
```

Agent and web UI in containers: `cp .env.example .env && docker compose up
--build` (run from the repo root), then open http://127.0.0.1:8080 and log in
with the token from `.env`.

### API docs

`make docs` serves Swagger UI for `api/openapi.yaml` on
http://127.0.0.1:8082 and forwards API requests to the agent on port 7070
(`make run` or `docker compose up`). Everything comes from one address, so
"Try it out" works without CORS: click "Authorize", enter the token (`dev`
for `make run`), and send requests. The spec is re-read on every page load.
This is a development tool only; the agent doesn't serve docs itself.

Integration tests create their own stacks named `gantry-test-…` and remove
them afterwards; other containers on the host are not touched.

### Changing the API

1. Edit `api/openapi.yaml`.
2. `make generate` updates `agent/internal/api/api.gen.go` (never edit it by
   hand).
3. Implement the new methods in `agent/internal/api/handlers.go`; the
   compiler shows what's missing.
4. `make web-generate` updates the web UI's types
   (`web/src/api/schema.gen.ts`).

## Known limitations

- One-shot services (e.g. init containers that exit with code 0) make a stack
  show as `partial`.
- For unmanaged stacks, only services that currently have containers are
  known.
- Operations are kept in memory only and are lost when the agent restarts.
- Swagger UI (`make docs`) can't display the event stream; use `curl -N`.
- One shared login (the agent token); there are no user accounts.
- The web UI refreshes every 5 seconds instead of receiving push events.
- Compose files are read-only in the web UI.
- Logs exist only as long as Docker keeps them; with log drivers such as
  `none` or `syslog` Docker returns none.
- The web UI keeps at most 10,000 log lines and searches only those, in the
  browser.

## Workflow and releases

GitHub Flow: `main` is always releasable. Work happens on short-lived branches
(`feature/…`, `fix/…`) that are merged into `main` via pull request.

| Event | CI |
|---|---|
| Pull request | Go and web tests, integration tests, both images built and smoke-tested, not pushed |
| Merge to `main` | same, then `ghcr.io/pixl-media/gantry-agent:edge` and `gantry-web:edge` are pushed |
| Tag `vX.Y.Z` | same, then `:X.Y.Z`, `:X.Y`, `:latest` of both images are pushed |

Release: test `:edge`, then `git tag v0.1.0 && git push origin v0.1.0`.

Planned: release binaries and `.deb`/`.rpm` packages with a systemd unit
for amd64 and arm64.
