# 07: Active-Connections Gauge

**What to build:** `lb_active_connections` tracks each backend's live
in-flight request count, wired at the exact call sites `proxy` already uses
to maintain `Backend.ActiveConns()`, so the gauge can never drift from the
real count.

**Blocked by:** 01 (needs the `Collector`)

**Status:** ready-for-agent

- [ ] `lb_active_connections` incremented alongside `Backend.IncActive()`
      and decremented alongside `Backend.DecActive()` in `proxy`, at the
      same call sites (dispatch, and the response-body-`Close()` wrapper)
- [ ] Every backend's `lb_active_connections` seeded to `0` in `main.go`
      immediately after `backend.NewRegistry` succeeds, using the same
      `Collector` method real updates use — not a separate seeding code
      path
- [ ] Test: the gauge tracks a sequence of concurrent in-flight requests
      correctly (mirroring S1.T6's `ActiveConns`-returns-to-0 leak-check
      pattern, now asserted against the metric instead of/alongside
      `Backend.ActiveConns()`)
- [ ] Test: after `NewRegistry` and before any request is served, every
      backend's `lb_active_connections` series exists and reads `0`
- [ ] Test: every existing proxy test continues to pass unchanged
- [ ] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments
