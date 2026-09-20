# 03: Canonical Transition-Log Vocabulary

**What to build:** Two new canonical `log/slog` fields — `event` and
`reason` — added to `internal/logger`'s frozen field vocabulary, with
closed, Go-constant, snake_case value sets. No behavior change: nothing
logs these fields yet. This is a shared prefactor so tickets 04 and 05
don't independently invent overlapping or inconsistent vocabulary for the
same underlying events.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] `internal/logger/doc.go`'s canonical field list gains `event` and
      `reason`, documented alongside the six existing request-scoped
      fields (`backend`, `method`, `status`, `latency_ms`, `remote_addr`,
      `path`), noting these two are transition-scoped rather than
      request-scoped
- [ ] `event` vocabulary defined as Go constants, snake_case, matching the
      convention already established for algorithm identifiers
      (`round_robin`, etc.): `health_ejected`, `health_reinstated`,
      `circuit_opened`, `circuit_closed`, `circuit_half_opened`
- [ ] `reason` vocabulary defined the same way: `probe_failures`,
      `probe_recovered` (active health), `outlier_window` (passive
      detection), `consecutive_failures`, `trial_success`, `trial_failure`,
      `cooldown_elapsed` (circuit)
- [ ] Each constant's doc comment states which subsystem/transition it
      belongs to, so tickets 04 and 05 can reference them without
      re-deriving the mapping
- [ ] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments
