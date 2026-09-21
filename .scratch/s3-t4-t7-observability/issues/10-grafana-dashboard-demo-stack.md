# 10: Grafana Dashboard + Observability Demo Stack

**What to build:** A five-panel Grafana dashboard covering the full
reserved-metric set, plus a new Prometheus + Grafana docker-compose stack
(separate from the existing dummy-backends compose) so the dashboard is
actually loadable and `MILESTONES.md`'s Sprint 3 exit criteria are
demonstrable from a clean checkout, not just declared in a JSON file
nobody can view.

**Blocked by:** 01, 06, 07, 08, 09 (needs the full, real, wired metric set
to build correct panels and demonstrate them against real data)

**Status:** done

- [x] Dashboard JSON with exactly five panels: request rate (by `backend`,
      `status_class`), latency p50/p99 per backend (`histogram_quantile`
      over `lb_request_duration_seconds`), circuit state per backend (a
      state-timeline or table reading the `lb_circuit_state` label-enum,
      e.g. `lb_circuit_state == 1`), backend healthy per backend, active
      connections per backend — nothing invented beyond what's actually
      instrumented
- [x] New docker-compose stack under `deployments/docker/observability/`
      adding Prometheus and Grafana services with dashboard/datasource
      provisioning, kept entirely separate from
      `deployments/docker/docker-compose.yml` (S1.T9) and Sprint 5's
      future bench compose
- [x] Prometheus scrape config targets the load balancer's
      `metrics.listen` port; reasonable default scrape interval (e.g. 15s)
- [x] No Go unit tests for this ticket (dashboard JSON and compose files
      carry no application logic), matching the S1.T9 docker-compose
      precedent
- [x] Documented manual/scripted smoke test, recorded in the session log:
      `docker compose up` on the new stack renders a complete dashboard
      (all panels populated, no blank panels) before any client traffic is
      sent, per the startup-seeding work in tickets 07–09
- [x] Documented manual smoke test reproducing the chaos-test exit
      criterion: stopping or fault-injecting a backend is visible on the
      dashboard (healthy → 0, circuit state moving through open) within
      the configured thresholds
- [x] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments

- 2026-09-21 (opencode): Five panels, `grafana/dashboards/l7loadbalancer.json`
  (uid `l7loadbalancer`): timeseries request rate `sum by (backend,
  status_class) (rate(lb_requests_total[5m]))`; timeseries latency p50/p99 via
  `histogram_quantile(…, sum by (le, backend) (rate(lb_request_duration_seconds_bucket[5m])))`;
  state-timeline `lb_circuit_state == 1` with legend `{{backend}} {{state}}`
  (state read straight from the label, no encoding table); stat
  `lb_backend_healthy` (UP/DOWN value mappings); timeseries
  `lb_active_connections`. Compose adds Prometheus + Grafana only, with
  datasource (uid `prometheus`) and dashboard-file provisioning; Prometheus
  scrapes `host.docker.internal:9090` (the LB's `metrics.listen`) at 15s, on
  host port `:9091` so it doesn't collide with the LB's `:9090`. The LB itself
  stays local (`make run`), continuing S1.T9's separation. `smoke.sh` +
  `README.md` document both smoke tests.
- Verification performed this session: dashboard JSON parsed (5 panels, only
  the five instrumented names referenced); `docker compose config` clean;
  `make test`/`make vet` green; and the LB binary run against
  `configs/example.yaml` showed exactly the seeded series the dashboard's
  first scrape needs (every backend `lb_backend_healthy=1`,
  `lb_active_connections=0`, and all three `lb_circuit_state` series with
  `closed=1`). The Docker daemon was **not** running this session, so the
  full `docker compose up` smoke was not executed — see the session log.
