<h1>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/logo/gantry-mark-dark.svg">
    <img src="docs/logo/gantry-mark.svg" alt="" width="44" align="top">
  </picture>
  gantry
</h1>

Lightweight Docker Compose manager: a small headless **agent** that runs on a
Docker host and exposes an HTTP API for managing Compose stacks, plus an
optional **web UI** that runs as its own container.

> Version 0.2. The agent lists stacks and shows their details, logs and
> volume sizes; starts, stops, restarts, updates and removes stacks or single
> services; edits, creates and deletes managed stacks; and tells clients
> about changes live. The web UI does all of that in the browser, in English
> and German, optionally with single sign-on through an identity provider.
> Packages and images are on the
> [releases](https://github.com/pixl-media/gantry/releases) page.

## Architecture

```mermaid
flowchart LR
  subgraph host["Docker host"]
    agent["gantry agent<br/>HTTP API, Go"]
    docker[("Docker socket")]
    stacks[("Stacks directory<br/>/opt/stacks")]
    agent -- "Engine API, docker compose" --> docker
    agent -- "read, write" --> stacks
  end
  browser["Browser"] --> web["gantry web (optional)<br/>React app, Caddy"]
  web -- "/api, session cookie" --> agent
  clients["Other API clients<br/>curl, scripts"] -- "bearer token (GANTRY_TOKEN)" --> agent
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
Changes appear right away, also those made outside gantry (e.g. `docker
stop` in a shell): the UI follows the agent's live event stream and reloads
what changed.

- **Stack list:** filter by type (managed or external) and search by name;
  the filter stays in the address, also when coming back from a stack. Shown
  as a table or as cards (the default on phones); the choice is remembered in
  the browser.
- **Logs:** the logs of all containers merged and live, with colors, a
  service filter, search, timestamps and a download; each container's logs
  button opens it already filtered.
- **Compose:** an editor with YAML highlighting and search. Saving checks the
  file with Compose first; *Save & apply* also starts the stack. If the file
  was changed elsewhere in the meantime, you're asked to reload instead of
  overwriting it.
- **Environment:** the stack's `.env` variables by name. A value is loaded
  only when you click to show or edit it.
- **Services:** each service on the Overview tab has a menu to start, stop,
  restart or update just that service. If that changes other services too
  (see [Service actions](#service-actions)), they are listed for you to
  confirm first.
- **New stack** on the list page, **Delete stack** in a stack's menu (type
  the name to confirm; named volumes only if you tick the box).
- **Notices:** when an action you started ends while its output panel is
  closed, a short message at the top right says how it went, with a button
  to the output if it failed or was canceled.
- **File access:** if the agent may not change a stack's files, its page
  says what's missing and shows the command that allows it; editing is
  locked until then (see [Write access](#configuration)).

The bottom of every page shows the versions of the web UI and the agent,
and warns if they come from different releases (see
[Updating](#updating)).

Unmanaged stacks are read-only. The UI is in English and German (English
by default, switchable in the app bar) and has a light and a dark mode
(following the system by default, switchable too).

**Login:** enter the web login token (`GANTRY_UI_TOKEN`) once; the API token
doesn't work here. The agent exchanges it for a session cookie that
JavaScript can't read (`HttpOnly`, `SameSite=Strict`, 12 hours); the token
itself is never stored in the browser. Logging out ends the
session at once; restarting the agent ends all sessions, so you log in again
after an update. After 10 wrong tokens
within a minute, by login or `Authorization` header, a client has to wait
(`429` with `Retry-After`). Behind the web UI's proxy all browsers share one
such limit, unless the proxy is listed in `GANTRY_TRUSTED_PROXIES`; then each
browser has its own.

**Single sign-on:** optionally, the login page also offers *Sign in with
single sign-on* through an identity provider (OIDC), such as authentik or
Keycloak, with its two-factor login and passkeys; only accounts on an allow
list get in (see [Single sign-on](#single-sign-on-oidc)).

## API

Routes under `/api/` require authentication: `Authorization: Bearer
<GANTRY_TOKEN>` for API clients, or the session cookie for the web UI (from a
login with `GANTRY_UI_TOKEN` or single sign-on). Each token works only in its
own place. With
the cookie, changing requests (POST, PUT, PATCH, DELETE) must come from the
same origin (`Origin` header), which protects against cross-site request
forgery. The agent logs every changing request with how it was
authenticated (`via=token` or `via=session`, with `user=` after single
sign-on), never with its body.

| Route | Description |
|---|---|
| `GET /healthz` | Liveness check, public. 200 while the agent runs, even if Docker is down |
| `POST /api/v1/session` | Log in with `{"token": …}` (`GANTRY_UI_TOKEN`); sets the session cookie (public) |
| `GET /api/v1/session/methods` | Which ways to log in the agent offers: `{"token": …, "oidc": …}` (public) |
| `GET /api/v1/session/oidc` | Single sign-on: redirects the browser to the identity provider (public) |
| `GET /api/v1/session/oidc/callback` | Where the identity provider sends the browser back; sets the session cookie (public) |
| `GET /api/v1/session` | Whether and how the request is authenticated, and who logged in by single sign-on |
| `DELETE /api/v1/session` | Log out; clears the cookie (public) |
| `GET /api/v1/version` | Agent version |
| `GET /api/v1/info` | Agent version and its Docker host: name, system, architecture, kernel, Docker and Compose versions |
| `GET /api/v1/stacks` | All stacks with status; `dockerAvailable` and `warnings` |
| `GET /api/v1/volumes` | Volumes of all Compose projects with their size on disk, including leftovers of deleted stacks; measured by Docker, kept for a minute |
| `POST /api/v1/stacks` | Create a managed stack from `{name, compose, env?}` |
| `GET /api/v1/stacks/{name}` | Services, containers (state, health, ports), named volumes and the raw Compose file with its `version` |
| `GET /api/v1/stacks/{name}/volumes` | A stack's volumes with their sizes and the total |
| `DELETE /api/v1/stacks/{name}?confirm={name}` | Delete a stack permanently, as an operation; `volumes=true` also removes its named volumes |
| `PUT /api/v1/stacks/{name}/compose` | Replace the Compose file (`{content, version}`), validated first |
| `GET /api/v1/stacks/{name}/env` | Names of the `.env` variables (no values) and the file's `version` |
| `PATCH /api/v1/stacks/{name}/env` | Set and delete `.env` variables (`{version, set, delete}`) |
| `POST /api/v1/stacks/{name}/env/reveal` | One `.env` value (`{key}`), logged without the value |
| `GET /api/v1/stacks/{name}/logs` | Logs of all containers, merged and live, as Server-Sent Events |
| `GET /api/v1/events` | Which stacks changed, live, as Server-Sent Events (`ready`, `changed`) |
| `POST /api/v1/stacks/{name}/actions/{action}` | Run `start`, `stop`, `restart`, `update` or `down` as an operation |
| `POST /api/v1/stacks/{name}/services/{service}/actions/{action}` | Run `start`, `stop`, `restart` or `update` on one service and what depends on it (`{confirm?}`) |
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

### Live changes

`GET /api/v1/events` tells clients when to reload, instead of polling. It
only names the stacks that changed; read them with the usual endpoints.

```bash
curl -N -H "Authorization: Bearer $GANTRY_TOKEN" http://127.0.0.1:7070/api/v1/events
# event: ready
# data: {}
#
# event: changed
# data: {"stacks":["whoami"],"all":false}
```

`ready` comes on every connect: reload everything, changes may have been
missed. `changed` with `"all": true` means the same (e.g. Docker was
unreachable). Changes from Docker's container events and from gantry's own
operations and file saves are collected for a quarter second; an idle stream
gets a `ping` event every 25 seconds.

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

### Service actions

`POST /api/v1/stacks/{name}/services/{service}/actions/{action}` runs
`start`, `stop`, `restart` or `update` on one service. gantry follows the
service's dependencies (`depends_on`, `network_mode: service:…`,
`volumes_from`) in both directions, so nothing is left broken:

| Action | Also affected |
|---|---|
| `start` | Stopped services it needs are started first |
| `stop` | Running services that need it are stopped too |
| `restart` | Running services that need it are restarted too; stopped services it needs are started |
| `update` | Pulls its image and recreates it; stopped services it needs are started; running services that need it are restarted |

gantry hands Compose exactly these services, with `--no-deps`, so what you
confirmed is what happens. If more than the chosen service is affected, the
agent answers `409 confirmation_required` with the plan; send the request
again with `confirm` listing exactly those services:

```bash
curl -X POST -H "Authorization: Bearer $GANTRY_TOKEN" http://127.0.0.1:7070/api/v1/stacks/shop/services/db/actions/restart
# 409 {"code":"confirmation_required","plan":{"service":"db","action":"restart","affected":[
#   {"service":"db","effect":"restart","reason":"target"},
#   {"service":"app","effect":"restart","reason":"dependent"}]},...}

curl -X POST -H "Authorization: Bearer $GANTRY_TOKEN" -H "Content-Type: application/json" \
  -d '{"confirm":["db","app"]}' http://127.0.0.1:7070/api/v1/stacks/shop/services/db/actions/restart
# 202 {"id":"op_…","action":"restart","service":"db","services":["db","app"],...}
```

The plan is worked out again when the action starts; if the services' state
changed in between, you get a new `confirmation_required`. Service actions
run as operations like stack actions, but never cancel a running one: while
an operation runs on the stack, they are rejected with `stack_busy`.

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

## Installation

The agent runs best as a systemd service ([decision 2](docs/decisions.md#2-systemd-service-first-container-optional)); a container image is
available too (see [Running as a container](#running-as-a-container)). The
web UI is a container either way.

**Packages** for Debian and Ubuntu (`.deb`) and Fedora and RHEL (`.rpm`),
amd64 and arm64, are attached to every
[release](https://github.com/pixl-media/gantry/releases). Install Docker with
the Compose plugin first.

```bash
sudo apt install ./gantry-agent_0.2.0_amd64.deb     # Debian, Ubuntu
sudo dnf install ./gantry-agent-0.2.0-1.x86_64.rpm   # Fedora, RHEL
```

The package:

- installs `/usr/bin/gantry-agent` and the `gantry-agent` systemd service,
  which runs as the system user `gantry` with access to Docker;
- creates `/etc/gantry/gantry.env` once, with two random tokens
  (`sudo grep TOKEN /etc/gantry/gantry.env`): `GANTRY_UI_TOKEN` for the web
  login, `GANTRY_TOKEN` for API clients;
- creates `/opt/stacks` for the `gantry` user, unless it exists (then give
  `gantry` write access to the directories, see
  [Write access](#configuration));
- starts the service. It listens on `127.0.0.1:7070`, this host only; see
  [Exposing gantry](#exposing-gantry) before changing that.

Settings go into `/etc/gantry/gantry.env` (variables below), followed by
`sudo systemctl restart gantry-agent`; logs with `journalctl -u gantry-agent`.
To update, install the newer package; the settings stay (see
[Updating](#updating) for the web UI). Removing the package
keeps the settings and the stacks (`apt purge` also removes the settings).

Releases also have plain binaries (`gantry-agent_<version>_linux_<arch>.tar.gz`)
and `checksums.txt`. Every file has signed build provenance:

```bash
gh attestation verify gantry-agent_0.2.0_amd64.deb -R pixl-media/gantry
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `GANTRY_TOKEN` | (none) | API token, only as `Authorization: Bearer` (scripts, curl). Generate it: `openssl rand -hex 24`. The agent refuses tokens shorter than 24 characters, with fewer than 8 different characters, or made of one repeated piece. Without it, bearer access is off; at least one of `GANTRY_TOKEN` and `GANTRY_UI_TOKEN` is required |
| `GANTRY_TOKEN_FILE` | (none) | Instead of `GANTRY_TOKEN`: a file holding the token, e.g. a systemd credential (`LoadCredential=`) or a Docker secret, so it isn't in the process environment |
| `GANTRY_UI_TOKEN` | (none) | Token for the web UI login, only there. Same rules as `GANTRY_TOKEN`, and different from it. Without it, the web login is off |
| `GANTRY_UI_TOKEN_FILE` | (none) | Instead of `GANTRY_UI_TOKEN`: a file holding it |
| `GANTRY_STACKS_DIR` | `/opt/stacks` | Directory with the managed stacks (same default as Dockge) |
| `GANTRY_LISTEN` | `127.0.0.1:7070` (image: `:7070`) | Listen address. The binary only accepts local connections by default; set e.g. `:7070` to reach it from other hosts |
| `GANTRY_TRUSTED_PROXIES` | (none) | Reverse proxies (IP addresses or CIDR prefixes, comma-separated) whose `X-Forwarded-For` and `X-Forwarded-Proto` the agent believes, such as the web UI's container. Then every client behind them has its own login limit |
| `GANTRY_ALLOW_INSECURE_HTTP` | `false` | Accept tokens and session cookies over plain HTTP from internet addresses. Only if you know why: anyone on the way can read them. Also understood by the web UI's container |
| `GANTRY_TLS_CERT` | (none) | PEM certificate (with its chain) for HTTPS on the agent itself, with `GANTRY_TLS_KEY`. Replaced files are picked up without a restart; see [TLS in the agent](#tls-in-the-agent) |
| `GANTRY_TLS_KEY` | (none) | PEM private key for `GANTRY_TLS_CERT`; make it readable only by the agent |
| `GANTRY_PUBLIC_URL` | (none) | Address of the web UI, e.g. `https://gantry.example.com`. If set, cookie-authenticated requests must come from exactly this origin, and with `https` the session cookie is `Secure` and named `__Host-gantry_session`. Required for single sign-on |
| `GANTRY_OIDC_ISSUER` | (none) | Identity provider for single sign-on, exactly as it announces itself, e.g. `https://auth.example.com/application/o/gantry/`. Together with the next two, turns single sign-on on; see [Single sign-on](#single-sign-on-oidc) |
| `GANTRY_OIDC_CLIENT_ID` | (none) | gantry's client ID at the identity provider |
| `GANTRY_OIDC_CLIENT_SECRET` | (none) | Its client secret |
| `GANTRY_OIDC_CLIENT_SECRET_FILE` | (none) | Instead of `GANTRY_OIDC_CLIENT_SECRET`: a file holding it, readable only by the agent |
| `GANTRY_OIDC_ALLOWED_GROUPS` | (none) | Groups whose members may log in (the ID token's `groups` claim), comma-separated. This one or the next is required |
| `GANTRY_OIDC_ALLOWED_EMAILS` | (none) | Email addresses that may log in, comma-separated; only addresses the provider has verified count |
| `GANTRY_OIDC_SCOPES` | `openid profile email` | Scopes to ask the provider for; add `groups` for providers that send groups only then (e.g. Dex) |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | Docker socket (only `unix://` is supported) |

**Two tokens:** if one leaks or lands in the wrong place, replace just that
one: set a new value and restart the agent. Restarting ends all sessions,
including any opened with the old token; whatever uses the other token keeps
working.

The stacks directory uses the same default and layout as Dockge, so gantry
picks up existing Dockge stacks without configuration. If the directory
doesn't exist, the agent still starts, lists unmanaged stacks and reports the
missing directory as a warning.

The agent needs `docker compose` on the host: Docker's Compose plugin
(`docker-compose-plugin` from Docker's packages, `docker-compose-v2` on
Ubuntu). The container image includes it.

**Write access:** the user the agent runs as (`gantry` with the package)
needs write access to `GANTRY_STACKS_DIR` to create and delete stacks, and
to a stack's directory plus read access to its Compose file and `.env` to
edit it. Files are replaced, never written in place, so they need not be
writable themselves. The package creates `/opt/stacks` for `gantry` but
leaves an existing one alone; for a directory from another tool, change the
owner of the directories only:

```sh
sudo chown gantry /opt/stacks /opt/stacks/*/
```

Not `chown -R`: the data in a stack's directory (`./data`, `./config`)
belongs to its containers, which could lose access to it. A stack's page
names what the agent may not read or write, with the command for exactly
that; ACLs work too (`setfacl -m u:gantry:rwx` on a directory, `u:gantry:r`
on a file). gantry never changes owners itself. Without write access, stacks
can still be listed and controlled. Deleting a stack stops at files the
agent may not remove (such as data a container wrote as root); the Compose
file goes last, so the stack stays listed. The container image runs as
root.

**Private registries:** Compose uses the Docker credentials of the user the
agent runs as (`~/.docker/config.json`, from `docker login`); with the
package, that is `/var/lib/gantry/.docker/config.json`. In container
mode, mount that file into the container (e.g.
`/root/.docker/config.json:/root/.docker/config.json:ro`).

### Running as a container

Mount the Docker socket and the stacks directory **at the same path** inside
the container as on the host. Docker resolves relative bind mounts of stacks
(e.g. `./data`) on the host, using the path the agent passes:

```yaml
services:
  gantry-agent:
    image: ghcr.io/pixl-media/gantry-agent:0.2
    environment:
      GANTRY_TOKEN: ${GANTRY_TOKEN}
      GANTRY_UI_TOKEN: ${GANTRY_UI_TOKEN}
      GANTRY_STACKS_DIR: /opt/stacks
      # The web UI below is a trusted proxy: every browser gets its own
      # login limit.
      GANTRY_TRUSTED_PROXIES: 172.30.250.0/24
      # The web UI's address, see "Exposing gantry" below.
      # GANTRY_PUBLIC_URL: https://gantry.example.com
    # No published port: the web UI reaches the agent over the network
    # below. For API clients on other hosts, publish it deliberately, e.g.
    # "127.0.0.1:7070:7070" for this host only.
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /opt/stacks:/opt/stacks
    logging:
      options:
        max-size: 10m
    restart: unless-stopped

  # Optional web UI. It forwards /api to the agent.
  gantry-web:
    image: ghcr.io/pixl-media/gantry-web:0.2
    environment:
      GANTRY_AGENT_URL: http://gantry-agent:7070
    ports:
      # This host only. Docker's published ports bypass host firewalls
      # (ufw, firewalld), so open it to others deliberately, behind HTTPS.
      - "127.0.0.1:8080:8080"
    logging:
      options:
        max-size: 10m
    restart: unless-stopped

networks:
  default:
    ipam:
      config:
        # Fixed, so GANTRY_TRUSTED_PROXIES can name it; pick another range
        # if it collides with one of your networks.
        - subnet: 172.30.250.0/24
```

Image tags: `:0.2` gets the fixes of 0.2.x, `:0.2.0` is that exact
release, `:latest` the newest release, and `:edge` the current `main`
(untested beyond CI).

The web UI speaks plain HTTP on port 8080; `GANTRY_WEB_PORT` changes it
(above 1023, the container doesn't run as root). The web container also
works with an agent running as a systemd service: point `GANTRY_AGENT_URL`
at it, and set the agent's `GANTRY_LISTEN` so the container can reach it. Or
run the container in the host's network, and the agent can stay on
`127.0.0.1`:

```sh
docker run -d --name gantry-web --restart unless-stopped --network host \
  -e GANTRY_AGENT_URL=http://127.0.0.1:7070 -e GANTRY_WEB_PORT=8090 \
  ghcr.io/pixl-media/gantry-web:0.2
```

In the host's network the UI listens on all of the host's addresses, and
the host firewall applies (no published Docker port). Set the agent's
`GANTRY_TRUSTED_PROXIES=127.0.0.1` so that every browser gets its own login
limit.

### Updating

- **The agent as a package:** install the newer package (see
  [Installation](#installation)); settings and stacks stay. The restart ends
  all sessions, so you log in again.
- **The web UI as a stack of its own:** put its Compose file into the stacks
  directory, and *Update* on its page in gantry pulls a newer image and
  recreates the container. With the agent as a service, for example in the
  host's network as above:

  ```yaml
  # /opt/stacks/gantry-web/compose.yaml
  services:
    web:
      image: ghcr.io/pixl-media/gantry-web:0.2
      network_mode: host
      environment:
        GANTRY_AGENT_URL: http://127.0.0.1:7070
        GANTRY_WEB_PORT: "8090"
      restart: unless-stopped
  ```

  If the web UI already runs from `docker run`, remove that container first
  (`docker rm -f gantry-web`), then start the stack. Updating it from the
  web UI itself works: the agent runs the update, the page loses its
  connection for a few seconds, and after reloading it you're still logged
  in, now with the new version. Don't stop or delete this stack from the
  web UI, though: that locks you out of it until you run `docker compose up
  -d` in its directory (the API keeps working).
- **The agent as a container** can't update itself: the update would stop
  the container it runs in, halfway. Update it, and a web UI in the same
  Compose project, outside gantry: `docker compose pull && docker compose
  up -d` in its directory.
- **Tags:** `:0.2` gets the fixes of 0.2.x; *Update* changes nothing if
  there is nothing new, so it is also the check. For the next minor
  release, change the tag (for the web UI, in gantry's Compose editor) and
  update the agent at the same time.
- **New releases:** the bottom of every page shows both versions and warns
  if they are from different releases. To hear about releases, watch the
  repository on GitHub (*Watch* → *Custom* → *Releases*).

### Exposing gantry

The agent controls Docker, which is root on the host.
gantry is built to stay safe even when exposed carelessly, but for access
beyond your own machine:

- **HTTPS is required from the internet.** Tokens and session cookies sent
  over plain HTTP from an internet address are refused (`403
  https_required`), by the agent and by the web UI's container; private
  networks, VPN address space (100.64.0.0/10) and this host are fine. Put
  gantry behind a reverse proxy with HTTPS (e.g. Caddy, Traefik). Proxies in
  private networks are trusted for `X-Forwarded-Proto`, so the session cookie
  is then marked `Secure`.
- Publish ports deliberately: the agent's not at all if only the web UI
  uses it, the web UI's on `127.0.0.1` or behind your proxy. Published
  Docker ports bypass host firewalls.
- Use random tokens (`openssl rand -hex 24`, one for each); the agent
  refuses obviously weak ones and the same token twice. Logins and bearer
  tokens are limited to 10 wrong attempts per minute per client (per /64 for
  IPv6).
- Give it **its own hostname**, such as `gantry.example.com`. Browsers send
  cookies to every port of a host, so with the UI at `http://nas:8080`, the
  session cookie also reaches every other web app on `http://nas:<port>`,
  including the containers gantry manages, and any of them could use it.
- Set `GANTRY_PUBLIC_URL` on the agent to that address. Changing requests from
  the browser must then come from exactly this origin, and with HTTPS the
  cookie is always `Secure` and can't be set by other hosts (`__Host-`).

### TLS in the agent

The agent can encrypt its own connections too: between
your reverse proxy or the web UI's container and the agent, or for API
clients on other hosts. Set `GANTRY_TLS_CERT` and `GANTRY_TLS_KEY` to a PEM
certificate and key. Replaced files (a renewal) take effect with the next
connection, without a restart; files that don't load keep the current
certificate. The agent warns two weeks before the certificate expires. It
doesn't obtain certificates itself: use your own CA, your proxy's
certificates, or a self-signed one, valid for the names clients use (the
web UI below reaches the agent as `gantry-agent`):

```bash
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 825 \
  -subj /CN=gantry -addext "subjectAltName=DNS:gantry-agent,DNS:localhost,IP:127.0.0.1" \
  -keyout key.pem -out cert.pem
```

```yaml
services:
  gantry-agent:
    environment:
      GANTRY_TLS_CERT: /etc/gantry/tls/cert.pem
      GANTRY_TLS_KEY: /etc/gantry/tls/key.pem
    volumes:
      - /etc/gantry/tls:/etc/gantry/tls:ro
  gantry-web:
    environment:
      GANTRY_AGENT_URL: https://gantry-agent:7070
      # Only for a self-signed certificate or your own CA: the web UI trusts
      # just this one (it replaces the container's system CAs).
      SSL_CERT_FILE: /etc/gantry/tls/cert.pem
    volumes:
      - /etc/gantry/tls/cert.pem:/etc/gantry/tls/cert.pem:ro
```

`SSL_CERT_FILE` is read when the web UI's container starts: after replacing a
self-signed certificate, restart that container. With your own CA, point it
at the CA's certificate instead, and renewals need nothing.

Behind a proxy, what counts for `https_required` and the `Secure` cookie is
still the browser's connection (`X-Forwarded-Proto`), not the proxy's. HSTS
stays tied to an `https` `GANTRY_PUBLIC_URL`.

### Single sign-on (OIDC)

Next to the web login token, or instead of it, the web UI can log in
through an identity provider that speaks OpenID Connect, such as authentik,
Keycloak or Dex. Two-factor login and passkeys then come from the provider;
gantry stores nothing.

1. At the provider, create an application for gantry:
   - a confidential client (with a client secret), authorization code flow;
   - redirect URI: `GANTRY_PUBLIC_URL` plus `/api/v1/session/oidc/callback`,
     e.g. `https://gantry.example.com/api/v1/session/oidc/callback`;
   - ID tokens signed with a key pair (RS256, ES256 and the like). In
     authentik, select a signing key: without one, it signs with the client
     secret (HS256), which gantry refuses.
2. Set on the agent, then restart it:

   ```bash
   GANTRY_PUBLIC_URL=https://gantry.example.com
   GANTRY_OIDC_ISSUER=https://auth.example.com/application/o/gantry/
   GANTRY_OIDC_CLIENT_ID=<client ID>
   GANTRY_OIDC_CLIENT_SECRET_FILE=/etc/gantry/oidc-client-secret
   GANTRY_OIDC_ALLOWED_GROUPS=gantry-admins
   ```

The login page then shows *Sign in with single sign-on*.

- **An allow list is required:** only members of a group in
  `GANTRY_OIDC_ALLOWED_GROUPS` or addresses in `GANTRY_OIDC_ALLOWED_EMAILS`
  get in; without either, the agent doesn't start. Everyone on the list can
  do everything, like with the token. Addresses count only if the provider
  marks them verified (`email_verified`); authentik doesn't by default
  (since 2025.10), so use a group there.
- **Claims come from the ID token.** authentik puts the groups in it with
  the `profile` scope ("Include claims in id_token"). Providers that send
  groups only for a `groups` scope, such as Dex, need
  `GANTRY_OIDC_SCOPES=openid profile email groups`.
- **The issuer** must be exactly what the provider announces, trailing slash
  included (authentik's ends with `/`).
- **HTTPS:** the issuer and `GANTRY_PUBLIC_URL` must use `https` (`http`
  only on this host, for tests). For a provider with a certificate from your
  own CA, point the agent's `SSL_CERT_FILE` at the CA's certificate.
- **Sessions** are the same as with the token: 12 hours, ended by logging
  out. Logging out ends the session in gantry, not the one at the provider.
  Taking someone off the allow list takes effect at their next login; to end
  all sessions at once, restart the agent.
- **Keep `GANTRY_UI_TOKEN`** as a way in while the provider is down; without
  it, single sign-on is the only way into the web UI.
- The agent talks to the provider only during logins, and starts without
  it. Its log names who logged in (`single sign-on login`), who was refused,
  and the user of every change (`user=`).

## Layout

| Path | What |
|---|---|
| `agent/` | Go agent: its own Go module and Dockerfile |
| `api/openapi.yaml` | API contract; Go code is generated from it |
| `web/` | Optional web UI: React, Vite, MUI; Dockerfile and Caddyfile |
| `dev/stacks/` | Example stacks for local development |
| `docs/` | Architecture decisions; the logo in `docs/logo/` |
| `compose.yaml` | Local development with agent and web UI as containers |
| `api.http` | Example requests for the VS Code REST Client |
| `agent/tools/devdocs/` | Dev-only Swagger UI server (`make docs`) |
| `.github/workflows/ci.yml` | Tests, image builds, packages and releases |
| `packaging/` | systemd unit, settings template and scripts of the `.deb`/`.rpm` packages |
| `.goreleaser.yaml`, `cliff.toml` | Release binaries and packages (GoReleaser); release notes (git-cliff) |
| `CHANGELOG.md` | Merged pull requests per release (`make changelog`) |
| `SECURITY.md` | How to report a vulnerability |
| `AGENTS.md`, `agent/AGENTS.md`, `web/AGENTS.md` | Conventions for contributors and coding agents |
| `.vscode/extensions.json` | Recommended VS Code extensions |
| `LICENSE` | GNU AGPLv3 |

## Development

Requirements: Go 1.27+, Docker with the Compose plugin; for the web UI
Node 24 or newer (CI and the image use Node 26 LTS) and pnpm 10.

```bash
make dev-env           # create .env with random tokens, or add a missing one
make run               # agent on http://127.0.0.1:7070, tokens from .env, managing dev/stacks
make web-install       # once: install the web UI's dependencies
make web               # web UI on http://127.0.0.1:5173 (in a second terminal), log in with GANTRY_UI_TOKEN from .env
make docs              # Swagger UI on http://127.0.0.1:8082 (in a second terminal)
make test              # unit tests, no Docker needed
make test-integration  # unit + integration tests against the local Docker daemon
make lint              # gofmt + go vet
make generate          # regenerate Go code after changing api/openapi.yaml
make build             # build the agent binary into agent/bin/
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

`make run` takes both tokens from `.env` (git-ignored) and creates that file
with random tokens on first use, or adds a missing one (`make dev-env`), so
the dev agent never runs with a token anyone could guess. The agent refuses
tokens shorter than 24 characters.

Agent and web UI in containers: `make dev-env && docker compose up --build`
(run from the repo root), then open http://127.0.0.1:8080 and log in with
`GANTRY_UI_TOKEN` from `.env`.

Integration tests create their own stacks named `gantry-test-…` and remove
them afterwards; other containers on the host are not touched.

### API docs

`make docs` serves Swagger UI for `api/openapi.yaml` on
http://127.0.0.1:8082 and forwards API requests to the agent on port 7070
(`make run` or `docker compose up`). Everything comes from one address, so
"Try it out" works without CORS: click "Authorize", enter the API token
(`GANTRY_TOKEN`, from `.env` for `make run`), and send requests. The spec is re-read on every page load.
This is a development tool only; the agent doesn't serve docs itself.

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
- No user accounts or roles: the web login token is shared, and everyone on
  the single sign-on allow list has the same rights
  ([decision 25](docs/decisions.md#25-scope-a-lightweight-manager-for-one-host)).
- Files edited directly on the host (not through gantry) show up in the web
  UI within a minute; Docker reports no event for them.
- Stacks can't be renamed (named volumes carry the project name).
- There is no history or undo for Compose files; validation and version
  checks protect against mistakes and lost updates.
- Validation checks the structure of a Compose file, not whether images
  exist or variables are set.
- Logs exist only as long as Docker keeps them; with log drivers such as
  `none` or `syslog` Docker returns none.
- The web UI keeps at most 10,000 log lines and searches only those, in the
  browser.

## Non-goals

gantry stays a lightweight manager for one host: no database or files of its
own, only what Docker and Compose can do, no background calls to outside
services. Several hosts in one UI, user accounts and roles, resource
usage, a history of Compose files, a console in containers and "update
available" hints are out of scope ([decision 25](docs/decisions.md#25-scope-a-lightweight-manager-for-one-host)).

For CPU, memory and network usage per container, use a tool such as
[cAdvisor](https://github.com/google/cadvisor) next to gantry. Its metrics
carry the Compose project and service labels, so they can be summed per
stack. Like gantry, it should not be reachable from the internet.

## Workflow and releases

GitHub Flow: `main` is always releasable. Work happens on short-lived branches
(`feature/…`, `fix/…`, `chore/…`) that are merged into `main` via pull request.

| Event | CI |
|---|---|
| Pull request | Go and web tests, integration tests, both images built and smoke-tested, packages built and the `.deb` installed with systemd; nothing published |
| Merge to `main` | same, then `ghcr.io/pixl-media/gantry-agent:edge` and `gantry-web:edge` are pushed |
| Tag `vX.Y.Z` | same, then `:X.Y.Z`, `:X.Y`, `:latest` of both images are pushed, and a GitHub release with notes, binaries, `.deb`/`.rpm` packages and checksums is created |

Release:

1. Test `:edge`.
2. A release pull request with the changelog: `make changelog TAG=v0.1.0`
   writes `CHANGELOG.md`, one line per merged pull request, grouped by branch
   prefix (git-cliff, `cliff.toml`).
3. After merging it: `git tag v0.1.0 && git push origin v0.1.0`. The release
   notes are that version's part of the changelog.

`make release-snapshot` builds the binaries and packages locally into `dist/`
(GoReleaser in Docker), without releasing anything.

GitHub Actions are pinned by commit and base images by digest; Dependabot
keeps both current. Pushed images carry signed build provenance (for the
public repository), which `gh attestation verify` checks:

```bash
gh attestation verify oci://ghcr.io/pixl-media/gantry-agent:edge -R pixl-media/gantry
```

## License

gantry is licensed under the [GNU Affero General Public License v3.0](LICENSE)
(`AGPL-3.0-only`). You may use, modify and share it, also commercially.
If you distribute a modified version, or let others use a modified version
over a network, you have to offer its source code under the same license.

Issues and ideas are welcome. Code contributions are not accepted for now.
