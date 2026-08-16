# syntax=docker/dockerfile:1

# Base images are build arguments so a build can pin or substitute them
# without editing this file:
#   docker build --build-arg GO_IMAGE=golang:1.26.6-alpine .
#
# Both are declared here, before any FROM: an ARG used in a FROM
# instruction must live in the global scope, because arguments declared
# inside a stage are not visible to the next stage's FROM.
ARG GO_IMAGE=golang:1.26-alpine
ARG RUNTIME_IMAGE=gcr.io/distroless/static:nonroot

# Build stage. Dependencies always come from the module proxy, never from a
# local vendor directory (.dockerignore excludes it), so the image builds
# identically on a laptop and in CI.
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
      -ldflags "-s -w -X github.com/remedik/remedik/internal/version.version=${VERSION}" \
      -o /out/remedik ./cmd/remedik

# Runtime stage: distroless, non-root, no shell. There is nothing in this
# image to exec into, which is the point — an operator with cluster write
# access should present the smallest possible attack surface.
FROM ${RUNTIME_IMAGE}

WORKDIR /
COPY --from=build /out/remedik /remedik

# 65532 is distroless' "nonroot" user.
USER 65532:65532

ENTRYPOINT ["/remedik"]
