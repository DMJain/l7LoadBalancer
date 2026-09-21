# 04: Health Transition Log Lines

**What to build:** The active health checker and passive outlier detector
each emit exactly one structured log line per genuine ejection or
reinstatement — not once per probe or per request for the duration of a
failure/success streak. Verifiable entirely through captured log output;
no metrics involvement.

**Blocked by:** 03 (needs the `event`/`reason` vocabulary)

**Status:** done

- [x] Both `internal/health.Checker` and `internal/health.OutlierDetector`
      gain a `*slog.Logger` (new constructor parameter)
- [x] The active checker's log emission is gated on an **exact-equality**
      check against its existing consecutive success/failure counters
      (`== threshold`), distinct from the `>=` check the `Mark*` calls
      themselves already use — reusing the counters `prober` already owns,
      no new field added to it
- [x] Active checker logs `event=health_ejected, reason=probe_failures` at
      WARN on the failure-threshold crossing, and
      `event=health_reinstated, reason=probe_recovered` at INFO on the
      success-threshold crossing, each including the `backend` field
- [x] The passive outlier detector's ejection log line reuses its existing
      `ejected`-per-episode guard (the same one that already makes
      `MarkUnhealthy` fire exactly once per episode) — no new
      edge-triggering logic
- [x] Outlier detector logs `event=health_ejected, reason=outlier_window`
      at WARN, exactly once per ejection episode
- [x] Test: a sustained multi-probe failure streak (beyond the threshold)
      produces exactly one `health_ejected` log line, not one per probe
- [x] Test: a sustained multi-probe success streak (beyond the threshold)
      after an ejection produces exactly one `health_reinstated` log line
- [x] Test: a failure burst within the outlier window that crosses the
      eject threshold produces exactly one `health_ejected` log line, not
      one per failure in the burst
- [x] Test: every existing S3.T1/S3.T2 test continues to pass unchanged
- [x] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments

Completed 2026-09-21 by opencode (S3.T5.3). `Checker`/`New` and
`OutlierDetector`/`NewOutlierDetector` each gained an unexported `log
*slog.Logger`; `main.go` threads its `log` into `health.New` and
`newHandler` → `health.NewOutlierDetector`.

The active checker logs at its `probeOnce` threshold crossings. Issue 04's
exact-equality gate (`failures == probeFailuresBeforeUnhealthy`,
`successes == probeSuccessesBeforeHealthy`) is retained, but on its own it
also fires for a backend that never left the healthy state (every backend's
first M successes, since `NewRegistry` starts healthy) and for one already
ejected by passive detection. Raised with the owner during the session; the
agreed fix is a second condition reading `Backend.IsHealthy()` before the
idempotent `Mark*` call, so a line describes a genuine transition. The guard
uses the backend's existing atomic health, so no new `prober` field is added
and no genuine transition is missed. The passive detector logs inside its
existing `!w.ejected && failures >= threshold` block, so it shares the exact
flag that already makes `MarkUnhealthy` fire once per episode.

New `internal/health/transition_log_test.go` uses the `proxy_test.go` buffered
`slog.JSONHandler` seam: exactly-one-line-per-streak/episode, the WARN/INFO
level, the `event`/`reason` pair, and the `backend` field, plus two tests
pinning the genuine-transition guards. Existing S3.T1/S3.T2 test assertions
are unchanged; only their constructor calls were updated for the new arity
(`discardLogger()` in-package, `slog.Default()` in `proxy_test.go`). No metrics
involvement; no new ADR (ADR-0013 decision 11 records the design).
