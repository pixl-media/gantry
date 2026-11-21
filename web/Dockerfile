# syntax=docker/dockerfile:1

# Base images are pinned by digest; Dependabot updates tag and digest
# together.

# The static files are the same for every platform: build them once, on the
# native platform, without emulation.
FROM --platform=$BUILDPLATFORM node:26-alpine@sha256:6bd9ab43c0ec0190ca7b2d193906cb8b7edf6e7f4afa43ddeccd6452710bffb5 AS build
WORKDIR /app
RUN npm install -g pnpm@10.18.3
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
# Shown in the web UI next to the agent's version; CI passes the release.
ARG VERSION=dev
RUN GANTRY_VERSION=${VERSION} pnpm build

# Caddy serves the app and forwards /api to the agent. No RUN steps here, so
# multi-arch builds need no emulation. It runs as nobody: port 8080 needs no
# root, and it stores nothing (no TLS, no saved config); whatever Caddy may
# still write goes to /tmp.
FROM caddy:2.11-alpine@sha256:003a97808e781aa9f7e75cc2f43d34d461099031faf5e20faefb9f61126836f0
COPY Caddyfile /etc/caddy/Caddyfile
COPY --from=build /app/dist /srv
ENV XDG_CONFIG_HOME=/tmp/caddy/config XDG_DATA_HOME=/tmp/caddy/data
USER 65534:65534
EXPOSE 8080
