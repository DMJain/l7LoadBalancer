# 02: Logger Test Coverage

**What to build:** `internal/logger` gets its first test file. It has
shipped since Sprint 1 with zero test coverage; this closes that gap.
Unrelated to every other ticket in this batch — no shared code, no shared
call sites.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] `internal/logger/logger_test.go` created
- [ ] Test: `logger.New` at a given `slog.Level` emits at that level and
      above, and suppresses below it
- [ ] Test: output is well-formed JSON (via `slog.NewJSONHandler`) with the
      expected top-level shape
- [ ] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments
