# 05: S5.T18 — `lb_p2c_ewma_seconds{backend}` gauge

**What to build:** expose each backend's P2C-EWMA latency estimate so B7 shows what the selector sees.

Spec: `../spec.md`; `PROGRESS.md` proposal (2026-10-02); gap report §3, B7.

**Blocked by:** 04.

**Status:** ready-for-agent

- [ ] ADR-0013 amended first (new gauge, its update point, its series lifecycle on reload).
- [ ] Gauge updated from the latency-recording path; seeded and deleted with the other per-backend series.
- [ ] Tests, Red-first; `make test-race` clean.
- [ ] B7 in the demo dashboard shows it under p2c-ewma.
- [ ] Verification (D9): dry-run row 6 (B7 part).
