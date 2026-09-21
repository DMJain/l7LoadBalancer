# 07: Active-Connections Gauge

**What to build:** `lb_active_connections` tracks each backend's live
in-flight request count, wired at the exact call sites `proxy` already uses
to maintain `Backend.ActiveConns()`, so the gauge can never drift from the
real count.

**Blocked by:** 01 (needs the `Collector`)

**Status:** done

- [x] `lb_active_connections` incremented alongside `Backend.IncActive()`
      and decremented alongside `Backend.DecActive()` in `proxy`, at the
      same call sites (dispatch, and the response-body-`Close()` wrapper)
- [x] Every backend's `lb_active_connections` seeded to `0` in `main.go`
      immediately after `backend.NewRegistry` succeeds, using the same
      `Collector` method real updates use — not a separate seeding code
      path
- [x] Test: the gauge tracks a sequence of concurrent in-flight requests
      correctly (mirroring S1.T6's `ActiveConns`-returns-to-0 leak-check
      pattern, now asserted against the metric instead of/alongside
      `Backend.ActiveConns()`)
- [x] Test: after `NewRegistry` and before any request is served, every
      backend's `lb_active_connections` series exists and reads `0`
- [x] Test: every existing proxy test continues to pass unchanged
- [x] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments

- 2026-09-21 — opencode (S3.T6.2): implemented. `reqState` gained a `metrics`
  reference (copied from `Proxy.metrics` in `ServeHTTP`) and a symmetric
  `activate()` next to the existing `release()`, so `IncActive`/
  `IncActiveConnections` and `DecActive`/`DecActiveConnections` are called from
  the same sites — dispatch, and the once-guarded response-body-`Close()`
  release (also the `ErrorHandler` path). `main.go` builds the collector before
  `backend.NewRegistry` and calls a new `seedMetrics(collector, reg)`
  immediately after it; that helper seeds every backend to `0` via
  `Collector.SetActiveConnections`. The seeding method is `SetActiveConnections`
  rather than the Inc/Dec pair because Prometheus `Vec` metrics materialize no
  series until first written and Inc/Dec cannot produce a `0` series — ADR-0013
  decision 9 names this explicitly ("the active-connections gauge's real
  transitions use `IncActiveConnections`/`DecActiveConnections`, so seeding its
  initial `0` uses `SetActiveConnections`"), and it is the same `Collector`
  method family real updates use, not a separate code path. Tests:
  `TestProxyActiveConnectionsGaugeTracksConcurrentInFlightRequests` (100
  concurrent blocked requests; asserts `Backend.ActiveConns()` and the gauge
  both read 100 in flight and both drain to 0) and
  `TestSeedMetricsActiveConnections` (registry + seed; asserts both backends'
  series exist at `0` via `prometheus/testutil.GatherAndCompare`, which added
  `github.com/kylelemons/godebug` to `go.mod` as a test-only indirect dep of
  `client_golang`'s testutil — already in `go.sum`). All existing proxy tests
  pass unchanged. `make test`/`make test-race` green, `go vet` clean,
  `make fmt` no diff.
