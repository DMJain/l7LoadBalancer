# 20: `RESULTS.md` narrative (S5.T10.4)

**What to build:** The published results document: generated tables and methodology from the real run, plus a hand-written narrative that explains what the numbers show and where the comparison is approximate. Every figure the narrative quotes traces to a generated cell. The document is self-contained so the later design-decisions doc can link to it rather than repeat it. Docs only.

**Blocked by:** 17 (all sections generated), 18 (number check), 19 (the real results)

**Status:** ready-for-agent

Spec: `../spec.md` (Results generator and document; User Stories 61–73)

- [ ] The generator is run against the committed real results and fills every generated region.
- [ ] The narrative, outside the markers, covers:
  - per-algorithm analysis against Nginx;
  - each **nearest-equivalent** gap (no bounded loads; no latency signal);
  - the **hot key** paragraph: all requests hash to one backend, Nginx pins 100% to it, and the LB spills once in-flight load exceeds (1+ε)×mean;
  - the degraded-slice outcome against the stated expectation;
  - the reload verdicts;
  - the disclosure that cpusets inside Docker Desktop's VM still share physical cores through a hypervisor, so the throughput ceiling is soft.
- [ ] The generator exits 0: every quoted figure matches a cell and the provenance record is clean. A second run produces no diff.
- [ ] Numbers are never restated in a form the check can't see: any latency or throughput is quoted with its unit.
- [ ] The document states that it was produced from the published run's commit, and names that commit.
- [ ] No changes to results, harness or code.
