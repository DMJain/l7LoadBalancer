# 05: S5.T16.5 — Latency-isolation acceptance check

**What to build:** a scripted check that proves the demo rig is sound before any UI is built on it: latency injected on one backend stays on that backend, and p2c-ewma moves traffic off it. It is the guard against the `degraded`-slice failure in `RESULTS.md`.

Spec: `../spec.md` — *Acceptance check (S5.T16.5)*; ADR-0023 decision 10.

**Blocked by:** 04 (demo compose stack).

**Status:** ready-for-agent

- [ ] A shell script in the demo area, in the style of the observability smoke script and the chaos scripts: `set -euo pipefail`, `fail()` with explicit messages, non-zero exit on failure.
- [ ] Drives the generators' control endpoints and the backends' admin listeners directly (internal network, e.g. `docker compose exec`); reads Prometheus's HTTP API. It does not depend on the control service.
- [ ] Preconditions asserted first: stack up; all four backends selectable on the round-robin and p2c-ewma LBs; every backend at `sleep_ms=0, jitter_ms=0, fail_rate=0`; generators at a fixed check rate.
- [ ] Phase 1, round-robin LB active: set backend3 to 200 ms (jitter 0); after 30 s, from the LB's per-backend request-duration histogram over a short window: backend3 p50 ≥ 150 ms; backends 1, 2, 4 p50 ≤ 20 ms.
- [ ] Phase 2, p2c-ewma LB active, backend3 still at 200 ms: after a fixed convergence period, backend3's request share < 15%.
- [ ] Every failure names the assertion, the LB and the measured values.
- [ ] A trap restores every backend's profile and the generators' rate and target on exit, pass or fail.
- [ ] One deliberate negative run (latency injected on two backends) confirms phase 1 fails with a useful message; it is recorded in the PROGRESS entry, as S5.T6 recorded its guard negative check.
- [ ] `shellcheck -S style` and `bash -n` clean; the check passes twice in a row on a fresh `up`.
