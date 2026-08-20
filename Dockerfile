# syntax=docker/dockerfile:1

# Base images are build arguments so a build can pin or substitute them
# without editing this file:
#   docker build --build-arg GO_IMAGE=golang:1.26.6-alpine .
#
# Both are declared here, before any FROM: an ARG used in a FROM
# instruction must live in the global scope, because arguments declared
# inside a stage are not visible to the next stage's FROM.
# Pinned by digest, with the tag kept beside it so a human can still read what
# it is. A tag is a pointer somebody else can move: the image built from
# golang:1.26.6-alpine last week and the one built from it today are not
# necessarily the same bytes, and "reproducible" then means nothing. Dependabot
# watches the docker ecosystem here, so these are bumped by a pull request that
# says what changed rather than by a silent re-resolution at build time.
ARG GO_IMAGE=golang:1.26.6-alpine@sha256:3889b425f035be855a72fb4755265311293b6d414521f0a519d819df32222d83
ARG RUNTIME_IMAGE=gcr.io/distroless/static:nonroot@sha256:f7f8f729987ad0fdf6b05eeeae94b26e6a0f613bdf46feea7fc40f7bd72953e6

# Build stage. Dependencies always come from the module proxy, never from a
# local vendor directory (.dockerignore excludes it), so the image builds
# identically on a laptop and in CI.
#
# --platform=$BUILDPLATFORM pins this stage to the machine doing the
# building, and the compiler is told what to target instead. Without it,
# buildx runs the whole stage under QEMU for every foreign architecture — so
# `go build` for linux/arm64 is emulated instruction by instruction on an
# amd64 runner. That is not a small tax: it took the first release's build
# past twenty minutes, against roughly two for the same work compiled
# natively.
#
# It is free to avoid because this binary is CGO_ENABLED=0 pure Go, which
# Go cross-compiles perfectly. Anything linking C would have to go back to
# emulation, or to a cross toolchain.
FROM --platform=${BUILDPLATFORM} ${GO_IMAGE} AS build

# Supplied by buildx, one value per platform being built.
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src

# Dependency manifests first: this layer is cached until they change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
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
