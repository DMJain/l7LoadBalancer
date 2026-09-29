# 18: Narrative number check (S5.T10.3)

**What to build:** The hand-written narrative can't drift from the data. Every latency or throughput figure quoted outside the generated regions must match a generated cell, or the generator fails and names the figure. The check is narrow, so configuration constants and percentages never trigger it. TDD-exempt, with the fixture test extended.

**Blocked by:** 16

**Status:** ready-for-agent

Spec: `../spec.md` (Results generator and document → narrative number check)

- [ ] Outside the markers, every token matching `[≈~]?[0-9][0-9,]*(\.[0-9]+)?\s?(ms|µs|req/s)` must equal a generated cell once the approximate prefix is stripped and commas and spacing are normalised.
- [ ] On a mismatch, the generator exits nonzero and names every unmatched token.
- [ ] Not matched, by construction: percentages, core counts, sizes (for example 1MB), ADR numbers and percentile names (for example p99.9).
- [ ] The fixture document gains narrative that exercises each case. The test script asserts:
  - a matching figure passes;
  - `≈`- and `~`-prefixed matching figures pass;
  - a non-matching figure fails and is named;
  - exempt tokens are ignored.
- [ ] Verified: `shellcheck -S style` and `bash -n` pass, and the extended test passes.
