# S3.T10–T13 — Demo stack: containerized LB, one-command compose, health endpoint, sprint retro

Bundle spec for the four remaining Sprint 3 tickets. Every design decision recorded
here was locked in the /grill-with-docs session that preceded this file
(Rounds 1–5). Kickoff prompts for the individual tickets consume this spec directly.

---

## Problem Statement

A reviewer or new contributor cloning the repository today cannot bring the whole
system up with a single command. Getting to a working demo requires three moving
parts started in three terminals — the dummy-backend compose, `make run` for the
load balancer as a host process, and the observability compose — and there is no
container image for the load balancer itself. Nothing published to a container
orchestrator can probe the load balancer's liveness or readiness, because the
process exposes only `/metrics` on `:9090` and the client listener on `:8080`;
neither answers "am I alive?" or "am I ready to accept traffic?" in a shape a
Docker `HEALTHCHECK`, a Kubernetes probe, or a Fly.io HTTP check understands.
Finally, Sprint 3 shipped fourteen tickets across three separate scoping passes
(health/circuit/metrics, observability, chaos), leaving `docs/architecture.md`
lagging behind current reality, `MILESTONES.md`'s exit criteria silent on the
demo-stack artifacts being added here, and no single document a Sprint 4 owner
can read as the debt inventory they are inheriting.

## Solution

Four sequential tickets, gated on each other in the order T12 → T10 → T11 → T13:

1. **S3.T12 — Health endpoint contract.** Add a third listener on `:8081` that
   serves three orchestrator-native probe paths (`/livez`, `/readyz`,
   `/startupz`) with structured JSON responses. Probe hits do not flow through
   the proxy path and are not counted as client traffic; they emit their own
   dedicated counter `lb_health_probe_total{endpoint, status}` on the existing
   Prometheus registry. Documented in **ADR-0014**.

2. **S3.T10 — Multi-stage LB Dockerfile.** Build the `l7LoadBalancer` binary
   under `golang:1.23-bookworm` with `CGO_ENABLED=0`, ship it in
   `gcr.io/distroless/static-debian12:nonroot`, expose all three listeners,
   bake a container-network-aware default config, add an OCI-labeled image
   whose `HEALTHCHECK` self-probes via a new `probe` subcommand on the same
   binary. **Amend ADR-0005** with one paragraph clarifying that this
   containerization is a demo-stack artifact and does not settle the
   deployment-target question deferred to Sprint 4/5.

3. **S3.T11 — Repo-root `docker-compose.yml`.** New file at the repository
   root that composes the LB image (from T10) with the three existing dummy
   backends, a Prometheus instance, and a Grafana instance — the whole system
   up under one command. The two existing composes stay in place, untouched
   and canonical for the flows they were built for (chaos scripts and
   host-process observability). Grafana provisioning is bind-mounted from the
   existing observability tree so there is a single source of truth for
   dashboard JSON and datasource config. A new `prometheus-stack.yml`
   sits next to the existing `prometheus.yml` because the scrape target
   changes (`l7lb:9090` instead of `host.docker.internal:9090`). Recorded as
   **ADR-0013 decision 18**.

4. **S3.T13 — Sprint 3 retro and architecture-doc update.** New file
   `docs/sprint-3-retro.md` mirroring the S2.T8 shape (Deliverables shipped /
   Deviations / ADR sweep / Architectural gaps / Sprint 4 handoff). Additive
   update to `docs/architecture.md` covering the six Sprint 3 subsystems
   (active health checking, passive outlier detection, circuit breaker,
   observability pipeline, health endpoint, container demo stack). No new ADR
   in T13 itself.

Before any ticket code lands, a separate `docs(milestones): add S3.T10–T13
deliverables and exit criteria` commit amends `MILESTONES.md` so the Sprint 3
"done" definition unambiguously includes: (i) the LB running as a container via
`docker compose up` at repo root with backends, Prometheus, and Grafana;
(ii) a health endpoint returning structured JSON with liveness and readiness
semantics; (iii) the Sprint 3 retro written.

## User Stories

### MILESTONES.md amendment (precondition)

1. As the project owner, I want `MILESTONES.md`'s Sprint 3 section to list T10–T13
   as deliverables with exit criteria, so that the sprint's "done" definition
   is unambiguous and future retros can audit against it.
2. As a Sprint 4 agent reading `MILESTONES.md` first, I want the Sprint 3
   boundary to match `PROGRESS.md`, so that I do not encounter silent drift
   between the strategic plan and the tactical log.
3. As a future sprint owner who needs to add tickets mid-sprint, I want a
   canonical precedent for how to do it, so that I can cite T10–T13's
   amendment commit rather than re-derive the policy.

