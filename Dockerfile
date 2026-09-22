# Multi-stage build for the Layer 7 load balancer (S3.T10).
#
# `docker build -t l7lb .` at the repository root produces a minimal image that
# runs standalone against the baked default config and reports its own health
# without a shell or curl. This is a demo/dev-stack artifact: it does NOT settle
# the deployment-target question deliberately deferred to Sprint 4/5 — see the
# amendment on ADR-0005.
#
# The build context is restricted by the repo-root .dockerignore allowlist
# (spec D21): only go.mod, go.sum, cmd/, internal/, and configs/ are sent.

# --- Builder ---------------------------------------------------------------
# Debian-based (not alpine) to sidestep musl edge cases in the static build
# (spec D14). The patch version matches go.mod's `go` directive so the
# toolchain never attempts a download at build time.
FROM golang:1.25.1-bookworm AS builder

ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /src

# Dependency layer first: it only invalidates when go.mod/go.sum change, so
# source edits reuse the module cache (spec D16).
COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY internal/ internal/

# CGO_ENABLED=0 produces a static binary that runs on the distroless static
# base; -trimpath drops local paths; -s -w strips the symbol table. version and
# commit are injected into the main package's package-level vars (S3.T10.0),
# where they reach the JSON startup log line.
RUN CGO_ENABLED=0 GOOS=linux go build \
	-trimpath \
	-ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
	-o /out/l7lb ./cmd/l7LoadBalancer

# --- Runtime ---------------------------------------------------------------
# Pinned by its live index digest (fetched with `docker buildx imagetools
# inspect gcr.io/distroless/static-debian12:nonroot` at authoring time) so
# rebuilds are drift-free. The human-readable tag — nonroot — is preserved on
# the line directly above rather than after the instruction: BuildKit rejects
# a trailing comment on FROM ("FROM requires either one or three arguments").
# The index digest (not a per-platform manifest digest) keeps the image
# buildable on arm64 and amd64 alike.
# gcr.io/distroless/static-debian12:nonroot
FROM gcr.io/distroless/static-debian12@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS runtime

ARG VERSION=dev
ARG COMMIT=unknown

# OCI labels (spec D22). source resolves from go.mod's module path
# github.com/DMJain/l7LoadBalancer; licenses from the repo's LICENSE (MIT).
LABEL org.opencontainers.image.source="https://github.com/DMJain/l7LoadBalancer" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.licenses="MIT"

COPY --from=builder /out/l7lb /l7lb

# Baked default config, using compose service names rather than 127.0.0.1
# (spec D20). configs/example.yaml stays the host-process default. The root
# compose bind-mounts the same file over this path so config can be edited on
# disk and picked up with `docker compose restart l7lb` (S3.T11).
COPY configs/docker.yaml /etc/l7lb/config.yaml

# All three listeners, for documentation. Compose decides which to publish.
EXPOSE 8080 8081 9090

# Native HEALTHCHECK that works in a shell-less distroless image: the binary
# probes itself via its own `probe` subcommand (S3.T10.0), targeting /livez on
# the health endpoint's default port. See ADR-0014 decision 2 for why /livez
# (it is unconditional 200; the probe timing out is the deadlock signal).
HEALTHCHECK --interval=5s --timeout=3s --retries=3 \
  CMD ["/l7lb", "probe", "http://127.0.0.1:8081/livez"]

ENTRYPOINT ["/l7lb", "-config", "/etc/l7lb/config.yaml"]
