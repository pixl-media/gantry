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
  which a TypeScript client is generated and shared by every consumer, such
  as the gantry web UI.
- Consumers that manage many agents keep agent tokens server-side; browsers
  never receive agent tokens from such an app.

## 5. Web UI: React + Vite SPA

**Decided 2026-11-12.** The optional web UI is a static React SPA built with
Vite, served by a small web server container that proxies `/api` to the agent.

- The agent is the backend, so the UI needs no server runtime.
- Next.js was considered, but its server features are unused here, and a
  static export can't serve runtime dynamic routes such as `/stacks/[name]`.
- The UI is built after the API, once its scope is clear.

Details on the stack and the login: decisions 14 and 15.

## 6. Standalone and publishable

**Decided 2026-11-12.** gantry must be fully usable on its own (agent plus
optional UI), without any other system, and stay publishable as
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

Actions (decision 13) work on managed stacks only; unmanaged stacks stay
read-only, so gantry never changes what it doesn't own. Taking over an
existing stack would be a deliberate feature of its own.

## 9. OpenAPI spec-first with oapi-codegen

**Decided 2026-11-12.** `api/openapi.yaml` is the contract. oapi-codegen
generates Go types and a strict server interface for `net/http` into
`agent/internal/api/api.gen.go`; handlers implement that interface, so code
that drifts from the spec doesn't compile. CI fails if the generated code is
out of date. API clients (such as the web UI) are generated from the same
file.

Rejected: hand-written spec and handlers (drift only caught by tests, types
maintained twice) and code-first (the contract becomes a by-product, and it
ties us to a framework).

## 10. The API never exposes secrets from stacks

**Decided 2026-11-12.** Stack details return the Compose file exactly as
stored (`${DB_PASSWORD}` stays unresolved). The API never returns the resolved
configuration (`docker compose config` output) or `.env` contents, and the
agent reads service names with `--no-interpolate`. Managing `.env` values
needs its own feature with its own protection.

*Amended by decision 18:* single `.env` values are available on explicit
request through their own endpoint.

## 11. `/healthz` is a liveness check

**Decided 2026-11-12.** `/healthz` (public) returns 200 as long as the agent
serves requests, also when Docker is down, so monitors don't restart the agent
while it reports the outage. Docker's state is part of the API responses
(`dockerAvailable`). The `z` follows the Go/Kubernetes convention and avoids
collisions with application routes.

## 12. Actions run as asynchronous operations

**Decided 2026-11-12.** Stack actions can take minutes (image pulls), so a
request starts an operation and returns `202` at once. Status and output are
available via `GET /api/v1/operations/{id}`, and live via Server-Sent Events
(`/events`), the pattern the web UI builds on. Long-running work never runs
synchronously inside a request.

- **One operation per stack.** `start`, `restart` and `update` are rejected
  with `409 stack_busy` while another runs; queuing would hide what's pending.
- **`stop` and `down` take precedence** as an emergency brake: they cancel
  the running operation, wait for it to end, then run. A new operation is
  marked active immediately, so two Compose processes never work on the same
  stack. Interrupting Compose doesn't undo Docker calls it has already made
  (Docker finishes them), so after canceling, `stop`/`down` run their command
  a second time to settle such late changes.
- **Cancel** interrupts Compose like Ctrl+C (SIGINT to its process group) and
  kills it after 15 seconds. Compose is declarative, so the next `start` or
  `stop` converges a partly changed stack.
- 30 minutes per operation; output limited to 1 MiB (the end is kept).
- Kept in memory for one hour: the agent stays stateless. The stacks' state
  comes from Docker, so nothing important is lost on restart.

