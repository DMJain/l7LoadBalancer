# MILESTONES

Strategic plan. Each sprint = one weekend of focused work. Tasks under each sprint go into `PROGRESS.md` as they get scoped.

## Sprint 1 — Foundation

**Goal**: Working proxy scaffold with two basic selection algorithms.

**Deliverables**:
- `httputil.ReverseProxy` wrapper using `Director` + custom `Transport` pattern.
- Backend registry with atomic state.
- `Selector` interface with `RoundRobin` and `LeastConnections` implementations.
- YAML config loader with strict validation.
- 3 dummy backends via docker-compose.
- Table-driven tests for both selectors.

**Exit criteria**:
- `make run` starts the proxy against `configs/example.yaml`.
- `curl` through the proxy distributes across 3 backends per selected algorithm.
- `make test` passes.
- `make test-race` passes.

## Sprint 2 — Advanced Algorithms

**Goal**: Implement the intellectually meaty selection algorithms.

**Deliverables**:
- `ConsistentHashBoundedLoads` per Mirrokni-Thorup-Zadimoghaddam (2016) — live `ActiveConns()` averaged over healthy backends, rehash when a backend is over `max(1, ceil(avg × 1.25))` (ADR-0009).
- `PowerOfTwoChoicesEWMA` — pick two random backends, choose lower EWMA-tracked latency; atomic latency updates.
- Unit tests demonstrating distribution properties (P2C converges load onto faster backends under latency skew; consistent-hash bounded-loads rebalances hot keys).
- ADR: why bounded-loads over naive consistent hashing.
- ADR: why P2C over least-connections when latency is skewed.

**Exit criteria**:
- All 4 algorithms selectable via config.
- Load-skew test: P2C measurably favors faster backends after warmup.
- Hot-key test: bounded-loads reroutes when one backend hits capacity.

## Sprint 3 — Resilience & Observability

**Goal**: Production-shaped failure handling and metrics.

**Deliverables**:
- Active health checks: goroutine per backend, HTTP probe, N-consecutive-success/failure state machine.
- Passive outlier detection: sliding window of recent request outcomes, eject after N 5xx.
- Circuit breaker: 3-state (closed / open / half-open) per backend with trial-request cooldown.
- Prometheus metrics: request counter (labels: backend, method, status_class), latency histogram per backend, circuit breaker state gauge, active connections gauge. Bucket choices deliberate, no defaults.
- Structured logging via `log/slog`.
- Grafana dashboard JSON committed in `deployments/`.
- Multi-stage LB Dockerfile: build with `CGO_ENABLED=0` in a Debian Go image, ship the static binary in a distroless non-root runtime, expose all three listeners, and self-probe via a `probe` subcommand in a native `HEALTHCHECK`.
- Repo-root `docker-compose.yml` composing the LB image with the three dummy backends, Prometheus, and Grafana — the whole demonstrable system from one `docker compose up`.
- Orchestrator-probe-shaped health endpoint contract: `/livez`, `/readyz`, and `/startupz` on their own listener, returning structured JSON with liveness and readiness semantics and their own `lb_health_probe_total{endpoint, status}` counter.
- Sprint 3 retrospective (`docs/sprint-3-retro.md`).

**Exit criteria**:
- `docker stop` a backend → ejected within health threshold, circuit trips, recovers on restart.
- Grafana shows per-backend latency histograms and circuit state.
- Chaos test: injecting 500s on one backend eventually opens its circuit.
- The LB runs as a container via `docker compose up` at the repository root alongside backends, Prometheus, and Grafana.
- The health endpoint returns structured JSON with liveness and readiness semantics.
- Sprint 3 retrospective is written.

## Sprint 4 — Hard Subsystems

**Goal**: The subsystems that separate a toy LB from a defensible one.

**Deliverables**:
- Programmatic application seam: the whole wiring graph (metrics collector, backend registry with seeded series, circuit breaker, selector, proxy with its observers, active health checker, and the client/metrics/health-endpoint servers) behind an importable `internal/app` build/run API, with the reload operation added in place — so production, tests, and the chaos harness assemble the system the same way (Sprint 3 retro handoff).
- Zero-downtime SIGHUP reload: `atomic.Pointer[Config]` swap, diff-based backend add/remove, draining state for removed backends with configurable drain window.
- Connection lifecycle correctness: context propagation client→backend, clean handling of client cancellation mid-stream, backend death mid-response, slow-loris timeouts. `pprof` audit — no goroutine leaks under sustained load.
- Backend connection pool tuning via `http.Transport`: `MaxIdleConnsPerHost`, `IdleConnTimeout`, `DialContext` with timeout, `ResponseHeaderTimeout`.
- Config env-var interpolation for backend URLs (`${VAR}`), so deployment secrets stay out of the config file and validation errors never echo resolved values.
- ADR: reload architecture (atomic pointer swap vs SO_REUSEPORT — trade-offs).
- ADR: retry policy (or the deliberate absence of one) and why.
- ADR: deployment target decision (bare binary vs. Docker vs. Kubernetes), informed by the reload/health-check/connection-lifecycle work above rather than decided ahead of it. Deferred from Sprint 1 — see `docs/adr/0005-scope-of-production-grade.md`.