### S3.T12 — Health endpoint contract

4. As a container orchestrator, I want a `/livez` endpoint that returns 200 as
   long as the load balancer's HTTP handler is responding, so that a probe
   timeout is my unambiguous signal to restart the container.
5. As a container orchestrator, I want a `/readyz` endpoint that returns 503
   until the load balancer is genuinely ready to accept client traffic, so that
   I do not route requests to an instance that would just return
   `ErrNoHealthyBackends` or that has not yet learned which of its backends are
   alive.
6. As a container orchestrator with a startup-probe concept (Kubernetes,
   Fly.io), I want a `/startupz` endpoint that transitions to 200 exactly once
   after initial config load and the first full active-probe round, so that I
   can give a slow-starting instance a longer grace period without
   loosening my steady-state liveness/readiness thresholds.
7. As a load-balancer operator debugging a "why is this instance not receiving
   traffic" issue, I want `/readyz`'s JSON body to name which check is failing
   (`config_loaded`, `initial_probe_complete`, `selectable_backends`), so that
   I do not have to correlate logs and metrics to figure out which of the three
   readiness conditions is the culprit.
8. As a fronting load balancer or Kubernetes Service, I want the LB's
   `/readyz` to go 503 when its entire backend fleet has been evicted, so that
   I can route around this LB instance during a full-cluster outage.
9. As a Prometheus operator, I want probe traffic on `:8081` to not appear
   in `lb_requests_total`, so that my request-rate dashboards are not polluted
   by probe traffic pulsing every few seconds.
10. As a Grafana user, I want a `lb_health_probe_total{endpoint, status}`
    time series, so that I can see readiness failing over time on the same
    dashboard as everything else Sprint 3 shipped.
11. As a load-balancer operator, I want the health endpoint's listen address
    to be configurable via `health_endpoint: { listen: ":8081" }`, so that I
    can rebind the port when running under a container-network constraint
    without editing code.
12. As a shutdown-signal handler, I want the health server's lifecycle to
    mirror the metrics server exactly — started in `main`, shared `sigCtx`,
    graceful shutdown alongside the client and metrics servers — so that a
    Sprint 4 `Run(ctx, cfg)` seam can absorb both listeners uniformly.

### S3.T10 — LB Dockerfile

13. As a reviewer, I want to be able to run `docker run <image>` and get a
    working (if unconfigured) load balancer, so that I can inspect it in
    isolation without setting up a full compose stack.
14. As a security-conscious reviewer, I want the container to run as a
    non-root user in a distroless base with no shell and no libc surprises,
    so that I can point at the Dockerfile as an example of modern Go container
    hygiene.
15. As a Docker daemon running the container, I want a native `HEALTHCHECK`
    directive that actually works despite the distroless base having no shell
    or `curl`, so that container-level health is observable without extra
    tooling.
16. As a build-cache operator, I want a multi-stage build that runs
    `go build` in a full Debian-based Go image and copies only the resulting
    binary into the runtime image, so that layer caching is efficient and the
    final image contains no toolchain.
17. As an OCI-image inspector, I want `docker inspect` to reveal the source
    repository, version, revision, and license labels, so that I can identify
    a build from its image alone.
18. As a load-balancer operator, I want the image to log its version and
    commit at startup, so that I can correlate a running container with the
    source tree that built it.
19. As a build-context uploader, I want a strict allowlist `.dockerignore`
    that includes only `go.mod`, `go.sum`, `cmd/`, `internal/`, and
    `configs/`, so that adding a new top-level directory does not
    accidentally slip secrets or gigabytes of docs into the build context.
20. As a container maintainer, I want the base image pinned by digest with the
    human-readable tag preserved as a comment, so that rebuilds are
    reproducible and drift-free.
21. As a portfolio reviewer, I want a `probe` subcommand on the main binary
    (invoked as `l7lb probe <url>`) rather than a `-probe` flag, so that the
    CLI grammar matches the `kubectl`/`docker` convention and does not compete
    with existing/future top-level flags.
22. As a Sprint 4/5 owner reading ADR-0005, I want a one-paragraph amendment
    stating that this Dockerfile does not constitute the deployment-target
    decision, so that the deferred choice remains genuinely deferred and can
    be informed by the reload/lifecycle work rather than pre-empted.

### S3.T11 — Root docker-compose.yml

23. As a reviewer, I want a single `docker compose up` at the repository root
    to bring up the whole demonstrable system (LB + three backends +
    Prometheus + Grafana), so that first-touch time is under a minute.
24. As the maintainer of the existing chaos test scripts, I want the two
    existing composes under `deployments/docker/` left untouched, so that
    `eviction.sh` and `circuit.sh` continue to work against the host-process
    LB flow they were built for.
25. As a reviewer opening the demo, I want only the ports I actually need to
    interact with published to the host (`:8080` for client traffic, `:8081`
    for `curl`-ing health, `:3000` for Grafana), so that the demo surface is
    tight and there is zero collision risk with the other two composes.
26. As a Prometheus operator, I want scrape configuration for the container
    network to live in a second file (`prometheus-stack.yml`) alongside the
    existing host-network `prometheus.yml`, so that neither stack's scrape
    target is fragile to the other.
27. As a Grafana maintainer, I want the root compose to bind-mount the same
    dashboard JSON and datasource provisioning the observability compose
    already uses, so that there is a single source of truth and the dashboard
    does not drift between the two stacks.
28. As a demo audience, I want the LB and its dependencies to start in a
    deterministic order — backends first, then the LB (whose readiness is
    itself gated by its own healthcheck), then Prometheus once the LB is
    healthy, then Grafana — so that first-scrape data is real and the demo
    does not flash a broken "no data" panel.
29. As a reviewer playing with `docker stop backend-a` to watch the circuit
    breaker trip, I want every service configured with
    `restart: unless-stopped`, so that a stopped container does not vanish and
    the recovery flow works with `docker start backend-a`.
30. As a load-balancer operator iterating on config, I want the container's
    baked default `configs/docker.yaml` to be identical to the file that the
    compose file bind-mounts over it, so that I can edit config on disk and
    just `docker compose restart l7lb` rather than rebuild the image.
31. As the Grafana datasource, I want the Prometheus service pinned to the
    literal name `prometheus` in the root compose, so that the shared
    datasource provisioning YAML (which hardcodes `http://prometheus:9090`)
    resolves in either stack.
32. As a Sprint 3 owner writing the ADR-0013 amendment, I want decision 18 to
    capture: root compose is additive, per-stack Prometheus config,
    single-source Grafana provisioning, the exact published-port list, and the
    mixed health-gating strategy, so that a future reader does not have to
    reconstruct the trade-offs from `docker-compose.yml` alone.

### S3.T13 — Sprint 3 retro and architecture doc

33. As a Sprint 4 agent, I want a `docs/sprint-3-retro.md` file mirroring the
    S2.T8 shape (Deliverables shipped / Deviations / ADR sweep / Architectural
    gaps / Sprint 4 handoff), so that reading one file gives me the sprint's
    exit state without spelunking `PROGRESS.md`, git log, and session logs.
34. As a Sprint 3 auditor, I want the deviations section to include all five
    known deviations — the mid-sprint S3.T6.5 bug fix; the T8/T9 chaos-test
    assembly duplication with the `Run(ctx, cfg)` convergence pointer; the
    Half-Open scan-promotion log gap ADR-0013 permanently documents; the
    mid-sprint T10–T13 scope additions and the amendment-first precedent they
    set; the T9 owner-approved spec/impl conflicts adapted in tests — so that
    a future reader can distinguish "known and accepted" from "unknown".
35. As a docs reader, I want `docs/architecture.md` updated additively (not
    rewritten) to cover the six Sprint 3 subsystems, so that the doc reflects
    the shipped system without risking a diff that drifts against AGENTS.md.
36. As an ADR-sweep auditor, I want T13 to verify that every non-trivial
    Sprint 3 decision has an ADR and every Sprint 3 ADR has shipped code, so
    that "ADRs decorate the actual codebase" remains true. Any gap must be
    closed or explicitly deferred with justification in the retro before T13
    marks DONE.
37. As a Sprint 4 owner, I want a "Sprint 4 handoff" section enumerating every
    piece of debt Sprint 3 leaves on the floor — the `Run(ctx, cfg)`
    convergence, the Half-Open log gap, the deferred deployment-target ADR,
    the two manual-verification-pending shell smokes (`eviction.sh`,
    `circuit.sh`), and any coverage gaps surfaced by the ADR sweep — so that I
    inherit an inventory rather than a scavenger hunt.
38. As the Sprint 3 retro author, I do not want a new ADR for the retro itself,
    so that the file's shape becomes the template by existing (as S2.T8's did
    for it).

## Implementation Decisions

### Ordering, gating, and MILESTONES amendment

- **D0.** `MILESTONES.md` is amended before any ticket code lands, in a
  separate commit `docs(milestones): add S3.T10–T13 deliverables and exit
  criteria`. The exit criteria added are the three items enumerated in the
  Solution section.
- **D1.** Tickets execute sequentially in the order **T12 → T10 → T11 →
  T13**. T13 is claimed in `PROGRESS.md` with an explicit
  `[BLOCKED-BY: S3.T10, S3.T11, S3.T12]` tag; the agent does not start
  T13 until all three predecessors are DONE.

### S3.T12 — Health endpoint contract

- **D2. Own listener on `:8081` (configurable).** Third `http.Server` in
  `main`, shares `sigCtx`, joins the graceful-shutdown sequence. Mirrors
  exactly how `metricsSrv` is wired. Sprint 4's `Run(ctx, cfg)` seam will
  absorb both listeners uniformly.
- **D3. Three probe paths.** `/livez`, `/readyz`, `/startupz`. Names are
  K8s-native so every orchestrator's probe field maps 1:1.
- **D4. `/livez` returns 200 unconditionally**, body `{"status":"alive"}`.
  Rationale: Go gives us no general deadlock signal; a deadlocked handler
  would not respond at all, so the probe timeout *is* the failure signal.
  A watchdog is speculative complexity with nothing to tune it against.
- **D5. `/startupz` is a one-shot gate**: 503 until `{config_loaded AND
  initial_probe_complete}` both true, at which point it transitions to 200
  and stays there permanently.
- **D6. `/readyz` gates on the startup conditions plus a live
  `Registry.Selectable() >= 1` check at query time.** The empty-selectable
  condition returning 503 is what enables an upstream LB or K8s Service to
  route around this instance during a full-cluster outage. Same signal
  `ErrNoHealthyBackends` already surfaces as 503 to clients.
- **D7. Structured JSON per-check response body.** Pinned shape:

  ```json
  {
    "status": "ready",
    "checks": {
      "config_loaded": true,
      "initial_probe_complete": true,
      "selectable_backends": 3
    }
  }
  ```

  On 503 the same envelope with `"status": "not_ready"` and the failing
  check visible (e.g. `"selectable_backends": 0`). `/livez` returns
  `{"status": "alive"}` unconditionally. `/startupz` returns the same
  envelope minus `selectable_backends` (it does not gate on that).

- **D8. New API on the active health checker.** `HealthChecker.ProbeRoundComplete() bool`
  backed by a single `atomic.Bool` flipped once after the first full sweep
  covers every backend. Cheap, testable, no lock contention.
- **D9. New API on the metrics collector.** `Collector.RecordProbe(endpoint string, statusCode int)`
  drives a new `lb_health_probe_total{endpoint, status}` counter registered
  on the same private Prometheus registry as everything else. Mirrors the
  existing `RecordRequest` shape so registration stays centralised.
  Cardinality is bounded (3 endpoints × ~2 status classes = 6 series).
- **D10. Probes are not counted as client traffic.** Because D2 makes the
  health server a distinct listener, probes never enter the proxy path and
  therefore never touch `lb_requests_total` or the latency histogram. The
  new `lb_health_probe_total` counter (D9) is the sole record of probe
  activity in Prometheus.
- **D11. Config surface.** New YAML block `health_endpoint: { listen: ":8081" }`,
  mirroring the existing `metrics: { listen: … }` block. `listen` is
  optional with a default of `":8081"`. Validation checks the string parses
  as a `host:port`. No other tunables.
- **D12. ADR-0014 records all of the above.** Ten decisions: separate
  listener; three probe paths; `/livez` unconditional 200; `/startupz`
  one-shot gate; `/readyz` gates on startup conditions plus live
  selectable-set check; empty-selectable → 503; structured JSON body;
  `lb_health_probe_total` counter; `health_endpoint` config block;
  `HealthChecker.ProbeRoundComplete() bool`. The ADR also states explicitly:
  Docker `HEALTHCHECK` targets `/livez`; Fly.io HTTP check targets
  `/readyz`; K8s maps all three to distinct probe configs.

### S3.T10 — LB Dockerfile

- **D13. Base image pinned by digest.** `FROM gcr.io/distroless/static-debian12:nonroot@sha256:<digest> AS runtime # nonroot`.
  The agent fetches the current digest at build time (`docker buildx imagetools inspect`);
  no hardcoded stale digest.
- **D14. Builder: `golang:1.23-bookworm AS builder`.** Debian-based to avoid
  musl edge cases.
- **D15. Build flags.** `CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=$VERSION -X main.commit=$COMMIT" -o /out/l7lb ./cmd/l7LoadBalancer`.
- **D16. Version/commit injection.** Add to `cmd/l7LoadBalancer/main.go`:
  ```go
  var (
      version = "dev"
      commit  = "unknown"
  )
  ```
  and a startup log line `slog.Info("starting", "version", version, "commit", commit)`.
  Dockerfile passes them via `ARG VERSION` / `ARG COMMIT`.
- **D17. `probe` subcommand on the main binary.** Grammar:
  `l7lb probe <url>`. Implemented as an early branch in `main` (~20 lines,
  `net/http` + `os.Exit`, no framework, no `cobra`). Doc comment points at
  ADR-0014 for why the default HEALTHCHECK targets `/livez`.
- **D18. HEALTHCHECK directive:**
  ```dockerfile
  HEALTHCHECK --interval=5s --timeout=3s --retries=3 \
    CMD ["/l7lb", "probe", "http://127.0.0.1:8081/livez"]
  ```
- **D19. Exposed ports.** `EXPOSE 8080 8081 9090`. Compose decides which to
  publish; the image documents what listens.
- **D20. Baked default config.** New file `configs/docker.yaml` with backend
  URLs using compose service names (`http://backend-a:8080`, etc.),
  `listen: ":8080"`, `metrics.listen: ":9090"`, `health_endpoint.listen: ":8081"`.
  `configs/example.yaml` is unchanged and remains the host-process default.
  The Dockerfile does `COPY configs/docker.yaml /etc/l7lb/config.yaml` and the
  entrypoint takes `-config /etc/l7lb/config.yaml`.
- **D21. `.dockerignore` allowlist.** Contents:
  ```
  *
  !go.mod
  !go.sum
  !cmd/
  !internal/
  !configs/
  ```
  Allowlist means adding a new top-level directory does not accidentally
  slip into the build context.
- **D22. OCI labels.** In the runtime stage:
  ```dockerfile
  LABEL org.opencontainers.image.source="<repo URL from go.mod>" \
        org.opencontainers.image.version="${VERSION}" \
        org.opencontainers.image.revision="${COMMIT}" \
        org.opencontainers.image.licenses="MIT"
  ```
  If `go.mod` does not resolve to a public repo URL, leave a `# TODO` and
  drop the `source` label — do not fabricate a URL.
- **D23. Single-arch only.** No `docker buildx --platform`. Users on Apple
  Silicon get an arm64 image from their own `docker build`. Multi-arch is
  the moment we push to a registry, which is a Sprint 5+ concern.
- **D24. ADR-0005 amendment.** One paragraph, appended to ADR-0005:

  > *"The LB Dockerfile and root `docker-compose.yml` (S3.T10, S3.T11) are
  > demo/dev-stack artifacts. They do not constitute a deployment-target
  > decision; that remains deferred to Sprint 4/5 per the original ADR."*

- **D25. No new ADR for T10 itself.** The only genuinely surprising choice
  (its existence given ADR-0005) is covered by the amendment. Inline
  Dockerfile comments cover the rest.

### S3.T11 — Repo-root docker-compose.yml

- **D26. File literally at repo root.** `./docker-compose.yml`. Not in
  `deployments/docker/`.
- **D27. Additive to the two existing composes.** `deployments/docker/docker-compose.yml`
  (dummy backends) and `deployments/docker/observability/docker-compose.yml`
  (Prometheus + Grafana against a host-process LB) are untouched. Chaos
  scripts remain valid against their host-process flow (scope discipline).
- **D28. Services and images.** Five services: `backend-a`, `backend-b`,
  `backend-c` (each `build: deployments/docker/dummy-backend`), `l7lb`
  (`build: .`, using the T10 Dockerfile), `prometheus`
  (`prom/prometheus:v2.53.0`), `grafana` (`grafana/grafana:11.1.0`).
  Backend names match `configs/example.yaml` / `configs/docker.yaml`.
- **D29. Prometheus service is named exactly `prometheus`.** Locked so the
  Grafana datasource provisioning YAML (which hardcodes
  `http://prometheus:9090`) works verbatim from either stack.
- **D30. New Prometheus config: `deployments/docker/observability/prometheus/prometheus-stack.yml`.**
  Sits alongside the existing `prometheus.yml`. The only delta:
  `targets: ["l7lb:9090"]` instead of
  `targets: ["host.docker.internal:9090"]`. Both files carry a header
  cross-reference to each other and to ADR-0013 decision 18.
- **D31. Grafana provisioning bind-mount.** Root compose bind-mounts
  `./deployments/docker/observability/grafana/provisioning:/etc/grafana/provisioning:ro`
  and the same for `dashboards`. Single source of truth for dashboard JSON
  and datasource config.
- **D32. Networking.** Default bridge network only (Compose auto-creates
  `l7loadbalancer_default`). Services DNS-resolve each other by service
  name. No frontend/backend split — that is a production concern excluded
  by ADR-0005.
- **D33. Published host ports (tight demo surface):** `8080:8080` (LB
  client), `8081:8081` (LB health — reviewer curls `/readyz` during the
  demo), `3000:3000` (Grafana). Prometheus, LB metrics, and the backends
  stay internal to the compose network.
- **D34. Dependency chain (mixed health-gating strategy).**
  - `backend-a/b/c`: no `depends_on` (start immediately).
  - `l7lb`: `depends_on backend-a/b/c` with `condition: service_started`;
    own `healthcheck` block runs `l7lb probe http://127.0.0.1:8081/livez`.
  - `prometheus`: `depends_on l7lb` with `condition: service_healthy` — so
    it never scrapes a not-yet-listening metrics port.
  - `grafana`: `depends_on prometheus` with `condition: service_started` —
    Grafana retries datasource connections gracefully.
  - Backends stay `scratch` (no shell, no wget); adding a `probe`
    subcommand to `dummy-backend` was rejected as scope creep into
    unrelated code.
- **D35. Restart policy.** `restart: unless-stopped` on every service. Makes
  the `docker stop backend-a` / `docker start backend-a` loop that
  demonstrates circuit-breaker recovery work without containers vanishing.
- **D36. Config bind-mount.** Root compose bind-mounts
  `./configs/docker.yaml:/etc/l7lb/config.yaml:ro`. Since the image bakes
  the same file, the bind-mount is a no-op override at first; its value is
  that a reviewer can edit config on disk and `docker compose restart l7lb`
  without rebuilding.
- **D37. Chaos scripts untouched.** `deployments/docker/chaos/eviction.sh`
  and `circuit.sh` continue targeting the host-process LB flow. No
  parameterisation. If future work wants unified chaos-against-container,
  that is its own ticket.
- **D38. ADR-0013 decision 18** (new decision appended to ADR-0013): the
  repo-root `docker-compose.yml` is a hermetic demo stack (LB + backends +
  observability). The two existing compose files under `deployments/docker/`
  remain canonical for their specialised flows (backends-only for chaos
  testing with host-process LB; observability-only for scraping a
  host-process LB). The root compose is additive, not a replacement. The
  decision also captures: per-stack Prometheus config, single-source
  Grafana provisioning, the exact published-port list (`8080`, `8081`,
  `3000`), and the mixed health-gating strategy from D34.

### S3.T13 — Sprint 3 retro and architecture doc

- **D39. New file `docs/sprint-3-retro.md`.** Mirrors S2.T8 shape.
  Section list: *Deliverables shipped* / *Deviations from MILESTONES/spec*
  / *ADR sweep* / *Architectural gaps left open* / *Sprint 4 handoff*.
- **D40. Deviations section — five items, one paragraph each:**
  1. S3.T6.5 inserted mid-sprint (reinstatement gate `==` → `>=` — a bug
     in already-shipped S3.T1 code). Cite the fix commit and S3.T6.5's
     `PROGRESS.md` entry.
  2. T8/T9 chaos tests use an external `chaos_test` package with
     duplication of Sprint 3 assembly; the "Sprint 4 `Run(ctx, cfg)` seam
     will converge this" pointer is a known debt. Cite the review notes
     and the two ticket entries.
  3. Half-Open promotion won by a `Registry.Selectable()` scan is never
     logged — a permanent gap ADR-0013 documents.
  4. T10/T11/T12/T13 were mid-sprint scope additions to `MILESTONES.md`.
     The paragraph explicitly names the precedent this sets:
     *"mid-sprint scope additions require a `MILESTONES.md` amendment
     commit before any code lands, per S3.T10–T13's amendment-first
     ordering (D0)."*
  5. T9's three owner-approved spec-to-implementation conflicts were
     adapted in tests rather than in production code. Cite the T9 review
     summary.
- **D41. ADR sweep — audit-only checklist.** For each Sprint 3 ADR (0004
  through 0014 plus the 0005 and 0013 amendments), verify: (i) the ADR has
  corresponding code and (ii) the code matches the ADR's decision text.
  For each non-trivial Sprint 3 decision, verify an ADR exists. Any gap is
  a **blocker** — T13 cannot mark DONE until the gap is closed or
  explicitly deferred with justification recorded in the retro.
- **D42. `docs/architecture.md` update — additive only.** New/refreshed
  sections cover the six Sprint 3 subsystems (active health checking,
  passive outlier detection, circuit breaker, observability pipeline,
  health endpoint, container demo stack). Sprint 1/2 sections stay
  untouched. Cross-references to ADR-0011 through ADR-0014 and the two
  amendments.
