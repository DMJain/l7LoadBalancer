# 07: S4.T10 — Soak test

**What to build:** A flag-gated one-hour soak test in the chaos harness through the app seam. Phases: warmup (steady load until goroutine/heap counts stabilize; baseline captured) -> steady-state proxying (20 min) -> client-cancellation phase (10 min, ~5% of clients disconnect mid-response) -> backend-death phase (10 min, one backend killed mid-response and restarted repeatedly) -> reload phase (10 min, SIGHUP every 60s: remove one backend, add another, short drain window) -> quiet (60s) -> assert. Tolerances: goroutines at end <= warmup baseline + 10; heap with runtime.GC() forced at both points, end HeapAlloc <= warmup + 8 MB. No trend check — the endpoint assertions are sufficient and deterministic. make soak runs without -race (tolerances calibrated for non-race); make soak-race keeps the goroutine assertion and drops the heap assertion (race's 5-8x memory overhead makes heap numbers meaningless). A duration flag shortens the run for iteration.

**Blocked by:** 06 (S4.T9 — the audit's assertion is the soak's core)

**Status:** ready-for-agent

- [ ] Flag-gated soak test through the app seam with the six phases
- [ ] Committed tolerances: +10 goroutines, +8 MB post-GC heap
- [ ] Duration flag for iteration
- [ ] make soak target (non-race) and make soak-race (goroutine assertion only)
- [ ] make test, make test-race, go vet, make fmt green
