# 09: Circuit-State Gauge

**What to build:** `lb_circuit_state` reflects each backend's current
circuit state, driven by the exact same `CircuitTransition` signal ticket
05 already introduced for the circuit transition log lines — not a second,
independently written detection path. Inherits ticket 05's documented,
permanent gap around `Selectable()`-scan-won promotions (this gauge won't
update in that case either, for the same reason the log line doesn't).

**Blocked by:** 01 (needs the `Collector`), 05 (needs `CircuitTransition`)

**Status:** done

- [x] `circuit.Breaker` gains a `*metrics.Collector` (or equivalent)
      alongside the `*slog.Logger` ticket 05 added, via the same
      constructor
- [x] `lb_circuit_state` set at the same `CircuitTransition != NoChange`
      call sites the log lines already fire from in
      `ObserveRoundTrip`/`Allow` — one shared signal driving both
- [x] Every backend's `lb_circuit_state` seeded to `closed` in `main.go`
      immediately after `backend.NewRegistry` succeeds, via the same
      `Collector` method (ticket 01's mutual-exclusion setter) real
      transitions use
- [x] Test: the full closed→open→half-open→closed (and →open) cycle each
      produces the correct single-series-at-`1` gauge state at every step,
      with the other two states at `0` (mirroring ticket 05's log-line
      cycle test, now asserted against the metric)
- [x] Test: after `NewRegistry` and before any request is served, every
      backend's `lb_circuit_state` shows `closed=1`, `open=0`,
      `half_open=0`
- [x] Test: every existing S3.T3/ticket-05 test continues to pass unchanged
- [x] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments

Completed 2026-09-21 by opencode (S3.T6.4). `circuit.Breaker`/`New` gained a
`*metrics.Collector` alongside the `*slog.Logger` ticket 05 added
(`circuit.New(cooldown, log, collector)`), so both observability surfaces are
wired at the same construction site (spec story 35).

The gauge is written at the exact same `CircuitTransition != NoChange` call
sites the ticket-05 log lines fire from: `CircuitOpened`/`CircuitReopened` set
`open`, `CircuitHalfOpened` sets `half_open`, and `CircuitClosed` sets `closed`
— one shared transition read per site, so the log line and the gauge can never
disagree (spec story 34). A `NoChange` outcome writes nothing, so a sustained
failure streak while already open does not re-write.

The documented, permanent gap is preserved and now pinned by a test: a
Half-Open promotion whose CAS is won by a `Registry.Selectable()` scan (via
`Breaker.Open` → `Backend.CircuitOpen`) has no collector path, so the gauge
stays at `open` — the same reason the log line does not fire (ADR-0013
decision 13). `main.go`'s `seedMetrics` seeds every backend to `closed` via
`Collector.SetCircuitState`, the exact mutual-exclusion setter real transitions
use (spec story 37); Prometheus `Vec` metrics materialize no series until first
written, so the seed is what makes the circuit panel non-blank on the first
scrape (ADR-0013 decision 9).

Existing S3.T3/ticket-05 tests pass unchanged in outcome; only their
`circuit.New` calls were updated for the new arity (`metrics.NewCollector()`),
matching the precedent tickets 05/08 set. No new ADR (ADR-0013 decisions 5/15
record the design). No metrics involvement beyond the one gauge this ticket
owns.