Rejected: synchronous requests (timeouts behind proxies, no progress), a
queue (hidden pending work) and "any action cancels the running one"
(clients could cancel each other's updates by accident).

## 13. What the actions do

**Decided 2026-11-12.** All Compose calls pass the project explicitly
(`-p <name> -f <file> --project-directory <dir>`) and use plain, uncolored
output.

| Action | Compose |
|---|---|
| `start` | `up -d --remove-orphans` |
| `stop` | `stop` |
| `restart` | `stop`, then `start` (doesn't apply Compose file changes) |
| `update` | `pull --ignore-buildable`, then `up -d --remove-orphans` |
| `down` | `down --remove-orphans` |

`restart` deliberately isn't `docker compose restart`: Docker runs a restart
as one job (stop, then start) and completes it even after Compose is
interrupted. In a test, a `stop` that preempted a slow restart was undone
seconds later by the restart's start. Two separate commands stop after the
current step.

`down` never uses `-v`: removing volumes deletes data and would need its
own, explicitly confirmed action. *That action is `delete` (decision 19).*

*Actions on single services: decision 23.*

## 14. Web UI login: a session cookie from the agent

**Decided 2026-11-12.** The agent token is root-equivalent on the host, so it
must not sit in JavaScript-readable browser storage, where an XSS bug could
read it. The UI sends the token once to `POST /api/v1/session`; the agent
answers with a `gantry_session` cookie:

- `HttpOnly` (invisible to JavaScript), `SameSite=Strict` (not sent from other
  sites), `Secure` behind HTTPS, valid 12 hours.
- Stateless: the value is the expiry plus an HMAC keyed with a key derived
  from the token. Sessions survive agent restarts; changing the token ends
  all of them. Logout clears the cookie.
- **CSRF:** with cookie auth, POST/DELETE must carry an `Origin` matching the
  host, in addition to `SameSite=Strict`. Logins from other sites are
  rejected too.
- At most 10 failed logins per minute and client address (`429` with
  `Retry-After`). Behind the UI's proxy that is one shared limit, which is
  fine for a single shared token. The agent warns about tokens shorter than
  24 characters.
- API clients keep using the bearer token. The middleware denies by
  default; public routes are listed explicitly as method plus path.
- The cookie makes the browser's plain `EventSource` work for live output.

Rejected: the token in `localStorage` (simplest, as Portainer and Dockge do,
but exposed to any XSS) and Basic Auth at the proxy (no login page, no
logout, the UI container would need the token).

*Amended by decision 20:* sessions now live in the agent's memory (logout
really ends them), wrong bearer tokens count against the same limit, tokens
shorter than 16 characters are refused, and `GANTRY_PUBLIC_URL` can pin the
cookie's origin. *Amended by decision 26:* the login takes its own token,
`GANTRY_UI_TOKEN`, not the API's.

## 15. Web UI stack and container

**Decided 2026-11-12.**

- **React 19 + Vite + MUI v9.** TanStack Query for
  data (reloaded on live changes, decision 24), React Router, react-i18next with English
  and German from the start (typed keys, a test keeps both in sync).
- **API types generated** from `api/openapi.yaml` with openapi-typescript;
  `openapi-fetch`/`openapi-react-query` call the API type-safely. CI fails if
  the generated file is out of date, like on the Go side.
- **Container:** Caddy serves the static build and forwards `/api` to the
  agent (`GANTRY_AGENT_URL`), so app and API share one origin: no CORS, and
  the cookie works. Hashed assets are cached as immutable.
- **Content-Security-Policy** as a second line of defense against XSS: only
  the app's own scripts, data only to its own origin, no framing.
  `style-src 'unsafe-inline'` is needed because MUI injects styles at
  runtime. No external fonts.
- TypeScript 6 rather than the new native TypeScript 7, until the ESLint
  tooling supports it.

## 16. Container logs: merged per stack, from the Engine API

**Decided 2026-11-12.** `GET /api/v1/stacks/{name}/logs` streams the logs of
all containers of a stack as Server-Sent Events, like `docker compose logs
-f`.

- **Engine API, not the Compose CLI:** logs are container data (decision 7).
  One HTTP stream per container instead of a `docker compose logs` process
  per viewer, and stdout/stderr arrive separately. The agent decodes
  Docker's multiplexed frames itself (standard library only).
- **Merged in the agent:** first the last `tail` lines of every container
  sorted by time, then live lines in arrival order. A watcher (every 2 s)
  re-attaches to containers that are started or recreated, continuing with
  `since` right after the last line sent, so restarts don't break the stream
  or repeat lines.
- **Unmanaged stacks can be read:** reading changes nothing (decision 8).
- **Unfiltered:** lines come as written, including ANSI codes; the web UI
  renders colors with its own small parser and strips other control
  sequences. Logs may contain whatever programs print; they are only
  available to authenticated clients.
- The end-of-stream error event is called `failed`, because browsers'
  `EventSource` uses `error` for connection errors.
- In the browser, lines are applied at most once per animation frame and
  capped at 10,000, so chatty containers don't slow the UI down.

## 17. Editing stack files

**Decided 2026-11-13.** Managed stacks can be created and their
`compose.yaml` and `.env` edited through the API; unmanaged stacks stay
read-only (decision 8). All file access goes through `internal/stackfiles`.

- **Validated before saving:** a new Compose file is written to a temporary
  file next to the original and checked with `docker compose config --quiet
  --no-interpolate` (with the stack's project directory, so relative paths
  and `env_file` resolve). On error the stored file stays untouched and the
  API answers `400 invalid_compose` with Compose's message. The check is
  structural: it doesn't pull images or require variables to be set.
- **Atomic writes:** temporary file in the same directory, `fsync`, then
  `rename`. A crash leaves either the old or the new file, never half of
  one. File modes are kept; new `.env` files get `0600`.
- **Versions against lost updates:** the version of a file is the SHA-256 of
  its content. A save carries the version it was based on; if the file has
  changed since (another tab, an edit over SSH), the API answers
  `409 version_conflict` instead of overwriting. Saves to one stack are
  serialized by a mutex.
- **Limits:** files up to 1 MiB; symlinks are never edited or followed
  (since decision 20 a symlinked stack directory isn't managed at all), so
  a link can't redirect a write outside the stacks directory.
- **`.env` keeps its layout:** comments, blank lines, order and `export`
  prefixes stay; changed keys are updated in place, new ones appended.
  Values are quoted only when needed, read back with Compose's own quoting
  rules (an integration test round-trips tricky values through the real
  `docker compose`). Keys must match `^[A-Za-z_][A-Za-z0-9_]*$`; values
  with line breaks are rejected.
- **Creating a stack** builds it in a hidden staging directory in the stacks
  directory, validates it, then renames it into place. A name that is
  already a directory or an existing Compose project (also an unmanaged
  one) is refused with `409 stack_exists`.
- Every change is logged (stack, file, keys), never with values.

Not in this version: renaming stacks (volumes are prefixed with the project
name), a history or undo for Compose files.

## 18. `.env` values only on request

**Decided 2026-11-13.** `.env` files hold secrets, but editing them needs
their values. `GET /api/v1/stacks/{name}/env` lists only the names; a single
value is returned by `POST /api/v1/stacks/{name}/env/reveal` when a user
explicitly asks for it (the eye or edit button in the UI).

- `POST`, so browsers and proxies never prefetch or cache it, and with
  cookie auth the CSRF `Origin` check applies. All `/api/` responses are
  sent with `Cache-Control: no-store`.
- Each reveal is logged with stack, key and client, never the value.
- The web UI keeps revealed values in memory only while the tab is shown,
  never in browser storage, and drops them again on a second click.

Rejected: returning all values with the list (every page view would expose
every secret) and not supporting `.env` at all (users would edit it over
SSH, which this tool is meant to replace).

## 19. Deleting a stack

**Decided 2026-11-13.** `down` keeps removing only containers and networks.
Deleting a stack is a separate, explicit action:
`DELETE /api/v1/stacks/{name}?confirm=<name>&volumes=false`.

- It runs as an operation (`delete`) that takes precedence like `stop` and
  `down`: first `docker compose down --remove-orphans`, then the stack
  directory is removed **permanently**, including bind-mounted data such as
  `./data`. If `down` fails, the files stay.
- **Named volumes** are removed only with `volumes=true` (`down --volumes`);
  this is the only place gantry ever passes `-v`. The stack details list the
  project's volumes so the UI can show what would be lost.
- `confirm` must repeat the stack name, a second safeguard next to the UI,
  where the user types the name and opts in to deleting volumes (off by
  default).
- Only managed stacks can be deleted; the directory must be a real
  directory, not a symlink.

Rejected: a trash folder (data would pile up unnoticed on small hosts;
backups are the place for recovery) and deleting volumes by default.

## 20. Hardening after the first security review

**Decided 2026-11-16**, after an independent review of the agent and the
web UI. None of the findings was an authentication bypass; these close the
gaps at the edges.

- **Request limits:** bodies at most 4 MiB, 4 KiB for the public login,
  enforced before anything decodes them (`413 request_too_large`); the server
  has read and idle timeouts. Responses may still run long: event streams
  are unaffected.
- **Sessions in memory** (replaces the stateless cookie of decision 14):
  256-bit random IDs, stored only as SHA-256. Logging out ends a session at
  once, so a captured cookie can be revoked; restarting the agent ends all
  sessions. A re-login after updates is the price; a credential that can't
  be revoked would be worse.
- **One limit for token guessing:** wrong tokens from logins and bearer
  headers count together, 10 per minute and client, checked and counted
  atomically. While blocked, the right token is refused too; letting it
  through would tell the guesser which guess was right. Behind the web UI's
  proxy all browsers still share one limit.
- **Tokens:** shorter than 16 characters, the agent doesn't start. Local
  development uses a random token in the git-ignored `.env` instead of the
  well-known `dev`.
- **`GANTRY_PUBLIC_URL`** (optional): cookie requests must come from exactly
  this origin, and with HTTPS the cookie is always `Secure` and named
  `__Host-gantry_session`. Cookies are scoped to a host, not a port, so the
  docs recommend giving the UI its own hostname: on `http://nas:8080` the
  cookie would reach every web app on that host, including managed stacks.
- **Symlinks:** a stack directory or Compose file that is a symbolic link
  isn't managed (its containers show as an unmanaged stack). All file
  access goes through `os.Root`, and opened files are checked against what
  was checked, so swapping in a link meanwhile doesn't help.
- **Compose's environment** never contains `GANTRY_*` variables: Compose
  interpolates from it, so a Compose file could otherwise read the token.
- **Errors:** creating a stack while Docker is unreachable gets `503
  docker_unavailable` (an existing project of that name can't be ruled
  out). Compose's raw errors aren't shown in the stack list, where they
  could quote `.env` lines on every refresh. Unexpected errors (500) go to
  the log, not to the client.
- **Smaller:** bounded output and log buffers, `no-store` for event streams,
  `nosniff`, no secrets lingering in the UI's mutation cache, Caddy running
  as `nobody`.

Not done yet: per-browser login limits behind the proxy (would need trusted
proxy settings) and pinning images and actions by digest. *Extended by
decision 21.*

## 21. Safe even when exposed to the internet

**Decided 2026-11-16**, after a second independent review that asked what
happens when someone puts the agent or the web UI on the internet directly.
gantry should stay safe even then; the operator's mistakes shouldn't be
enough to lose the host.

- **No credentials over plain HTTP from the internet.** Tokens and session
  cookies from an internet address over plain HTTP are refused with `403
  https_required`, before they are checked, by the agent and by the web UI's
  Caddy. Private networks, VPN address space (100.64.0.0/10) and loopback are
  allowed, as is HTTPS (directly or via `X-Forwarded-Proto` from a trusted or
  private proxy). `GANTRY_ALLOW_INSECURE_HTTP=true` turns it off. Refusing
  can't stop the first request from crossing the wire, but no such setup ever
  works, so nobody relies on it.
- **Only this host by default:** the binary listens on `127.0.0.1:7070`; the
  image sets `:7070` (inside a container, the published ports decide). The
  README's compose example publishes no agent port and the web UI on
  `127.0.0.1` only, because Docker's published ports bypass host firewalls.
- **Strong tokens:** at least 24 characters, at least 8 different ones, not
  one repeated piece; `GANTRY_TOKEN_FILE` keeps it out of the environment.
  With a random token, guessing stays hopeless even for a botnet that the
  per-client limit can't stop.
- **The limit itself:** IPv6 clients count per /64; at most 10,000 tracked
  keys, then one shared bucket (during an attack from that many addresses,
  new clients may have to wait; guessing stays limited); pruning now and
  then, not per attempt. Only the `Bearer` scheme is a token attempt.
  `GANTRY_TRUSTED_PROXIES` names proxies whose `X-Forwarded-For` counts, so
  every browser behind the web UI has its own limit. No proxy is trusted by
  default: behind NAT that would let anyone forge their address.
- **Less to exhaust:** at most 1024 connections, 128 per client (proxies
  only count toward the total), 16 KiB of headers; failed attempts are
  logged one by one up to 10 a minute, then summarized.
- **Smaller:** token comparison by SHA-256 digest (no length leak), the
  Origin check compares the scheme over HTTPS, HSTS with an https public URL,
  event streams end with the session that opened them.

Not done: TLS in the agent itself (a proxy does it, and the refusal above
already keeps plain HTTP from working; planned for v0.1, see decision 25)
and pinning images and actions by digest (backlog). A global failure budget
was dropped: with enforced strong tokens it protects nothing.

## 22. License: AGPLv3

**Decided 2026-11-16.** gantry is licensed under the GNU AGPLv3
(`AGPL-3.0-only`, full text in `LICENSE`).

- **AGPL, not GPL:** gantry is a network service. Under the GPL, a modified
  version offered as a hosted service never counts as distributed, so its
  source could stay closed. The AGPL requires offering the source to the
  service's users too. Plain use, also commercial and internal, has no
  obligations under either.
- **Not MIT or Apache-2.0:** they would allow closed products and services
  built on gantry without anything in return.
- **`-only`, not `-or-later`:** a later AGPL version only applies if the
  copyright holder chooses it.
- **No outside code contributions for now:** all code stays with the
  copyright holder, which keeps the licensing in one hand. Contributions
  would need a contributor license agreement first. Dependency updates
  (version numbers only) are unaffected.
- **Dependencies:** everything built into the agent binary and the web UI
  is under MIT, BSD, ISC or Apache-2.0, which are compatible with AGPLv3.
  Development tools (linters, generators) aren't shipped and don't matter
  here.
- API clients are unaffected: talking to gantry over its HTTP API doesn't
  make them derived works.
- Open point: much of the code was written with an AI assistant, and how
  far such code is protected by copyright is legally unsettled.

## 23. Actions on single services

**Decided 2026-11-16.** `POST /api/v1/stacks/{name}/services/{service}/actions/{action}`
runs `start`, `stop`, `restart` or `update` on one service of a managed
stack, as an operation.

- **The dependency chain in both directions.** Starting a service starts
  the stopped services it needs; stopping it stops the running services that
  need it (they would break); restarting or updating it restarts them, so
  they reconnect. Dependencies are `depends_on`, `network_mode: service:…`
  and `volumes_from`, as for Compose.

  | Action | Compose |
  |---|---|
  | `start` | `up -d --no-deps <stopped dependencies> <service>` |
  | `stop` | `stop <service> <running dependents>` |
  | `restart` | `stop <service> <dependents>`, `up -d --no-deps <stopped dependencies>` if any, `start <service> <dependents>` |
  | `update` | `pull --ignore-buildable <service>`, `up -d --no-deps <stopped dependencies> <service>`, then `stop` and `up -d --no-deps` of the running dependents |

  Dependents are brought up with `up` after an update, not `start`: if they
  share the service's network namespace or volumes, their containers must
  be recreated for the new one. `restart` stays `stop` + `start`, for the
  reasons in decision 13.
- **gantry works out the set, not Compose.** Compose's own handling of
  dependencies differs between commands and versions. gantry reads the graph
  with `docker compose config --format json --no-interpolate` (keeping only
  names and dependencies, decision 10), takes the running state from the
  Engine API, and passes every affected service explicitly with `--no-deps`.
  What was shown is exactly what runs.
- **Confirmation without a race.** If more than the chosen service is
  affected, the agent answers `409 confirmation_required` with the plan. The
  request is repeated with `confirm`, the exact set of services; the agent
  works out the plan again and runs it only if the set still matches. API
  clients get the same protection as the web UI.
- **Never more than that.** Service actions never use `-v`, `--volumes` or
  `--remove-orphans` (project-wide), and `down` has no service variant. The
  operations manager rejects such commands as a second line of defense.
- **No precedence.** One operation per stack still applies. A service
  action is rejected with `stack_busy` while any operation runs, even a
  service `stop`; the stack's `stop`, `down` and `delete` cancel a running
  service action as before.
- Unmanaged stacks have no service actions, like stack actions (decision 8).

## 24. Live stack changes instead of polling

**Decided 2026-11-16.** The web UI polled the stack list, the open stack and
its operations every 5 seconds, per open tab, and every list request ran
`docker compose config` once per managed stack.

- **`GET /api/v1/events`** is a Server-Sent Events stream that only names
  the stacks that changed (`changed`, or `all: true`); clients reload them
  with the usual endpoints. The stream carries no state, so it needs no
  rules of its own about what a client may see, and the endpoints stay the
  single source of truth. `ready` on every (re)connect means "reload
  everything", since changes may have been missed. Changes are collected
  for 250 ms, so starting a stack (many container events) is one event. A
  `ping` event every 25 seconds keeps proxies from closing it; it is an
  event, not a comment, because EventSource hides comments and the web UI
  uses the pings to notice a connection that died without an error (sleep,
  network change, a proxy keeping the client side open): after 70 seconds of
  silence it starts over. It ends with the session, like the other streams,
  and all event streams end when the agent shuts down instead of holding up
  its exit.
- **Sources:** Docker's container events for Compose projects (label
  filter), so changes made outside gantry show up too; and the agent
  itself: operations starting and finishing, and files saved through the
  API (Docker sends nothing for those). Files edited directly on the host
  cause no event.
- **Docker's event stream is followed only while someone listens**, one
  connection for all clients. When it breaks, the agent reconnects with a
  wait growing from 1 to 30 seconds and reports `all` (once per outage, and
  on reconnect), since events may have been missed.
- **Slow clients can't block it:** each subscriber's pending changes are
  merged into one set until it reads them.
- **Polling stays as a safety net:** once a minute while the stream works
  (for edits on the host), every 5 seconds while it is down.
- **Path:** `/api/v1/events`, not `/api/v1/stacks/events`, which would hide
  a stack named `events`.
- **Cached Compose reads:** each managed stack's services and dependencies
  (decision 23) are cached, keyed by the Compose file's and `.env`'s size
  and modification time (`.env` can set `COMPOSE_PROFILES`), for at most a
  minute (for files they pull in, like `include:` or `env_file`). Saving
  through the API drops the entry at once; errors aren't cached. Service
  action plans always read the files fresh. A file watcher (inotify) was
  rejected: not in the standard library, and the key plus expiry is enough.

## 25. Scope: a lightweight manager for one host

**Decided 2026-11-17**, after reviewing the backlog: gantry stays small.

- **What lightweight means here:**
  - **No state of its own:** no database and no files of its own. The stack
    folders and Docker are the truth; sessions and operations live in
    memory.
  - **One host per agent.**
  - **Only what Docker and Compose can do**, made safe to use over an API
    and a web UI.
  - **No background calls to outside services.**

  An exception to these needs a decision of its own.
- **In scope:** packaging, release notes, supply chain (pinning, signing),
  TLS in the agent, separate tokens for the API and the web login, an
  optional OIDC login after v0.1 (it stores nothing in gantry), UI polish,
  splitting the web bundle.
- **Not in gantry:** several hosts in one UI, user accounts and roles,
  resource usage and its history, a history of Compose files, a console in
  containers, renaming stacks with their volumes.
- **Dropped:**
  - **"Update available" hints for images:** with `latest`, the update
    action pulls and recreates only what changed; with pinned versions,
    picking the next one is a decision of its own. Checking registries in
    the background would need their credentials and run into rate limits.
  - **A global failure budget** (decision 21): tokens are random and long.
  - **A web UI password with TOTP:** a password isn't safer than the token;
    only a second factor is, and setting one up in the browser needs stored
    state. An identity provider (OIDC) gives that without state. Revisit
    only for a concrete need on servers without one.

## 26. Separate tokens for the API and the web login

**Decided 2026-11-17.** One token served API clients and the web login. A
token copied into the wrong place then had to be replaced everywhere,
scripts included.

- **`GANTRY_TOKEN`** works only as `Authorization: Bearer` (API clients,
  scripts); **`GANTRY_UI_TOKEN`** only at `POST /api/v1/session`. Neither is
  accepted in the other's place; using the wrong one is a failed attempt
  and counts against the same limit.
- **Each is optional**, which turns its way in off: without a web login
  token the login answers `403 login_disabled` and no session exists;
  without an API token, bearer headers never work. At least one is needed,
  both follow the rules of decision 21 (also `_FILE`), and they must differ,
  or the agent doesn't start.
- **Replacing one** is a new value and a restart. The restart ends all
  sessions, including any opened with the old token.
- **Audit:** the auth middleware logs every changing request with method,
  path, client and `via=token|session`, never its body, so a log shows
  whether a change came from a browser or a script.
- **Same power:** a session can do everything a bearer token can. The split
  makes replacing and tracing easier, not a leaked token less dangerous;
  only a second factor would (decision 25).
- Rejected: a web UI password instead of a second token (weaker than a
  random token, decision 25), and accepting the API token at the login as a
  fallback (it would quietly undo the split).

## Open questions

- **Release distribution:** GoReleaser for binaries, `.deb`/`.rpm` and images.
  While the repo is private, downloads need a token.
