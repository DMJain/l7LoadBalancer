# 09: Circuit-State Gauge

**What to build:** `lb_circuit_state` reflects each backend's current
circuit state, driven by the exact same `CircuitTransition` signal ticket
05 already introduced for the circuit transition log lines — not a second,
independently written detection path. Inherits ticket 05's documented,
permanent gap around `Selectable()`-scan-won promotions (this gauge won't
update in that case either, for the same reason the log line doesn't).

**Blocked by:** 01 (needs the `Collector`), 05 (needs `CircuitTransition`)

**Status:** ready-for-agent

- [ ] `circuit.Breaker` gains a `*metrics.Collector` (or equivalent)
      alongside the `*slog.Logger` ticket 05 added, via the same
      constructor
- [ ] `lb_circuit_state` set at the same `CircuitTransition != NoChange`
      call sites the log lines already fire from in
      `ObserveRoundTrip`/`Allow` — one shared signal driving both
- [ ] Every backend's `lb_circuit_state` seeded to `closed` in `main.go`
      immediately after `backend.NewRegistry` succeeds, via the same
      `Collector` method (ticket 01's mutual-exclusion setter) real
      transitions use
- [ ] Test: the full closed→open→half-open→closed (and →open) cycle each
      produces the correct single-series-at-`1` gauge state at every step,
      with the other two states at `0` (mirroring ticket 05's log-line
      cycle test, now asserted against the metric)
- [ ] Test: after `NewRegistry` and before any request is served, every
      backend's `lb_circuit_state` shows `closed=1`, `open=0`,
      `half_open=0`
- [ ] Test: every existing S3.T3/ticket-05 test continues to pass unchanged
- [ ] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments
