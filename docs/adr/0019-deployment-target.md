# ADR-0019: Deployment target — the Docker container as the packaging artifact

- **Status**: Accepted
- **Date**: 2026-09-28
- **Deciders**: Darshan Jain (project owner) + opencode agent (S4.T19, decision made in the ADR-writing session; scope in `.scratch/s4-t12-t15-closeout/spec.md`)

## Context

Sprint 1 deferred the deployment-target decision (bare binary vs. Docker vs.
Kubernetes) on purpose. [ADR-0005](0005-scope-of-production-grade.md) lists "a
settled deployment target" among the things "production-grade" explicitly does
**not** mean, and its 2026-09-22 amendment records that the Sprint 3 Dockerfile
and root `docker-compose.yml` are demo/dev-stack artifacts that "do not
constitute a deployment-target decision": the image exists so a reviewer can
bring the system up with one command and so the shipped binary can self-probe
in a shell-less image, not to commit the project to containers. The deferral's
stated reason was that the reload and connection-lifecycle work would inform
the choice (ADR-0005; `MILESTONES.md` Sprint 4). That work now exists, so this
ADR makes the call.

The choice has to follow from the system actually built, whose deployment-
relevant properties are these:

1. **Three listeners in one process.** The client proxy on `cfg.Listen`
   (default `:8080`), the Prometheus metrics server on `metrics.listen`
   (`:9090`), and the health endpoint on `health_endpoint.listen` (`:8081`) —
   the health endpoint a separate always-on `http.Server`, never a path on the
   proxy, so probe traffic cannot enter the request path or its metrics
   ([ADR-0014](0014-health-endpoint-contract-and-probe-semantics.md) decision
   1).
2. **Reload is in-process, triggered by SIGHUP.** A reload swaps an immutable
   registry snapshot in place; there is no second process and no listening-
   socket handoff ([ADR-0015](0015-reload-architecture.md) decision 1). `main`
   installs SIGHUP separately from the SIGINT/SIGTERM shutdown context
   (`cmd/l7LoadBalancer/main.go:202`), so a reload never races shutdown.
3. **The binary probes itself.** The native `HEALTHCHECK` runs
   `/l7lb probe http://127.0.0.1:8081/livez` — the `probe` subcommand
   (`main.go:106`) exists precisely so a shell-less runtime has a health check
   ([ADR-0014](0014-health-endpoint-contract-and-probe-semantics.md) decision
   2; `Dockerfile:76`).
4. **No external state.** Configuration is one YAML file; there is no
   database, cache, or shared store to back up or coordinate ("there is none to
   back up; config is a file" — ADR-0005). Backend state (health, circuit,
   EWMA, active connections) is in-process and intentionally reset on restart;
   only the config file is durable.
5. **No clustering.** One instance serves one configured backend set; there is
   no peer discovery, no shared state between instances, and no scale-out
   requirement expressed anywhere in `MILESTONES.md` (ADR-0005 multi-tenancy
   bullet).

The artifacts already exist: a digest-pinned
`gcr.io/distroless/static-debian12:nonroot` image with the static binary baked
to `/l7lb`, `configs/docker.yaml` baked to `/etc/l7lb/config.yaml`, all three
ports `EXPOSE`d, and OCI labels (S3.T10); and a repo-root `docker-compose.yml`
that runs the image with the dummy backends, Prometheus, and Grafana (S3.T11).
What was missing was the decision that these are the *target*, not a demo.

## Decision

**The deployment target is the Docker container: the digest-pinned distroless
image is the packaged deployment artifact. Kubernetes is explicitly not the
target, and the bare binary is the supported primitive beneath the container,
not the packaged form.**

A deployment is: run the image, accepting `-config` pointing at a config file
(baked, or bind-mounted over `/etc/l7lb/config.yaml`), and publish the client
port. The health and metrics ports are exposed for an orchestrator or a
Prometheus scraper and may stay on an internal network. Operational moves map
directly onto the mechanisms already built:

- **Fleet change** — edit the backend list in the mounted config and
  `docker kill -s HUP <container>` (or `docker compose restart`). The binary is
  PID 1, so it receives the signal directly and swaps the backend set in place,
  dropping nothing (ADR-0015, ADR-0016; proven end-to-end at the OS boundary in
  S4.T14).
- **Health** — the image's own `HEALTHCHECK` (the `probe` subcommand against
  `/livez`) gives any container runtime a liveness signal with no shell or
  `curl` in the image.
- **Graceful shutdown** — `docker stop` delivers SIGTERM to PID 1; the shared
  signal context drains in-flight requests and shuts all three listeners down.
- **Any non-backend config change** (listener addresses, algorithm, transport,
  server timeouts) is rejected whole by reload (ADR-0015 decision 4) and
  requires a container restart — a deliberate operator action, not a silent
  no-op.

### Why the system's properties select containers

- The image is the reproducible unit; the three-port shape, baked config, OCI
  labels, and self-probe `HEALTHCHECK` already constitute a complete container
  contract. Choosing containers promotes an existing, tested artifact rather
  than commissioning new packaging.
- A single stateless process with no external state is the canonical container
  workload: nothing here needs host integration that a container would
  complicate.
- In-process SIGHUP reload works unchanged under a container runtime because
  the binary is PID 1 and receives the signal directly; the bind-mounted config
  lets a deployment change the backend fleet without rebuilding the image.

### SO_REUSEPORT process handoff is explicitly Post-Sprint 5

In-process reload cannot cover a *binary* change: a new image for the same
listener addresses needs a process replacement, and there is no socket handoff,
so a restart drops in-flight requests. SO_REUSEPORT-based handoff — start the
new process on the same port, drain the old — is the mechanism that would make
container image rollouts zero-downtime. It is recorded, not built: it stays on
`MILESTONES.md`'s Post-Sprint 5 "Optional extensions" list ("SO_REUSEPORT
socket handoff for reload across process restart"). ADR-0015 decision 1 already
states why it was not needed for the backend-only reload this sprint ships.

## Consequences

- Positive: the deployment decision is now the artifact that already exists —
  the distroless image — so the Sprint 4 close-out can state a target instead
  of leaving it deferred, and the container contract (three ports, baked config,
  `HEALTHCHECK`, OCI labels) is promoted from demo to target.
- Positive: backend-fleet changes deploy without a rebuild or a restart via the
  bind-mounted config and SIGHUP, so the common operational move is
  zero-downtime by construction.
- Positive: the health endpoint's orchestrator-native paths
  (`/livez`/`/readyz`/`/startupz`, ADR-0014) remain exactly what a container
  runtime or a future orchestrator consumes; nothing in this choice invalidates
  them.
- Negative: a non-backend config change or an image change requires a restart
  that drops in-flight requests, because there is no socket handoff (ADR-0015
  decision 1). Accepted; SO_REUSEPORT is the documented post-Sprint-5 path to
  close it.
- Negative: the container adds a runtime dependency the bare binary did not
  need. Accepted for reproducibility and the demo/`HEALTHCHECK` value; the bare
  binary remains runnable for development and the host-process chaos flow.
- Neutral: Kubernetes is not an alternative *for this project's scope*, but the
  health endpoint is Kubernetes-shaped by design (ADR-0014). If a future need
  introduces clustering or external state, the target decision is revisited in
  a new ADR; the endpoint contract need not change.
- Neutral: this ADR closes the Sprint 4 deployment-target deliverable that
  ADR-0005 deferred from Sprint 1 (`MILESTONES.md` Sprint 4). It records a
  decision; it changes no code.

## Alternatives considered

- **Bare binary as the packaged target** (run `/l7lb` under systemd or a
  supervisor, native SIGHUP, the `probe` subcommand as the health check):
  rejected as the *target* — it forgoes the reproducible packaging, the pinned
  runtime, and the single-command demo stack that already exist, for no
  property the container lacks. Retained as the supported primitive: `make run`
  and the host-process chaos flow keep working, and the container is the
  packaged form of the same binary.
- **Kubernetes as the target**: rejected — the system is a single stateless
  instance with no clustering, no external state to coordinate, and no
  scale-out requirement, so Kubernetes' scheduling, service discovery,
  scaling, and rolling-update machinery would address problems this project
  does not have. Adopting it would imply exactly the operational readiness
  ADR-0005 disclaims. The K8s-shaped health endpoint (ADR-0014) makes the
  system *runnable* on Kubernetes; that is a capability, not a target
  commitment.
- **Docker packaging plus a Kubernetes deployment contract now**: rejected for
  the same reason as Kubernetes itself — writing orchestration manifests for a
  target the project does not adopt is unused surface.
- **SO_REUSEPORT handoff now, to make container rollouts zero-downtime**:
  deferred, per ADR-0015 decision 1 and `MILESTONES.md`'s Post-Sprint 5 list —
  it is more moving parts than the backend-only reload needs, and the
  throughput/availability question it answers is not one Sprint 4 asks.
