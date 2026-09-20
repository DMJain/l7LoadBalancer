# 04: Health Transition Log Lines

**What to build:** The active health checker and passive outlier detector
each emit exactly one structured log line per genuine ejection or
reinstatement — not once per probe or per request for the duration of a
failure/success streak. Verifiable entirely through captured log output;
no metrics involvement.

**Blocked by:** 03 (needs the `event`/`reason` vocabulary)

**Status:** ready-for-agent

- [ ] Both `internal/health.Checker` and `internal/health.OutlierDetector`
      gain a `*slog.Logger` (new constructor parameter)
- [ ] The active checker's log emission is gated on an **exact-equality**
      check against its existing consecutive success/failure counters
      (`== threshold`), distinct from the `>=` check the `Mark*` calls
      themselves already use — reusing the counters `prober` already owns,
      no new field added to it
- [ ] Active checker logs `event=health_ejected, reason=probe_failures` at
      WARN on the failure-threshold crossing, and
      `event=health_reinstated, reason=probe_recovered` at INFO on the
      success-threshold crossing, each including the `backend` field
- [ ] The passive outlier detector's ejection log line reuses its existing
      `ejected`-per-episode guard (the same one that already makes
      `MarkUnhealthy` fire exactly once per episode) — no new
      edge-triggering logic
- [ ] Outlier detector logs `event=health_ejected, reason=outlier_window`
      at WARN, exactly once per ejection episode
- [ ] Test: a sustained multi-probe failure streak (beyond the threshold)
      produces exactly one `health_ejected` log line, not one per probe
- [ ] Test: a sustained multi-probe success streak (beyond the threshold)
      after an ejection produces exactly one `health_reinstated` log line
- [ ] Test: a failure burst within the outlier window that crosses the
      eject threshold produces exactly one `health_ejected` log line, not
      one per failure in the burst
- [ ] Test: every existing S3.T1/S3.T2 test continues to pass unchanged
- [ ] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments
