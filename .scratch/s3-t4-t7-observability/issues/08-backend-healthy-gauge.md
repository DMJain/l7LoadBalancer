# 08: Backend-Healthy Gauge

**What to build:** `lb_backend_healthy` reflects each backend's current
health, driven by the exact same edge-triggered signal ticket 04 already
introduced for the health transition log lines — not a second,
independently written detection path.

**Blocked by:** 01 (needs the `Collector`), 04 (needs the edge-triggering
signal)

**Status:** done

- [x] `internal/health.Checker` and `internal/health.OutlierDetector` each
      gain a `*metrics.Collector` (or equivalent) alongside the
      `*slog.Logger` ticket 04 added, via the same constructors
- [x] `lb_backend_healthy` set at the same `==`-gated (active checker) and
      `ejected`-guarded (outlier detector) call sites the log lines already
      fire from — one shared signal driving both the log line and the
      gauge
- [x] Every backend's `lb_backend_healthy` seeded to `1` in `main.go`
      immediately after `backend.NewRegistry` succeeds, using the same
      `Collector` method real transitions use
- [x] Test: a backend crossing the failure threshold sets its gauge to `0`
      exactly once per transition (mirroring the log-line exactly-once
      test from ticket 04, now asserted against the metric)
- [x] Test: a backend crossing the success threshold after ejection sets
      its gauge back to `1`
- [x] Test: after `NewRegistry` and before any probe cycle runs, every
      backend's `lb_backend_healthy` series exists and reads `1`
- [x] Test: every existing S3.T1/S3.T2/ticket-04 test continues to pass
      unchanged
- [x] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments

Completed 2026-09-21 by opencode (S3.T6.3). `health.Checker`/`New` and
`OutlierDetector`/`NewOutlierDetector` each gained a `*metrics.Collector`
alongside the `*slog.Logger` ticket 04 added, so both observability surfaces
are wired at the same construction sites in `main.go` (spec story 35).

The gauge is written inside the exact `if` blocks the ticket-04 log lines fire
from — the active checker's two `==`-gated, genuine-transition-guarded branches
and the passive detector's `ejected`-per-episode guard — so no second detection
path exists and the log/gauge pair cannot disagree (spec story 34). The gauge
shares the log line's once-per-streak/once-per-episode edge trigger; the new
tests assert the metric value at the transition and the paired log-line count,
proving the shared signal.

`main.go`'s `seedMetrics` seeds every backend to `1` via the same
`Collector.SetBackendHealthy` method real transitions use (spec story 37).
Prometheus `Vec` metrics materialize no series until first written, so the seed
is what makes the healthy panel non-blank on the first scrape (ADR-0013
decision 9).

Existing S3.T1/S3.T2/ticket-04 tests pass unchanged in outcome; only their
constructor calls were updated for the new arity (`metrics.NewCollector()`),
matching the precedent ticket 04 set when it added the logger parameter. No new
ADR (ADR-0013 decision 6 records the design). No metrics involvement beyond the
one gauge this ticket owns; the circuit-state gauge is S3.T6.4.