- **D43. Sprint 4 handoff section enumerates all known debt:** the
  `Run(ctx, cfg)` seam and the chaos-test convergence path; the Half-Open
  scan-promotion log gap (pinned at two test layers per ADR-0013 decision
  13); the deployment-target ADR still deferred per ADR-0005; the two
  manual-verification-pending shell smokes (`eviction.sh`, `circuit.sh`);
  and any test-coverage gaps surfaced by the ADR sweep (D41). Sprint 4's
  kickoff spec reads this section as its starting debt inventory.
- **D44. No ADR for T13 itself.** S2.T8 did not need one; T13 does not
  either. The retro's shape becomes the template by existing.

## Testing Decisions

### General principle

Tests exercise externally-observable behaviour: HTTP status codes and JSON
bodies from the new endpoints, Prometheus counter/gauge deltas observed via
`testutil`, container-level HEALTHCHECK transitions, `docker compose up`
converging on all-healthy, log emissions asserted via structured `slog`
capture. Tests never assert on the shape of internal atomics, mutex
ownership, or private struct layout.

### S3.T12 — testable units

- **`internal/health.HealthChecker.ProbeRoundComplete()`** — a targeted
  test constructs a checker over N backends with a fake probe transport,
  drives probes until every backend has been contacted at least once, and
  asserts the atomic flips from `false` to `true` exactly once (and stays
  `true` under further probe rounds). Race-safe under `-race`.
- **The three probe handlers (`/livez`, `/readyz`, `/startupz`)** — table-
  driven tests using `httptest.NewServer` (or the handler directly with
  `httptest.NewRecorder`) that construct a checker + registry in known
  states and assert:
  - `/livez` returns 200 and `{"status":"alive"}` in every state.
  - `/startupz` returns 503 with `{"config_loaded": …, "initial_probe_complete": false}`
    before the round completes and 200 after; stays 200 permanently.
  - `/readyz` returns 503 in three scenarios (no config; probe round not
    complete; empty selectable set at query time), naming the failing
    check in the JSON body each time. Returns 200 only when all three
    conditions hold.
- **Metrics counter** — a test hits each endpoint in each state and
  asserts `lb_health_probe_total{endpoint, status}` increments correctly
  via `promtestutil.CollectAndCount` / `.ToFloat64`, exactly as the
  existing `metrics` tests do for other counters.
- **Prior art:** `internal/metrics/metrics_test.go` (Prometheus registry
  assertions), `internal/proxy/proxy_test.go` (handler-shape tests using
  `httptest`), `internal/health/checker_test.go` (health-checker driving
  with fake transports).

### S3.T10 — testable units

- **`probe` subcommand behaviour** — a unit test in
  `cmd/l7LoadBalancer/main_test.go` (existing file) stubs `os.Args`,
  runs `main` (or a factored `runProbe` function) against a `httptest`
  server that returns 200/503 and asserts the process exits 0/1.
  A second test asserts a connection refusal exits non-zero.
- **Image-level HEALTHCHECK integration** — an out-of-band smoke test
  documented in the T10 kickoff (not a Go test): `docker build`, then
  `docker run -d <image>` against a running host stack, then poll
  `docker inspect --format '{{.State.Health.Status}}'` until `healthy`.
  This is a manual verification for the retro, not a CI-shaped test —
  matches the S3.T8/T9 pattern where Docker smokes are manually
  verified per the retro.
- **Prior art:** `cmd/l7LoadBalancer/main_test.go` (top-level main-package
  test wiring); the S3.T7 observability smoke script pattern for the
  manual verification.

### S3.T11 — testable units

- **Compose validation** — a documented smoke: `docker compose -f docker-compose.yml config`
  parses; `docker compose up -d --build` converges; each service reaches
  `healthy` (LB) or `running` (backends, Prometheus, Grafana); `curl http://localhost:8081/readyz`
  returns 200 with three selectable backends; `curl http://localhost:8080/`
  returns 200 through the proxy; Grafana at `http://localhost:3000`
  renders the provisioned dashboard with data. Manual verification, same
  pattern as S3.T7's `smoke.sh` and the S3.T8/T9 chaos smokes.
- **No Go-level tests for the compose file itself.** The compose is
  infrastructure; its correctness is proved by the smoke above.
- **Prior art:** `deployments/docker/observability/smoke.sh` (Prometheus/
  Grafana smoke pattern); `deployments/docker/chaos/{eviction,circuit}.sh`
  (LB-in-the-loop smoke pattern).

