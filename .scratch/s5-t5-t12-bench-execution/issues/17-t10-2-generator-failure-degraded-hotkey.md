# 17: Results generator — failure, degraded, hot-key sections (S5.T10.2)

**What to build:** The generator covers the rest of the matrix: reload and backend-kill verdicts, degraded-slice distributions, and consistent-hash hot-key distributions. After this, every published number and verdict comes from raw output. TDD-exempt, with the fixture test extended.

**Blocked by:** 15 (the final failure/degraded/hot-key result formats), 16 (the generator)

**Status:** ready-for-agent

Spec: `../spec.md` (Results generator and document)

- [ ] **Failure section**:
  - backend-kill measurements;
  - no-op reload and drain reload measurements with their `verdict` and any failed criterion;
  - the drain reload's three arrival counter snapshots.
- [ ] **Degraded section**: one row per algorithm × competitor, with p50, p99 and the share for each backend.
- [ ] **Hot-key section**: for every consistent-hash result (core and degraded), the owner, the per-backend shares and the spill flag.
- [ ] Each section has its own marker region. Existing regions and narrative are untouched.
- [ ] The fixture gains representative failure, degraded and hot-key result files, including one FAIL verdict. The test script asserts the new sections, still gets no diff on a second run, and checks that the FAIL verdict is shown with its criterion.
- [ ] Verified: `shellcheck -S style` and `bash -n` pass, and the extended test passes.
