# 02: S3.T8 — Chaos Test: Backend Eviction and Recovery

**What to build:** Two chaos-scenario arcs that verify the
`Backend.healthy` axis end-to-end against the full Sprint 3 assembly
(registry + selector + proxy + `health.Checker` + outlier +
`circuit.Breaker` + `metrics.Collector`), plus a shell-scripted
docker-compose smoke closing MILESTONES.md's literal "`docker stop` a
backend" wording. Lands the shared chaos-test harness (new `test/chaos/`
external test package, local `assemble()` helper duplicating `main.go`'s
wiring, `flippableBackend` helper for failure injection) that S3.T9
reuses. Every timing knob (probe interval, probe timeout, circuit
cooldown) uses the config-driven fast-path values per ADR-0011 decision
10; every timed assertion uses `require.Eventually` with `-race`-generous
deadlines, never `time.Sleep(exact)`.

**Blocked by:** 01 (S3.T6.5 reinstatement gate fix)

**Status:** ready-for-agent

### Shared harness (lands with this ticket, reused by S3.T9)

- [ ] New `test/chaos/` package at the repo root, files in `package
      chaos_test` (external test package, so the import graph mirrors a
      consumer's and the acyclic guarantee stays visible)
- [ ] Local `assemble(t, cfg)` helper duplicates `main.go`'s Sprint 3
      wiring (registry, selector via `balancer.NewFromConfig`, proxy,
      `health.Checker`, outlier detector, `circuit.Breaker`,
      `metrics.Collector`, `Registry.SetCircuitGate`, `RoundTripObserver`
      registrations); returns a handle exposing the proxy `http.Handler`,
      the metrics collector, and the captured `slog.Handler`
- [ ] `helpers_test.go` in the same package with a shared
      `flippableBackend`: `Serve200()`, `Serve500()`, `Kill()`,
      `Restart()`, `URL() string`; `Kill()` closes the underlying
      listener (real `ECONNREFUSED` for probes and proxy dispatch, not
      a handler swap to HTTP 503); `Serve200()`/`Serve500()` swap the
      served handler through an `atomic.Pointer[http.Handler]` so the
      swap is safe under concurrent request/probe goroutines with no
      mutex around the request path
- [ ] Timing config for the fast test path: `probeInterval = 20ms`,
      `probeTimeout = 100ms`, `circuitCooldown = 200ms`; thresholds
      (consecutive success/failure for active, sliding window for
      passive, `circuitFailuresBeforeOpen`) stay at their compile-time
      constants
- [ ] Gauge assertions via
      `prometheus/client_golang/prometheus/testutil.ToFloat64` against
      the assembled `metrics.Collector`'s registered gauges (S3.T6.3
      precedent), keyed by the `backend` label (and `state` label for
      circuit-state assertions)
- [ ] Log assertions via a helper `assertTransitionLogged(t, captured,
      event, backend, reason)` that decodes each captured `slog.Record`
      and performs field-allowlist matching on exactly `event`,
      `backend`, and `reason` against S3.T5.2's frozen vocabulary
      strings — never substring, never regex, never full record equality
- [ ] All timed assertions use `require.Eventually` with deadlines ≥ 2s
      to absorb `-race` overhead on the slowest CI executor; no fixed
      `time.Sleep(exact)` beyond initial baseline setup

### T8 arc (i) — active-only eviction and recovery

- [ ] Baseline: three healthy `flippableBackend`s all `Serve200()`;
      `lb_backend_healthy{backend=X}=1` for each; no `health.transition`
      records in the captured handler
- [ ] `Kill()` on backend X → within the `require.Eventually` deadline:
      gauge flips to `0`; exactly one
      `event=health.transition, backend=X, reason=<vocab>` record;
      selector no longer chooses X for a burst of subsequent requests
- [ ] `Restart()` on backend X → within the deadline: gauge flips back
      to `1`; exactly one reinstatement record; selector resumes
      choosing X across a burst of subsequent requests

### T8 arc (iii) — combined 500 signal, health-axis recovery only

- [ ] Baseline as above
- [ ] `Serve500()` on backend X → within the deadline:
      `lb_backend_healthy{backend=X}=0`,
      `lb_circuit_state{backend=X,state="open"}=1`; one
      `event=health.transition` record; one `event=circuit.transition`
      record
- [ ] `Serve200()` on backend X → within the deadline: gauge back to
      `1`; one reinstatement record; **also assert**
      `lb_circuit_state{backend=X,state="open"}` remains at `1` at the
      same checkpoint (cross-gate independence per ADR-0011 decision 1
      — verifies the ADR's orthogonality claim mechanically)
- [ ] Do **not** assert HTTP-traffic restoration in this arc — that
      lives on the circuit axis and belongs to S3.T9; asserting it here
      would either flake on cooldown or accidentally pass by timing

### Docker-compose smoke

- [ ] New `deployments/docker/chaos/` with `eviction.sh` and a shared
      `README.md` (also used by S3.T9's `circuit.sh`)
- [ ] `eviction.sh` starts with `set -euo pipefail`, passes `bash -n`
      and `shellcheck`
- [ ] Script assumes LB runs as host process (`go build -o ./l7-lb
      ./cmd/l7LoadBalancer && ./l7-lb -config …` — Go toolchain
      required on host; Dockerfile is S3.T10, not this bundle); backends
      come from the existing S1.T9 compose at
      `deployments/docker/docker-compose.yml`; no new compose file
- [ ] Assertions go through the LB's Prometheus HTTP API on
      `metrics.listen` (default `:9090`) — S3.T7 precedent, not
      Grafana-eyeballing
- [ ] Scenario: `docker stop backend-a` → assert
      `lb_backend_healthy{backend="backend-a"}=0` via a Prometheus
      instant query with a polled retry window; `docker start backend-a`
      → assert gauge back to `1` via the same query
- [ ] `README.md` documents prereqs (Go toolchain on host, LB as host
      process, backends via `docker compose -f
      deployments/docker/docker-compose.yml up -d`), the exact
      Prometheus query strings, and the expected result for each step

### Existing coverage preserved

- [ ] `internal/proxy/proxy_test.go`'s existing
      `TestProxyCircuitOpensOnRepeated5xxAndStopsRouting` and
      passive-ejection tests remain untouched
- [ ] No prod code outside `internal/health` (from ticket 01) is edited
      by this ticket

### Close-out

- [ ] `make test`, `make test-race`, `go vet`, `make fmt` clean; the
      chaos package is included in `make test`/`make test-race`
- [ ] End-to-end `docker compose up` execution is
      `[MANUAL VERIFICATION PENDING]` in the session log per S3.T7
      precedent; the shell half's `[DONE]` gate is script existing +
      `bash -n` + `shellcheck` + documented Prometheus queries with
      expected values
- [ ] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion in one atomic commit
- [ ] Session log entry appended to `docs/sessions/<YYYY-MM-DD>-<agent>.md`
      including the **Sprint 4 convergence pointer** (the local
      `assemble()` helper duplicates ~20 lines of `main.go` wiring and
      will either converge with Sprint 4's `Run(ctx, cfg)` seam or
      become dead code)

## Comments
