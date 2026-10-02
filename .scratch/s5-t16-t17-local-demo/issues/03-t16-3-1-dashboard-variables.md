# 03: S5.T16.3.1 — Dashboard `$window`, LB variable, request-share panel

**What to build:** the single Grafana dashboard the project ships becomes usable for a live demo — its rate window and LB can be chosen by URL, and it shows each backend's share of requests — while the repo-root stack renders exactly as it does today.

Spec: `../spec.md` — *Demo stack (S5.T16.3)*, dashboard changes; ADR-0023 decisions 4 and 9.

**Blocked by:** None (can start immediately). S5.D1 is done.

**Status:** done

- [x] A `$window` interval variable, default `5m`, replaces every hard-coded `[5m]` range in the dashboard JSON.
- [x] An LB variable populated from the Prometheus `job` label filters every panel; in the root stack it resolves to the single LB job, so every panel shows what it shows today.
- [x] A per-backend request-share panel: each backend's request rate over `$window` divided by the total, for the selected LB.
- [x] Still one dashboard JSON; no demo-specific copy. No provisioning, Prometheus or Grafana environment change in the root stack.
- [x] Verified against the root stack: the observability smoke script passes; with traffic driven as its README describes, every pre-existing panel shows data with the defaults; a URL with `var-window=15s` renders the same panels with the shorter window.
- [x] The observability README notes the two variables and their defaults.
- [x] Config-only (no Go): exempt from Red-Green per AGENTS.md; `make test` still green.
