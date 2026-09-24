# 05: S4.T3.0 — Reload hook-up APIs (checker add/remove, outlier forget, series deletion, initial-probe admission)

**What to build:** The additive package-level APIs that T3's reload operation
will call, each verifiable on its own: the active checker can start and stop
probing individual backends; a newly added backend is admitted after one
successful probe with an `initial_probe` reinstatement; a removed backend's
probe in flight never produces a transition; the outlier detector can forget a
backend; the collector can delete a backend's healthy and circuit-state series.
Nothing calls these yet. Same prefactor pattern as S3.T12.0. Spec: stories
27–31 (mechanism), 36, 58, decisions D15–D16. Design per ADR-0015.

**Blocked by:** 04.

**Status:** ready-for-agent

- [ ] The active checker gains **add** (start a prober for a backend) and
      **remove** (stop it). Each prober has its own cancel function in a
      mutex-guarded map, off the request path. Existing Start behaviour and
      unchanged backends' probers are unaffected.
- [ ] A prober checks its own context after a probe returns and before
      applying any outcome: a probe in flight at remove never marks health,
      logs, or writes a gauge. Race test (`-race`) with a probe blocked on a
      channel gate: remove, release, assert no transition log and no gauge
      write.
- [ ] A backend started via add as "added" is unhealthy until **one**
      successful probe, then marked healthy with exactly one
      `health_reinstated` line with reason `initial_probe` (not
      `probe_recovered`) and its healthy gauge flipped 0 → 1. A failing first
      probe emits nothing. Added probers probe immediately, not after one
      interval. Recovery after ejection still needs two successes.
- [ ] The probe-round-complete latch is never cleared by add/remove (ADR-0014
      decision 4).
- [ ] The log vocabulary gains the `initial_probe` reason constant; the
      health-reinstated event's documentation is widened to cover first
      admission.
- [ ] The outlier detector gains **forget**, deleting a backend's window.
- [ ] The metrics collector gains deletion of a backend's `lb_backend_healthy`
      and `lb_circuit_state` series (all state labels). No deletion of the
      active-connections series (T4).
- [ ] Tests Red first, one per surface above; `make test`, `make test-race`,
      vet, fmt clean.