### S3.T13 — testable units

- **T13 is docs-only.** No Go tests. Verification is the D41 ADR-sweep
  checklist itself: for each ADR, `git log --grep` and `grep -r` confirm
  code exists; for each non-trivial Sprint 3 code path, `grep -r` in
  `docs/adr/` confirms an ADR references it. The retro's ADR-sweep section
  records the audit as a table so a future reader can re-run the check.

## Out of Scope

- **Publishing the container image to a registry** (GHCR, Docker Hub, ECR).
  T10 builds locally; no `docker push`, no CI, no image tagging convention
  beyond the OCI labels. Sprint 5+ concern.
- **Multi-architecture (linux/amd64 + linux/arm64) builds** via
  `docker buildx`. Single-arch only; users get their native arch from
  their own `docker build`.
- **Any change to the two existing compose files** under `deployments/docker/`.
  They remain canonical for chaos and host-process observability flows.
- **Any change to the chaos scripts** (`eviction.sh`, `circuit.sh`). They
  keep targeting the host-process LB flow (scope discipline).
- **Adding a `probe` subcommand to `dummy-backend`** (or otherwise changing
  its `scratch` base image). Backends use `depends_on: service_started`
  only in the root compose.
- **A watchdog-based liveness signal** (request-loop-progress or similar).
  `/livez` is unconditional 200 by design; the probe timing out is the
  deadlock signal (D4).
- **A frontend/backend network split** in the root compose. Single default
  bridge network only; network isolation is a production concern excluded
  by ADR-0005.
- **The deployment-target decision** (bare binary vs. Docker vs.
  Kubernetes). Explicitly still deferred to Sprint 4/5 per ADR-0005; the
  T10 amendment paragraph reaffirms this rather than resolving it.
- **The Sprint 4 `Run(ctx, cfg)` seam refactor.** T12's mirror-of-`metricsSrv`
  wiring is designed so the seam absorbs both listeners uniformly later;
  the seam itself is Sprint 4's ticket, not this bundle's.
- **Removing the Half-Open scan-promotion log gap.** ADR-0013 permanently
  documents this gap; T13 records it in the handoff section, does not
  close it.
- **Rewriting `docs/architecture.md` Sprint 1/2 sections.** T13's update is
  additive-only.
- **A new ADR for T11, T13, or for the LB Dockerfile itself.** The three
  ADR touches this bundle needs are: ADR-0005 amendment (T10), ADR-0013
  decision 18 (T11), ADR-0014 new (T12).

## Further Notes

### Full decision-tree provenance

Every design decision above corresponds to a numbered question and its
locked answer from the /grill-with-docs session that preceded this spec.
Reading in order: Rounds 1 (Q1–Q4, sprint scope and gating), 2 (Q5–Q12,
T12 shape), 3 (Q13–Q21, T10 shape), 4 (Q22–Q31, T11 shape with Q27
addendum), 5 (Q32–Q38, T13 shape). The session log for those rounds is
the authoritative provenance for any decision here that a future reader
finds under-motivated.

### ADR touches this bundle produces

- **ADR-0005** gets a one-paragraph amendment during T10 (D24).
- **ADR-0013** gets decision 18 appended during T11 (D38).
- **ADR-0014** is written new during T12 (D12), covering ten decisions.
- No ADRs are written during T13 itself (D44).

### Reader hints for the individual ticket kickoff prompts

- T12's kickoff must pin: `HealthChecker.ProbeRoundComplete() bool` API,
  `Collector.RecordProbe(endpoint, statusCode)` API, `health_endpoint`
  config block, the exact JSON envelope from D7, and the ADR-0014 decision
  list from D12.
- T10's kickoff must pin: the digest-pin ceremony from D13 (agent fetches
  live), the `probe` subcommand grammar from D17, the `main.version`/
  `main.commit` addition from D16, the `.dockerignore` allowlist from D21,
  the OCI labels from D22, and the ADR-0005 amendment paragraph from D24.
- T11's kickoff must pin: the exact file location (`./docker-compose.yml`),
  the service names (D28), the `prometheus` service-name lock (D29), the
  new `prometheus-stack.yml` path (D30), the Grafana bind-mount source
  paths (D31), the exact published-port list (D33), the dependency chain
  from D34, and the ADR-0013 decision 18 text from D38.
- T13's kickoff must pin: the section list from D39, the five deviations
  from D40 with the D0 precedent naming, the audit-blocker rule from D41,
  the additive-only rule for `docs/architecture.md` from D42, and the
  handoff-section enumeration from D43.
