# 06: S5.T20.5 — Benchmark summary page

**What to build:** one static HTML file readable on camera in 30 seconds.

Spec: `../spec.md` — D3; gap report §5.

**Blocked by:** 05.

**Status:** ready-for-agent

- [ ] Title exactly as D3. One table: four algorithm rows (round-robin, least-connections, consistent-hash, p2c-ewma), columns peak req/s, p50, p99, p99.9 (ms), from `RESULTS.md`'s core table at 10 KiB, load balancer. No Nginx row. Best cell per column highlighted. Text at least 32 px. Nothing else on the page.
- [ ] Every number matches a generated cell in `RESULTS.md` (checked by a script, not by eye). A missing row reads "—" until S5.T20.6 lands.
- [ ] Verification (D9): dry-run row 9 (with the "—" allowed until S5.T20.6).
