# 06: Cross-Selector Regression Tests

**What to build:** A cross-cutting test proving `RoundRobin` and `LeastConnections` are truly interchangeable with respect to health transitions — so health-awareness is proven to be a property of the `Selector` contract, not something one implementation happens to get right and the other doesn't.

**Blocked by:** 02, 03

**Status:** ready-for-agent

- [ ] Compile-time assertions for both: `var _ Selector = (*RoundRobin)(nil)`, `var _ Selector = (*LeastConnections)(nil)`
- [ ] Scripted test (run against both selectors): backend goes unhealthy mid-run via `SetHealthy(false)` → immediately stops being chosen
- [ ] Same scripted test: backend recovers via `SetHealthy(true)` → resumes being chosen
- [ ] `go test -cover ./internal/balancer/...` output recorded in the Sprint 1 session log (no enforced threshold yet)
