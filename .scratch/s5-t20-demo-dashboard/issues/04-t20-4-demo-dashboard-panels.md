# 04: S5.T20.4 — Demo-only dashboard: zone A and B panels

**What to build:** a demo-only Grafana dashboard file with the zone A and B panels, embedded by the control page.

Spec: `../spec.md` — D4, D5, D9; gap report §3.

**Blocked by:** 03.

**Status:** ready-for-agent

- [ ] ADR-0023 decision 9 amended first: a demo-only dashboard file is permitted; the shared JSON is unchanged.
- [ ] C1 total rate stat; C2 success (2xx+3xx) / failed (5xx) / no-backend (5xx with `backend=""`) stats as `increase(...[$__range])` totals with rates; C3 aggregate p50/p99, 2-minute range, titled "as measured at the load balancer".
- [ ] B1 stacked 100 % share summed over all `lb-*` jobs, with an annotation on `demo_active_lb` changes labelled with the algorithm; B2, B3 (p99 bar gauge), B4 ("requests in progress"), B5 (1 green / 0 red), B6; B7 row (requests in progress; EWMA added by S5.T18).
- [ ] The control page embeds the new panels; provisioning mounts the new file in `demo/` only.
- [ ] Verification (D9): dry-run rows 3, 5, 6, 7 and 8.
