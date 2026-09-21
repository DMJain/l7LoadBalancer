# 01: S3.T6.5 — Active Checker Reinstatement Gate (`==` → `>=`)

**What to build:** Close the drift between [ADR-0011](../../../docs/adr/0011-health-passive-outlier-and-circuit-breaker-composition.md)
decision 3 (stated intent — a passively-ejected backend recovers via the
next successful active probe) and the shipped active checker (whose
reinstatement gate uses `==` against the consecutive-successes
accumulator, so a backend whose accumulator already crossed `M` in
background probing while passive detection ejected it under load never
fires `MarkHealthy`, leaving `lb_backend_healthy` stuck at `0` while
`IsHealthy()` reads `false` until the process restarts). One-line
behavioural change in the active checker plus a targeted unit test that
reproduces the scenario at checker scope, plus an appended "Amendment"
section on ADR-0011 dating the drift and the fix.

Prerequisite: unblocks S3.T8's combined-signal recovery arc (iii), whose
gauge assertion would otherwise fail on this exact scenario.

**Blocked by:** None (can start immediately)

**Status:** done

- [x] Active checker's reinstatement gate compares the
      consecutive-successes accumulator with `>=` against `M`, not `==`
- [x] New unit test in `internal/health/checker_test.go`: seeds a
      backend with `healthy = false` (simulating a prior passive-detection
      ejection), drives `M` consecutive successful probes through the
      checker's normal loop, asserts `Backend.IsHealthy()` reads `true`
      after the `M`th cycle, and asserts exactly one
      `event=health.transition` `slog` record fires with the correct
      frozen `reason` string from S3.T5.2 (structured field-allowlist
      match on `event`+`backend`+`reason`, never substring)
- [x] Under the current `==` gate, the test would fail; under the `>=`
      gate, it passes — the test is the mechanical verification of the
      fix, not an incidental regression check
- [x] ADR-0011 gains a dated "Amendment (2026-09-22): Reinstatement gate
      uses `>=`, not `==`" section appended after "Alternatives
      considered," one paragraph: the scenario that exposes the bug, the
      one-line fix, no tradeoff discussion; this is a shipped-code drift
      correction, not a new decision (no new ADR)
- [x] No metrics collector, no observer fan-out, no proxy wiring
      touched — the fix is at the checker layer and S3.T6.3 already
      verifies the observer→gauge wiring below it
- [x] `make test`, `make test-race`, `go vet`, `make fmt` clean
- [x] `PROGRESS.md`: this ticket added, Sprint 3 status line updated to
      "Sprint 3 in progress — T1–T7 done, T8–T9 chaos tests remaining,"
      this ticket flipped to `[DONE]` on completion in the same atomic
      commit
- [x] `AGENTS.md`'s ADR table: ADR-0011 row unchanged (an amendment
      section within an existing ADR is not a new row)
- [x] Session log entry appended to `docs/sessions/2026-09-22-<agent>.md`
      with the drift narrative

## Comments

Completed 2026-09-22 by opencode (S3.T6.5). Red-first: the new
`TestProberReinstatesPassivelyEjectedBackendWithAccumulatorPastThreshold`
failed against the shipped `==` gate (0 reinstatement records), passed after
the one-line `>=` change. `internal/health/checker.go`'s `probeOnce` doc
comment updated to explain the failure/success gate asymmetry; ADR-0011
gained the dated 2026-09-22 amendment. No metrics/observer/proxy code
touched. `make test`, `make test-race`, `go vet`, `make fmt` all clean.
`failure == N` deliberately left as-is (not the reported defect).
