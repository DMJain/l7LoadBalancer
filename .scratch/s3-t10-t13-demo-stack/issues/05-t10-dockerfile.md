# 05: S3.T10 — Multi-stage LB Dockerfile, `.dockerignore`, baked config, OCI labels, ADR-0005 amendment

**What to build:** A single multi-stage Dockerfile at the repository root that
produces a minimal, non-root, distroless image of the load balancer, and the
handful of supporting artifacts it needs. `docker build .` at the repo root
produces the image; `docker run <image>` starts it standalone with a working
default config; the container transitions to `healthy` via its own
`HEALTHCHECK`.

Contents of the Dockerfile:

- **Builder stage** — `FROM golang:1.23-bookworm AS builder`. Debian-based to
  sidestep musl edge cases. `CGO_ENABLED=0 GOOS=linux go build -trimpath
  -ldflags="-s -w -X main.version=$VERSION -X main.commit=$COMMIT"
  -o /out/l7lb ./cmd/l7LoadBalancer`. Version and commit come from `ARG`
  directives with sensible defaults.
- **Runtime stage** — `FROM gcr.io/distroless/static-debian12:nonroot@sha256:<digest> AS runtime`
  with the human-readable tag preserved as a `# nonroot` comment on the same
  line. The digest is fetched live at build authoring time (via
  `docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot`)
  rather than hardcoded to a stale value. Copies the built `l7lb` binary and
  the baked default config into `/etc/l7lb/config.yaml`.
- **`HEALTHCHECK`** — uses the `probe` subcommand from ticket 04, targeting
  `/livez` on the health endpoint's default port:

  ```dockerfile
  HEALTHCHECK --interval=5s --timeout=3s --retries=3 \
    CMD ["/l7lb", "probe", "http://127.0.0.1:8081/livez"]
  ```

- **`EXPOSE 8080 8081 9090`** — declares all three listeners for documentation.
  Compose (ticket 06) chooses which to publish.
- **OCI labels** — `org.opencontainers.image.source`, `.version`, `.revision`,
  `.licenses`. `source` uses the repo URL from `go.mod`'s module path; if
  that path does not resolve to a public repo, the label is omitted and a
  `# TODO` comment marks the gap rather than fabricating a URL.

Supporting files:

- **`.dockerignore`** at the repo root — strict allowlist. Contents:

  ```
  *
  !go.mod
  !go.sum
  !cmd/
  !internal/
  !configs/
  ```

  Everything else (`docs/`, `.git/`, `bin/`, `.scratch/`, `deployments/`,
  `bench/`, `*.md`) is excluded by the leading `*`. Adding a new top-level
  directory is excluded by default, which is the right safety direction.

- **`configs/docker.yaml`** — a new config file that mirrors
  `configs/example.yaml` but uses compose service names for backend URLs
  (`http://backend-a:8080`, `http://backend-b:8080`, `http://backend-c:8080`),
  `listen: ":8080"`, `metrics.listen: ":9090"`, `health_endpoint.listen: ":8081"`.
  Baked into the image at `/etc/l7lb/config.yaml` via `COPY`. `configs/example.yaml`
  is untouched — it remains the host-process default.

- **ADR-0005 amendment** — a single paragraph appended to the existing
  ADR-0005 (not a new ADR file), reading approximately:

  > *"The LB Dockerfile and root `docker-compose.yml` (S3.T10, S3.T11) are
  > demo/dev-stack artifacts. They do not constitute a deployment-target
  > decision; that remains deferred to Sprint 4/5 per the original ADR."*

No new ADR is written for T10 itself; the amendment covers the only
genuinely surprising choice (why this exists at all given ADR-0005's
deferral). Everything else is self-documenting via inline Dockerfile
comments.

**Blocked by:** 04. The Dockerfile's `HEALTHCHECK` uses the `probe`
subcommand from ticket 04; the ldflags target the `main.version`/`main.commit`
vars added there. Ticket 04 transitively depends on tickets 01–03.

**Status:** ready-for-agent

- [ ] `docker build -t l7lb:test .` succeeds from a clean checkout in under
      ~2 minutes on a typical developer laptop.
- [ ] `docker run --rm l7lb:test` starts the load balancer with the baked
      `configs/docker.yaml` and produces a JSON log line containing
      `"version": "<injected value>"` and `"commit": "<injected value>"`.
- [ ] `docker inspect l7lb:test` shows the four OCI labels
      (`source`, `version`, `revision`, `licenses`) populated from the ARG
      values passed at build time. If `source` is omitted per the `go.mod`-
      resolution rule, the `# TODO` comment is present in the Dockerfile.
- [ ] The runtime stage's base image is pinned by digest, with the human-
      readable tag preserved as a same-line comment.
- [ ] `docker run` produces a running container whose
      `docker inspect --format '{{.State.Health.Status}}' <id>` transitions
      from `starting` to `healthy` within the HEALTHCHECK's advertised
      interval-plus-tolerance window. (Manual smoke, recorded in session
      log; not a Go test.)
- [ ] The final image's user is `nonroot` (uid 65532), verified via
      `docker inspect --format '{{.Config.User}}' l7lb:test`.
- [ ] The `.dockerignore` allowlist takes effect: `docker build`'s "Sending
      build context" line reports a size measured in tens of KB, not the
      whole repo tree. Verified by inspecting build output.
- [ ] `configs/docker.yaml` parses cleanly through `config.Load` +
      `config.Validate` in a small unit test; its backend URLs use compose
      service names, not `127.0.0.1`.
- [ ] `docs/adr/0005-scope-of-production-grade.md` gains the one-paragraph
      amendment; the amendment is in a dedicated `## Amendment (2026-…) —
      demo-stack containerization does not settle deployment target` section
      so future readers see the delta cleanly.
- [ ] `configs/example.yaml` is not touched by this ticket.
- [ ] `make test`, `make test-race`, `go vet ./...`, `make fmt` pass — the
      only Go change in this ticket is the `configs/docker.yaml` parsing
      test.
- [ ] `PROGRESS.md` entry for this ticket links back to spec decisions
      D13–D15, D18–D25 for provenance.
