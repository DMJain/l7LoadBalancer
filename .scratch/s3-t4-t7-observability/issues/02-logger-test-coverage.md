# 02: Logger Test Coverage

**What to build:** `internal/logger` gets its first test file. It has
shipped since Sprint 1 with zero test coverage; this closes that gap.
Unrelated to every other ticket in this batch — no shared code, no shared
call sites.

**Blocked by:** None (can start immediately)

**Status:** done

- [x] `internal/logger/logger_test.go` created
- [x] Test: `logger.New` at a given `slog.Level` emits at that level and
      above, and suppresses below it
- [x] Test: output is well-formed JSON (via `slog.NewJSONHandler`) with the
      expected top-level shape
- [x] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments

Completed 2026-09-21 by opencode (S3.T5.1). `logger.New` binds `os.Stdout`
at handler-construction time and its frozen `New(level)` signature takes no
writer, so the tests capture output by swapping `os.Stdout` for an `os.Pipe`
around the `New` call and reading the pipe back. Two tests: a table over
DEBUG/INFO/WARN/ERROR asserting the emitted record set is exactly
"configured level and above", and a JSON-shape test asserting a bare record
has exactly `time`/`level`/`msg` while attributes land as top-level keys.
Coverage 100.0% of `internal/logger` statements. Test-only ticket — the
production `logger.New` implementation is unchanged.
