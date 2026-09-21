# 03: Canonical Transition-Log Vocabulary

**What to build:** Two new canonical `log/slog` fields — `event` and
`reason` — added to `internal/logger`'s frozen field vocabulary, with
closed, Go-constant, snake_case value sets. No behavior change: nothing
logs these fields yet. This is a shared prefactor so tickets 04 and 05
don't independently invent overlapping or inconsistent vocabulary for the
same underlying events.

**Blocked by:** None (can start immediately)

**Status:** done

- [x] `internal/logger/doc.go`'s canonical field list gains `event` and
      `reason`, documented alongside the six existing request-scoped
      fields (`backend`, `method`, `status`, `latency_ms`, `remote_addr`,
      `path`), noting these two are transition-scoped rather than
      request-scoped
- [x] `event` vocabulary defined as Go constants, snake_case, matching the
      convention already established for algorithm identifiers
      (`round_robin`, etc.): `health_ejected`, `health_reinstated`,
      `circuit_opened`, `circuit_closed`, `circuit_half_opened`
- [x] `reason` vocabulary defined the same way: `probe_failures`,
      `probe_recovered` (active health), `outlier_window` (passive
      detection), `consecutive_failures`, `trial_success`, `trial_failure`,
      `cooldown_elapsed` (circuit)
- [x] Each constant's doc comment states which subsystem/transition it
      belongs to, so tickets 04 and 05 can reference them without
      re-deriving the mapping
- [x] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments

Completed 2026-09-21 by opencode (S3.T5.2). Added `internal/logger/vocab.go`
with the two closed vocabularies as exported Go constants — `Event*` (5) and
`Reason*` (7) — each documenting its subsystem/transition and, where one
exists, its paired `event`/`reason`. `internal/logger/doc.go` now splits its
field list into request-scoped (the original six) and transition-scoped
(`event`, `reason`). `internal/logger` stays a leaf: no new import.

Test-first: `vocab_test.go` pins every constant's exact string and asserts each
vocabulary is duplicate-free and snake_case, so a typo'd variant cannot enter
the closed set; it compiled against undefined constants (Red) before `vocab.go`
landed (Green). `go test -cover ./internal/logger/...` → 100.0%. No new ADR:
ADR-0013 decision 12 already fixes the value sets; this ticket only implements
them. `docs/design/sprint-1-contracts.md`'s Sprint-1 log vocabulary was
deliberately left untouched (issue scope named `doc.go` only).
