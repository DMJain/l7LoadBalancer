# 06: S4.T9 — pprof audit

**What to build:** pprof mounted on the metrics listener's handler — no new listener, no new config; the metrics listener is already always-on and unauthenticated, so no new attack surface (ADR-0005 puts security hardening out of scope). A goroutine-leak audit test through the app seam: a load burst including client cancellations and backend deaths, a quiet period, then an assertion that the goroutine count returns to baseline within a small delta.

**Blocked by:** 02, 03, 04, 05 (S4.T5-T8 — audits what they built)

**Status:** ready-for-agent

- [ ] pprof mounted on the metrics listener
- [ ] Test: pprof endpoints respond on the metrics listener
- [ ] Goroutine-leak audit test: burst with cancellations and backend deaths, quiet period, baseline + delta assertion
- [ ] make test, make test-race, go vet, make fmt green
