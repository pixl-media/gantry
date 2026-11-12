# syntax=docker/dockerfile:1

# The static files are the same for every platform: build them once, on the
# native platform, without emulation.
FROM --platform=$BUILDPLATFORM node:24-alpine AS build
WORKDIR /app
RUN npm install -g pnpm@10.18.3
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
RUN pnpm build

# Caddy serves the app and forwards /api to the agent. No RUN steps here, so
# multi-arch builds need no emulation.
FROM caddy:2.11-alpine
COPY Caddyfile /etc/caddy/Caddyfile
COPY --from=build /app/dist /srv
EXPOSE 8080
