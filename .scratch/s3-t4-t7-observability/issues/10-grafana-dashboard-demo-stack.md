# 10: Grafana Dashboard + Observability Demo Stack

**What to build:** A five-panel Grafana dashboard covering the full
reserved-metric set, plus a new Prometheus + Grafana docker-compose stack
(separate from the existing dummy-backends compose) so the dashboard is
actually loadable and `MILESTONES.md`'s Sprint 3 exit criteria are
demonstrable from a clean checkout, not just declared in a JSON file
nobody can view.

**Blocked by:** 01, 06, 07, 08, 09 (needs the full, real, wired metric set
to build correct panels and demonstrate them against real data)

**Status:** ready-for-agent

- [ ] Dashboard JSON with exactly five panels: request rate (by `backend`,
      `status_class`), latency p50/p99 per backend (`histogram_quantile`
      over `lb_request_duration_seconds`), circuit state per backend (a
      state-timeline or table reading the `lb_circuit_state` label-enum,
      e.g. `lb_circuit_state == 1`), backend healthy per backend, active
      connections per backend — nothing invented beyond what's actually
      instrumented
- [ ] New docker-compose stack under `deployments/docker/observability/`
      adding Prometheus and Grafana services with dashboard/datasource
      provisioning, kept entirely separate from
      `deployments/docker/docker-compose.yml` (S1.T9) and Sprint 5's
      future bench compose
- [ ] Prometheus scrape config targets the load balancer's
      `metrics.listen` port; reasonable default scrape interval (e.g. 15s)
- [ ] No Go unit tests for this ticket (dashboard JSON and compose files
      carry no application logic), matching the S1.T9 docker-compose
      precedent
- [ ] Documented manual/scripted smoke test, recorded in the session log:
      `docker compose up` on the new stack renders a complete dashboard
      (all panels populated, no blank panels) before any client traffic is
      sent, per the startup-seeding work in tickets 07–09
- [ ] Documented manual smoke test reproducing the chaos-test exit
      criterion: stopping or fault-injecting a backend is visible on the
      dashboard (healthy → 0, circuit state moving through open) within
      the configured thresholds
- [ ] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments
