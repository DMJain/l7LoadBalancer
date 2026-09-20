# 01: Metrics Package

**What to build:** `internal/metrics` becomes a real, leaf-only Prometheus
`Collector` — request counter, latency histogram, backend-healthy gauge,
circuit-state gauge, and a new active-connections gauge — plus a
`metrics.listen` config field and a working `/metrics` exposition endpoint.
Nothing else in the codebase calls into it yet; this ticket only builds and
proves the package itself.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] `internal/metrics` gains **no** internal imports — stays a true leaf,
      per the frozen package dependency graph in `AGENTS.md`
- [ ] A `Collector` type wraps `github.com/prometheus/client_golang`, owning
      its own private `prometheus.NewRegistry()` (not
      `prometheus.DefaultRegisterer`), safe for independent, parallel
      construction (`t.Parallel()`-safe, no package-level `sync.Once`)
- [ ] `lb_requests_total` (`CounterVec`) and `lb_request_duration_seconds`
      (`HistogramVec`) implemented with labels `backend`, `method`,
      `status_class` exactly as reserved in `doc.go` — never `status_code`
- [ ] Histogram buckets are the provisional set already proposed in
      `doc.go` (`.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10` seconds),
      with a comment noting they're provisional pending Sprint 5 benchmark
      data
- [ ] `lb_backend_healthy` (`GaugeVec`, label `backend`) implemented as a
      plain 0/1 gauge
- [ ] `lb_circuit_state` (`GaugeVec`, labels `backend`, `state`) implemented
      as a label-enum (`state="closed"/"open"/"half_open"`); its setter
      method **unconditionally** sets the target state's series to `1` and
      the other two to `0` for that backend on every call — not just the
      first call for that backend — so the exactly-one-state-is-`1`
      invariant can never be broken by a later caller that only sets the
      new state
- [ ] New `lb_active_connections` (`GaugeVec`, label `backend`) added,
      amending the metric-name reservations in `internal/metrics/doc.go`
      and `docs/design/sprint-1-contracts.md` — no new ADR number, this
      fills an acknowledged `MILESTONES.md` gap rather than reversing a
      locked decision
- [ ] `Config` gains a new `metrics:` section (`listen`, following the
      `HealthConfig`/`CircuitConfig` nil-means-omitted pointer pattern),
      defaulting to `:9090` when omitted, always-on (no disable toggle);
      `KnownFields(true)` strictness preserved
- [ ] `/metrics` served via `promhttp.HandlerFor(registry, ...)` on its own
      `http.Server`, separate from the client-traffic listener, started and
      shut down in `main.go` alongside the existing server, sharing the
      existing `sigCtx`
- [ ] Test: each instrument's basic Inc/Observe/Set behavior, via the
      `Collector`'s own method API (no HTTP)
- [ ] Test: the circuit-state setter zeroes the other two states after two
      sequential transitions for the same backend, asserting all three
      series, not just the latest one
- [ ] Test: `/metrics` exposition (via `httptest.NewServer` wrapping
      `promhttp.HandlerFor`) contains every reserved-plus-new metric name
      and label combination after driving the collector through its
      methods
- [ ] Test: `metrics.listen` omitted → `:9090`; explicit value → kept;
      nested unknown-field typo under `metrics:` → rejected (matching
      S3.T0.3's config test shape)
- [ ] `go.mod`/`go.sum` updated for the new `github.com/prometheus/
      client_golang` dependency
- [ ] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments
