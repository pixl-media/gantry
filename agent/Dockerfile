# syntax=docker/dockerfile:1

# Build on the native platform and cross-compile, so multi-arch builds
# don't need QEMU emulation.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/gantry-agent ./cmd/gantry-agent

# The agent runs `docker compose`, so the image carries the Docker CLI and
# the Compose plugin. Copied from the official image for the target platform;
# no RUN steps in the final stage, so no emulation is needed.
FROM docker:29-cli AS docker-cli

FROM alpine:3.24
COPY --from=docker-cli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=docker-cli /usr/local/libexec/docker/cli-plugins/docker-compose /usr/local/libexec/docker/cli-plugins/docker-compose
COPY --from=build /out/gantry-agent /usr/local/bin/gantry-agent
# In a container the agent listens on all of its interfaces (the binary's
# default is 127.0.0.1); which of them are reachable is decided by the ports
# you publish.
ENV GANTRY_LISTEN=:7070
EXPOSE 7070
ENTRYPOINT ["gantry-agent"]