**Exit criteria**:
- SIGHUP with 1000 in-flight requests drops zero.
- Chaos test: `docker kill` a backend mid-response — client gets clean 502, no crash, no leak.
- 1-hour soak test under load — no goroutine or memory growth.

## Sprint 5 — HTTP/2, Benchmarks, Docs

**Goal**: Production-ready artifact with benchmarks and documentation.

**Deliverables**:
- HTTP/2 client-facing: TLS with self-signed cert + ALPN; also h2c via `golang.org/x/net/http2/h2c`.
- HTTP/2 to backends: `http.Transport` with `ForceAttemptHTTP2: true`.
- Benchmark rig in `bench/`: docker-compose with LB + Nginx + 4 backends and a vegeta load generator (wrk dropped — no HTTP/2 support; ADR-0020), response-size matrix (200B, 10KB, 1MB), and protocol and failure-mode benchmarks.
- Bounded load-generator memory (S5.T6.1): the `vegeta` container caps its Go heap (`GOMEMLIMIT`/`GOGC`, ADR-0021) so the 1 MB runs complete on a standard 8–16 GB laptop with Docker Desktop defaults; raising the Docker VM memory is explicitly not the fix.
- S5.T6 — the published run (absorbs S5.T7): vegeta peak-throughput discovery (binary-search the highest sustainable request rate — seed, double, bisect; thresholds fixed in `bench/run.sh`, not flags) plus the fixed-rate latency sweep at 30/50/70/90% of the discovered peak (`LATENCY_RATES_PCT`), because the sweep's rates are fractions of the peak found in the same invocation.
- Nearest-equivalent Nginx comparisons: every algorithm runs head-to-head with Nginx — round-robin and least-connections **matched**, consistent-hash (`hash $remote_addr consistent`, no bounded loads) and p2c-ewma (`random two least_conn`, no latency signal) **nearest-equivalent**, each stating its gap.
- A `degraded` slice: one backend 50 ms slow for the whole run, all four algorithms × both competitors at one absolute rate, headline result the per-backend request share.
- Reload runs: a **no-op reload** and a **drain reload** (one backend removed under load), each judged PASS/FAIL against criteria fixed before the run — zero non-2xx/transport errors, post-reload p99 ≤ 2× pre-reload p99, and the removed backend receives zero arrivals after the reload is applied.
- `make bench-repro` — the one-command reproducer (preflight, certs, images, smoke, full matrix) behind the exit criterion; S5.T11 (orchestration manifests) is dropped per ADR-0019.
- Published numbers: p50 / p99 / p99.9 and throughput ceilings, with `.txt` summaries and `.hdr` histograms committed alongside the harness.
- S5.T13 — `README.md` rewrite (three-sentence summary, Mermaid request-path diagram, quickstart, local live-demo section with a marked video slot and ADR-0023 for why there is no public URL, headline results limited to owner-approved claims) and `docs/architecture.md` diagrams (request path; package graph derived from `go list`).
- S5.T14 — `docs/design-decisions.md`: seven topics (`net/http/httputil` over frameworks, bounded-loads consistent hashing, P2C-EWMA including its no-decay limit, reload architecture, failure-mode interaction, honest Nginx comparison, concurrency model), each problem → options → choice → cost, each linking its ADRs and evidence.
- S5.T15 — `docs/what-id-do-differently.md`: honest retrospective, every item citing a source, with marked slots for owner-written items.
- S5.T19 — final portfolio pass: S5.T19.1.1 safe fixes and report-first scans, S5.T19.1.2 release verification (including a fresh-clone quickstart run), and S5.T19.2 the annotated `v0.1.0` tag, gated on the recorded demo video. No history rewrite.
- Reader-facing documents (S5.T13–T15) follow the writing rules in the bundle spec.
- S5.T16 — local live-demo stack under `demo/` (ADR-0023; supersedes the original live public deployment): one LB per algorithm sharing four backends, an eight-client Zipf-ranked Poisson traffic generator with a runtime rate control, a dummy-backend admin listener for runtime latency/jitter/failure, and a control service plus a single page that embeds the existing Grafana dashboard. All ports bound to `127.0.0.1`. Gated by a two-phase latency-isolation acceptance check.
- S5.T17 — demo script: scenario sequence, click path, and expected on-screen state for each step, used to record the demo video.

**Exit criteria**:
- `make bench-repro` (built on `bench/run.sh`) reproduces all published numbers from a clean checkout, refusing to start on a dirty tree, running containers, or fewer than eight Docker vCPUs.
- README opens with architecture diagram and 3-sentence project summary.
- Design decisions doc covers: why stdlib over frameworks, why bounded-loads CH, why P2C, reload architecture, failure-mode interaction, and honest benchmark comparison with Nginx.
- One-command local demo (`demo/`) passes its latency-isolation acceptance check, and a recorded demo video following the S5.T17 script exists.
- The repository is tagged `v0.1.0` (annotated) only after the hygiene and release-verification tickets pass and the README links a real recorded demo video.

## Post-Sprint 5 — Optional extensions

Documented, not committed to:
- SO_REUSEPORT socket handoff for reload across process restart.
- gRPC pass-through.
- Rate limiting.
- Request retries with idempotency-aware policy.
- WebSocket upgrade path validation.
