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
- `ConsistentHashBoundedLoads` per Mirrokni-Thorup-Zadimoghaddam (2016) — per-backend load counters, rehash when a backend is over capacity.
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

**Exit criteria**:
- `docker stop` a backend → ejected within health threshold, circuit trips, recovers on restart.
- Grafana shows per-backend latency histograms and circuit state.
- Chaos test: injecting 500s on one backend eventually opens its circuit.

## Sprint 4 — Hard Subsystems

**Goal**: The subsystems that separate a toy LB from a defensible one.

**Deliverables**:
- Zero-downtime SIGHUP reload: `atomic.Pointer[Config]` swap, diff-based backend add/remove, draining state for removed backends with configurable drain window.
- Connection lifecycle correctness: context propagation client→backend, clean handling of client cancellation mid-stream, backend death mid-response, slow-loris timeouts. `pprof` audit — no goroutine leaks under sustained load.
- Backend connection pool tuning via `http.Transport`: `MaxIdleConnsPerHost`, `IdleConnTimeout`, `DialContext` with timeout, `ResponseHeaderTimeout`.
- ADR: reload architecture (atomic pointer swap vs SO_REUSEPORT — trade-offs).
- ADR: retry policy (or the deliberate absence of one) and why.

**Exit criteria**:
- SIGHUP with 1000 in-flight requests drops zero.
- Chaos test: `docker kill` a backend mid-response — client gets clean 502, no crash, no leak.
- 1-hour soak test under load — no goroutine or memory growth.

## Sprint 5 — HTTP/2, Benchmarks, Docs

**Goal**: Production-ready artifact with benchmarks and documentation.

**Deliverables**:
- HTTP/2 client-facing: TLS with self-signed cert + ALPN; also h2c via `golang.org/x/net/http2/h2c`.
- HTTP/2 to backends: `http.Transport` with `ForceAttemptHTTP2: true`.
- Benchmark rig in `bench/`: docker-compose with LB + Nginx + 4 backends, wrk and vegeta harness, response-size matrix (200B, 10KB, 1MB), concurrency sweep, failure-mode benchmarks.
- Published numbers: p50 / p99 / p99.9 at 50% and 90% of peak, plus throughput ceilings. Raw wrk/vegeta output committed alongside.
- README with architecture diagram (Mermaid or Excalidraw export).
- `docs/design-decisions.md` — rationale-heavy doc covering every non-trivial choice.
- `docs/what-id-do-differently.md` — honest retrospective.

**Exit criteria**:
- `bench/run.sh` reproduces all published numbers from a clean checkout.
- README opens with architecture diagram and 3-sentence project summary.
- Design decisions doc covers: why stdlib over frameworks, why bounded-loads CH, why P2C, reload architecture, failure-mode interaction, and honest benchmark comparison with Nginx.

## Post-Sprint 5 — Optional extensions

Documented, not committed to:
- SO_REUSEPORT socket handoff for reload across process restart.
- gRPC pass-through.
- Rate limiting.
- Request retries with idempotency-aware policy.
- WebSocket upgrade path validation.
