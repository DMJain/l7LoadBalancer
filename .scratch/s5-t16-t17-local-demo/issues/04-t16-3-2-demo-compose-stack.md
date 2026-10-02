# 04: S5.T16.3.2 — `demo/` compose stack

**What to build:** one command brings up the whole local demo — four LBs (one per algorithm) sharing four admin-enabled backends, eight Zipf-ranked traffic clients, a fast-scraping Prometheus and an embeddable Grafana — with traffic visibly flowing on the dashboard, and one command tears it down. Nothing is reachable off the loopback interface.

Spec: `../spec.md` — *Load balancers*, *Demo stack (S5.T16.3)*; ADR-0023 decisions 2–4, 6, 9.

**Blocked by:** 01 (admin listener), 02 (traffic generator), 03 (dashboard variables).

**Status:** ready-for-agent

- [ ] A compose file in a top-level demo area, independent of the root stack and the bench rig; neither of those is edited.
- [ ] Four LBs from the existing LB image, each with its own config file; the configs list the same four backends and differ only in `algorithm` (`round_robin`, `least_conn`, `consistent_hash_bounded`, `p2c_ewma`) plus anything that must be unique per instance.
- [ ] Four dummy backends with `ADMIN_ENABLED=true`.
- [ ] Eight generator services expanded from one YAML anchor with `RANK=1..8` (not `deploy.replicas`), the four LBs as the target allowlist, a default target and a demo default total rate high enough that every LB keeps in-flight counts above zero, and `GOMEMLIMIT`/`GOGC` set.
- [ ] A demo Prometheus scraping each LB as its own `job` at a 1 s interval.
- [ ] A demo Grafana provisioning the same single dashboard JSON, with `GF_SECURITY_ALLOW_EMBEDDING=true` and anonymous Viewer access.
- [ ] Published ports: Grafana (and, for rehearsal, the LB client ports) only, each bound to `127.0.0.1`; generators, admin listeners, LB metrics and health ports stay internal. (The control service's port arrives in 06.)
- [ ] One-command up and down documented (a short README in the demo area, and a `make` target if it follows the existing Makefile pattern).
- [ ] Verified: `docker compose config` clean; after `up`, every LB is a healthy Prometheus target, every backend is selectable on every LB, and the dashboard at `var-window=15s` with the active LB selected shows request rate and per-backend share moving; `docker compose ps` shows no published port on a non-loopback address.
- [ ] Config-only (no Go): exempt from Red-Green per AGENTS.md.
