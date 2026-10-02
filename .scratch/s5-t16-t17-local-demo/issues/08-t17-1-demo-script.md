# 08: S5.T17.1 — Demo script

**What to build:** a written demo script the owner follows to rehearse and record: the scenario sequence, the click path for each step, what to narrate (with the ADR behind each claim), and the expected on-screen state, so every recording tells the same defensible story.

Spec: `../spec.md` — *Demo script (S5.T17)*; ADR-0023 decision 5 and the EWMA consequence.

**Blocked by:** 07 (kill and revive).

**Status:** ready-for-agent

- [ ] A markdown document in the demo area, ordered scenarios; each step has: click path, narration with the backing ADR, expected on-screen state, and how long to wait.
- [ ] A convergence pause after every LB switch, stating why (EWMA cold on first activation, stale thereafter — ADR-0010, ADR-0023).
- [ ] A reset step between scenarios returning every backend to its baseline profile.
- [ ] The scenario list is fixed with the owner at the start of the ticket (open decision 7 in the spec); the spec's example list is the starting proposal.
- [ ] States, and never contradicts, the known selector behaviours: p2c-ewma's rank-based split (≈50 / 33 / 17 / 0 % with four backends) and that a backend restored to fast latency may stay starved because EWMA has no decay — narrated as a known limitation, with the recovery step restarting that LB or showing recovery on another algorithm; least-connections' first-in-order tie-break at low concurrency and why the demo rate keeps in-flight counts above zero; consistent-hash-bounded's per-client stickiness and spill of the hottest key's owner; a flaky backend driving outlier ejection and the circuit breaker while `/health` passes.
- [ ] Every expected on-screen state is observed in one full rehearsal against the stack; any step whose observed state differs is fixed in the script (or recorded as a finding), not left aspirational.
- [ ] Docs-only: exempt from Red-Green per AGENTS.md.
