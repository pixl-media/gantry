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
output; a stack's page shows services, containers and recent operations.

- **Logs:** the logs of all containers merged and live, with colors, a
  service filter, search, timestamps and a download; each container's logs
  button opens it already filtered.
- **Compose:** an editor with YAML highlighting and search. Saving checks the
  file with Compose first; *Save & apply* also starts the stack. If the file
  was changed elsewhere in the meantime, you're asked to reload instead of
  overwriting it.
- **Environment:** the stack's `.env` variables by name. A value is loaded
  only when you click to show or edit it.
- **New stack** on the list page, **Delete stack** in a stack's menu (type
  the name to confirm; named volumes only if you tick the box).

Unmanaged stacks are read-only. The UI is in English and German (from the
browser's language, switchable) and follows the system's light or dark mode.

**Login:** enter the agent token once. The agent exchanges it for a session
cookie that JavaScript can't read (`HttpOnly`, `SameSite=Strict`, 12 hours);
the token itself is never stored in the browser. After 10 wrong tokens
within a minute, by login or `Authorization` header, a client has to wait
(`429` with `Retry-After`). Behind the web UI's proxy all browsers share one
such limit.

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
| `POST /api/v1/stacks` | Create a managed stack from `{name, compose, env?}` |
| `GET /api/v1/stacks/{name}` | Services, containers (state, health, ports), named volumes and the raw Compose file with its `version` |
| `DELETE /api/v1/stacks/{name}?confirm={name}` | Delete a stack permanently, as an operation; `volumes=true` also removes its named volumes |
| `PUT /api/v1/stacks/{name}/compose` | Replace the Compose file (`{content, version}`), validated first |
| `GET /api/v1/stacks/{name}/env` | Names of the `.env` variables (no values) and the file's `version` |
| `PATCH /api/v1/stacks/{name}/env` | Set and delete `.env` variables (`{version, set, delete}`) |
| `POST /api/v1/stacks/{name}/env/reveal` | One `.env` value (`{key}`), logged without the value |
| `GET /api/v1/stacks/{name}/logs` | Logs of all containers, merged and live, as Server-Sent Events |
| `POST /api/v1/stacks/{name}/actions/{action}` | Run `start`, `stop`, `restart`, `update` or `down` as an operation |
| `GET /api/v1/operations` | Recent operations (`?stack=` to filter) |
| `GET /api/v1/operations/{id}` | Operation status and output |
| `GET /api/v1/operations/{id}/events` | Live output as Server-Sent Events |
| `POST /api/v1/operations/{id}/cancel` | Cancel a running operation |

The full contract is [`api/openapi.yaml`](api/openapi.yaml). Stack details
return the Compose file as stored: variables like `${DB_PASSWORD}` stay
unresolved. `.env` values are only returned one at a time by the reveal
endpoint. Errors carry a machine-readable `code` (e.g. `stack_busy`,
`stack_unmanaged`, `invalid_compose`, `version_conflict`). All `/api/`
responses are sent with `Cache-Control: no-store`. Request bodies are limited
to 4 MiB (4 KiB for the login); larger ones are refused with `413
request_too_large`. Requests must arrive within a minute; responses such as
event streams may run as long as needed.

### Logs

`GET /api/v1/stacks/{name}/logs` works like `docker compose logs -f`: first
the last `tail` lines (default 200, at most 5000) of every container, merged
by time, then new lines as they appear. Containers that are started or
recreated meanwhile (e.g. by `restart` or `update`) are picked up without
repeating lines. `service` and `container` filter; `follow=false` ends after
the past lines with an `end` event. Unmanaged stacks can be read too.

```bash
curl -N -H "Authorization: Bearer $GANTRY_TOKEN" "http://127.0.0.1:7070/api/v1/stacks/whoami/logs?tail=20"
# event: log
# data: {"time":"2026-11-12T20:15:03.112Z","service":"whoami","container":"whoami-whoami-1","stream":"stdout","line":"Starting up on port 80"}
```

Lines come as the programs wrote them, including ANSI color codes (the web UI
renders them), and may contain anything the programs print. If Docker can't
be read, a `failed` event ends the stream.

### Editing stacks

Only managed stacks can be edited. Every write is checked and atomic: a new
Compose file is validated with `docker compose config` before it replaces
the old one (`400 invalid_compose` with Compose's message otherwise), and
files are written to a temporary file and renamed, so a crash never leaves
half a file.

**Versions** prevent lost updates. Reads return a `version` (the SHA-256 of
the content); send it back when saving. If the file has changed since, e.g.
over SSH or in another browser tab, the agent answers `409
version_conflict` instead of overwriting it.

```bash
curl -H "Authorization: Bearer $GANTRY_TOKEN" http://127.0.0.1:7070/api/v1/stacks/whoami | jq -r .composeFile.version
# 3b1f…
curl -X PUT -H "Authorization: Bearer $GANTRY_TOKEN" -H "Content-Type: application/json" \
  -d '{"version":"3b1f…","content":"services:\n  whoami:\n    image: traefik/whoami:v1.12\n"}' \
  http://127.0.0.1:7070/api/v1/stacks/whoami/compose
```

Saving doesn't touch running containers; run `start` to apply the changes.

**`.env`:** `GET …/env` lists only the names. `PATCH …/env` sets and
deletes variables, keeping comments, blank lines and order; values are
quoted as needed. A single value is returned by `POST …/env/reveal`, which
the agent logs (stack, key and client, never the value). New `.env` files
are created with mode `0600`.

**Creating** a stack writes `compose.yaml` (and `.env`, if given) into a new
directory in `GANTRY_STACKS_DIR`, after validating them. Names already used
by a directory or a running Compose project are refused with `409
stack_exists`.

**Deleting** a stack (`DELETE /api/v1/stacks/{name}?confirm={name}`) runs as
an operation: `down --remove-orphans`, then the stack's directory is
removed, **including data in it such as `./data`**. This can't be undone.
Named volumes are kept unless `volumes=true` is passed. `confirm` must repeat
the stack name.

Symlinked stack directories and files are never edited or deleted; files are
limited to 1 MiB.

### Actions

Only managed stacks can be changed; unmanaged ones answer `409
stack_unmanaged`.

| Action | Compose | Effect |
|---|---|---|
| `start` | `up -d --remove-orphans` | Creates or updates containers to match the Compose file |
| `stop` | `stop` | Stops containers; they are kept |
| `restart` | `stop`, then `start` | Restarts existing containers, **without** applying Compose file changes (use `start` for that) |
| `update` | `pull --ignore-buildable`, then `up -d --remove-orphans` | Pulls new images and recreates changed containers |
| `down` | `down --remove-orphans` | Removes containers and networks. **Volumes are never removed** (only deleting a stack with `volumes=true` does) |

### Operations

Actions can take minutes (image pulls), so they run as operations: the
request returns `202` with the operation at once, and you follow it.

```bash
curl -X POST -H "Authorization: Bearer $GANTRY_TOKEN" http://127.0.0.1:7070/api/v1/stacks/whoami/actions/update
# {"id":"op_3f9c2a71b0de","stack":"whoami","action":"update","status":"running",...}

curl -N -H "Authorization: Bearer $GANTRY_TOKEN" http://127.0.0.1:7070/api/v1/operations/op_3f9c2a71b0de/events
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
| `GANTRY_TOKEN` | (required) | Token for the API and the web UI login; use at least 24 random characters (`openssl rand -hex 24`). Shorter than 16 characters: the agent refuses to start |
| `GANTRY_STACKS_DIR` | `/opt/stacks` | Directory with the managed stacks (same default as Dockge) |
| `GANTRY_LISTEN` | `:7070` | Listen address |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | Docker socket (only `unix://` is supported) |

The stacks directory uses the same default and layout as Dockge, so gantry
picks up existing Dockge stacks without configuration. If the directory
doesn't exist, the agent still starts, lists unmanaged stacks and reports the
missing directory as a warning.

The agent needs `docker compose` (the Compose plugin of Docker's official
packages) on the host. The container image includes it.

**Write access:** to create, edit and delete stacks, the user the agent runs
as needs write access to `GANTRY_STACKS_DIR` and the stacks in it. For a
systemd service that runs as a dedicated user, give that user ownership of
the directory (e.g. `chown -R gantry: /opt/stacks`); otherwise stacks can
only be listed and controlled. The container image runs as root.

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
export GANTRY_TOKEN=$(sed -n 's/^GANTRY_TOKEN=//p' .env)
curl -H "Authorization: Bearer $GANTRY_TOKEN" http://127.0.0.1:7070/api/v1/stacks
```

`make run` takes the token from `.env` (git-ignored) and creates that file
with a random token on first use, so the dev agent never runs with a token
anyone could guess. The agent refuses tokens shorter than 16 characters.

Agent and web UI in containers: `make .env && docker compose up --build`
(run from the repo root), then open http://127.0.0.1:8080 and log in with the
token from `.env`.

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
- Stacks can't be renamed (named volumes carry the project name).
- There is no history or undo for Compose files; validation and version
  checks protect against mistakes and lost updates.
- Validation checks the structure of a Compose file, not whether images
  exist or variables are set.
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
