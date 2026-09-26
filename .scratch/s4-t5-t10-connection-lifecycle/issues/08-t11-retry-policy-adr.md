# 08: S4.T11 — Retry-policy ADR

**What to build:** A docs-only ADR recording "no retry, ever": a failed round trip is classified (per S4.T5/T6), logged, and surfaced as a 502 or 499; retrying would double-count active connections (two slots for one client request) and corrupt the outlier window (one logical failure recorded as two). Written after T5/T6 land so the ADR cites the code that forces the decision. AGENTS.md's ADR index gains its row. Docs-only, TDD-exempt.

**Blocked by:** 07 (S4.T10 — last, per the bundle map)

**Status:** ready-for-agent

- [ ] ADR written: no retry, ever, with the observer/active-connection justification
- [ ] ADR index updated
