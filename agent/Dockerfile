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

FROM alpine:3.24
COPY --from=build /out/gantry-agent /usr/local/bin/gantry-agent
EXPOSE 7070
ENTRYPOINT ["gantry-agent"]
