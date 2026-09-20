# 08: Backend-Healthy Gauge

**What to build:** `lb_backend_healthy` reflects each backend's current
health, driven by the exact same edge-triggered signal ticket 04 already
introduced for the health transition log lines — not a second,
independently written detection path.

**Blocked by:** 01 (needs the `Collector`), 04 (needs the edge-triggering
signal)

**Status:** ready-for-agent

- [ ] `internal/health.Checker` and `internal/health.OutlierDetector` each
      gain a `*metrics.Collector` (or equivalent) alongside the
      `*slog.Logger` ticket 04 added, via the same constructors
- [ ] `lb_backend_healthy` set at the same `==`-gated (active checker) and
      `ejected`-guarded (outlier detector) call sites the log lines already
      fire from — one shared signal driving both the log line and the
      gauge
- [ ] Every backend's `lb_backend_healthy` seeded to `1` in `main.go`
      immediately after `backend.NewRegistry` succeeds, using the same
      `Collector` method real transitions use
- [ ] Test: a backend crossing the failure threshold sets its gauge to `0`
      exactly once per transition (mirroring the log-line exactly-once
      test from ticket 04, now asserted against the metric)
- [ ] Test: a backend crossing the success threshold after ejection sets
      its gauge back to `1`
- [ ] Test: after `NewRegistry` and before any probe cycle runs, every
      backend's `lb_backend_healthy` series exists and reads `1`
- [ ] Test: every existing S3.T1/S3.T2/ticket-04 test continues to pass
      unchanged
- [ ] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments
