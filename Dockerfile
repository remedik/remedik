# syntax=docker/dockerfile:1

# Build stage. Dependencies always come from the module proxy, never from a
# local vendor directory (.dockerignore excludes it), so the image builds
# identically on a laptop and in CI.
# The Go version tracks go.mod. It is an argument so a build can pin or
# substitute it without editing this file:
#   docker build --build-arg GO_IMAGE=golang:1.26.6-alpine .
ARG GO_IMAGE=golang:1.26-alpine
FROM ${GO_IMAGE} AS build

ARG VERSION=dev
WORKDIR /src

# Dependency manifests first: this layer is cached until they change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/ratyx/remedik/internal/version.version=${VERSION}" \
      -o /out/remedik ./cmd/remedik

# Runtime stage: distroless, non-root, no shell. There is nothing in this
# image to exec into, which is the point — an operator with cluster write
# access should present the smallest possible attack surface.
ARG RUNTIME_IMAGE=gcr.io/distroless/static:nonroot
FROM ${RUNTIME_IMAGE}

WORKDIR /
COPY --from=build /out/remedik /remedik

# 65532 is distroless' "nonroot" user.
USER 65532:65532

ENTRYPOINT ["/remedik"]
