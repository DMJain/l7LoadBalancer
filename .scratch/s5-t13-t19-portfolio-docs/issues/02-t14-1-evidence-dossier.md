# 02: S5.T14.1 — Evidence dossier for the design document

**What to build:** a short evidence note the owner can approve or strike item by item, holding every fact the design-decisions document needs that has not yet been verified. No prose for the document is written until the owner has answered it.

Spec: `../spec.md` — *Approved headline claims*, *Design-decisions document*, *Further Notes*.

**Blocked by:** 01 (S5.D2).

**Status:** ready-for-agent (ends at an owner review)

## In scope / out of scope (AGENTS.md Step 2.5)

- **In scope:** verifying the unverified facts below against the code, tests, ADRs and benchmark outputs, and writing one dossier note in this bundle's directory.
- **Out of scope:** any prose for the design document; new measurements or benchmark runs; editing ADRs, tests or code; any claim beyond C1–C7 and the candidates below.

## Acceptance criteria

- [ ] **Concurrency primitives:** for each primitive the document will claim (atomic counters and pointers, compare-and-swap state, mutex, channel, exactly-once release, context-based join, immutable snapshot swap), the dossier gives the use site as file and line, found by searching the code. A primitive with no use site found is listed as "not found — do not claim". Each entry also gives the one-sentence reason the project chose it, taken from the ADR or the frozen concurrency ownership table.
- [ ] **C4 ejection mechanism:** reads ADR-0011 and the failure-scenario outputs, and states which mechanism ejected the stopped backend (passive outlier ejection, active probe, or circuit breaker), with the evidence. If the evidence does not settle it, the dossier says so and recommends citing only ADR-0018 and the observed figures.
- [ ] **C4 peak:** states whether the 3500 req/s peak behind the 1750 req/s rate appears in any retained output; if not, records that it is inferred from the rate being 50% of the peak.
- [ ] **N1 (bounded versus unbounded hot-key figures):** the figures with their test and ADR-0009 source, and a resolution of the 3,996 versus 4,005 discrepancy with the evidence for the resolution.
- [ ] **N2 (P2C latency-bias figures):** the figures with their test and ADR-0010 source and the test conditions (backend counts, latencies), so a worked example can use them exactly.
- [ ] **N3 (soak-test result):** the figures with their retained artefacts and the conditions of the run; if the artefacts are missing, says so.
- [ ] **Worked-example inputs:** the exact numbers the three worked examples will use (a bounded-loads capacity calculation per ADR-0009, a P2C choice with one slow backend per ADR-0010, a drain with a named window per ADR-0016), each checked against the formula or test it comes from.
- [ ] **Definitions inventory:** a list of the technical terms the document will use, in order of first use, so the later tickets define each at first use.
- [ ] Each item is marked with the claim it supports and carries a one-line recommendation (use, use with this caveat, drop).
- [ ] The dossier is shown to the owner. The ticket is not marked done until the owner has approved, edited or struck each item; the answers are recorded in the dossier.
- [ ] Docs-only (TDD-exempt). The claim commit and any commit are shown to the owner for approval first.
