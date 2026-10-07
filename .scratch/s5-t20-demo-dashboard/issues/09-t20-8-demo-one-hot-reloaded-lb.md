# 09: S5.T20.8 — Reshape the demo around one hot-reloaded LB

**What to build:** the demo runs one load balancer, and its algorithm is changed the production way: edit its config file, send SIGHUP (or post to `/admin/reload`). The control page loses its algorithm buttons and reads the real algorithm from the LB. The dashboard marks algorithm changes from the LB's own `lb_algorithm_info`. The acceptance check and both scripts follow.

Spec: `../spec.md` addendum (owner, 2026-10-07); S5.T20.7.

**Blocked by:** 08 (S5.T20.7).

**Status:** ready-for-agent

- [ ] ADR-0023 amended (decision 4 superseded: one LB; the config directory is bind-mounted so editor saves are seen) and ADR-0025 amended (algorithm read from the LB's `lb_algorithm_info`; the algorithm buttons and `demo_active_lb` withdrawn).
- [ ] `demo/docker-compose.yml`: one `lb` service on `127.0.0.1:8080` with `./configs` mounted as a directory and `demo/configs/lb.yaml`; generators, Prometheus and the control service point at it; the four per-algorithm configs removed.
- [ ] Control service: `/api/state` reports each LB's current algorithm (parsed from its `/metrics`); `/metrics` and `demo_active_lb` removed; the page has no algorithm buttons and its header shows the LB's real algorithm; Red-first tests.
- [ ] Demo dashboard: queries on `job="lb"`; switch markers from `lb_algorithm_info` appearing; circuit panel without the active-LB join.
- [ ] `demo/acceptance.sh`: phase 2 is an algorithm hot reload through `/admin/reload` (no restart), asserts the reload succeeded, `lb_algorithm_info` moved, backend3's share fell, and zero client errors across it; the trap restores the file's config.
- [ ] `demo/README.md`, `demo/DEMO-SCRIPT.md` and `docs/demo/video-script.md` rewritten around the hot reload; `docs/demo/dashboard-gap-report.md` §8–§9 marked superseded by the video script.
- [ ] Verification: fresh `make demo-up`, acceptance passes, and the scripted on-camera hot reload (edit file, SIGHUP) works with zero client errors.
- [ ] Gates clean.
