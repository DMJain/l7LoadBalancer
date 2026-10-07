# 03: S5.T20.2 — Control page: state header, invalid-reload button, controls, `/metrics`

**What to build:** the control page becomes the one recording screen's frame: S1 and S2 header lines, the invalid-reload button, four algorithm buttons, one-click Clear, Stop/Start labels, and an active-LB metric for the dashboard's switch markers.

Spec: `../spec.md` — D1, D6, D9; gap report §2–§4.

**Blocked by:** 02.

**Status:** ready-for-agent

- [ ] ADR-0025 amended first: a new peer set (the four LB metrics/admin listeners on the internal network), a fixed invalid-config body (never caller-supplied), and `/metrics` on the control service.
- [ ] S1 shows the active algorithm name (mapped from the active LB); S2 shows the active LB's `GET /admin/reload/last` as "Last reload: SUCCESS|REJECTED at hh:mm:ss — <error>" or "no reload yet".
- [ ] "Attempt reload with invalid config" posts a fixed `algorithm: "p3c_ewma"` body to the active LB and shows the result.
- [ ] Four algorithm buttons replace the LB dropdown (same `POST /api/lb`); labels never say "reload".
- [ ] Per-backend Clear (`0/0/0`); Kill/Revive labelled Stop/Start (kill semantics unchanged).
- [ ] `GET /metrics` serves `demo_active_lb{lb="…"} 1` for the active LB (stdlib text format); the demo Prometheus scrapes it.
- [ ] Tests, Red-first, against `httptest` stand-ins; `demo/acceptance.sh` still passes.
- [ ] Verification (D9): dry-run rows 3 (header part) and 4.
