# Observability stack

Prometheus and Grafana for an LB that runs on the host with `make run`. Prometheus scrapes the LB's `metrics.listen` endpoint through the host gateway every 15 s. Grafana shows the `l7LoadBalancer` dashboard. This stack is separate from the dummy backends and the benchmark rig (ADR-0013 decision 17).

## Files

| Path | Purpose |
|------|---------|
| `docker-compose.yml` | Prometheus and Grafana |
| `prometheus/prometheus.yml` | Scrapes `host.docker.internal:9090`, job `l7loadbalancer` |
| `prometheus/prometheus-stack.yml` | Used by the repo-root `docker-compose.yml`. Scrapes the `l7lb` service at `l7lb:9090` (ADR-0013 decision 18). |
| `grafana/provisioning/` | Prometheus datasource (uid `prometheus`) and dashboard loader |
| `grafana/dashboards/l7loadbalancer.json` | The dashboard. The demo stack mounts it too. |
| `smoke.sh` | Smoke test |

The dashboard has six panels: request rate by backend and status class, latency p50 and p99, circuit state, backend healthy, active connections, and request share. Two variables are settable from the URL (`?var-window=15s&var-lb=...`):

- `$window` (default `5m`): the range for every rate and histogram query. `15s` reacts faster.
- `$lb` (`label_values(job)`): filters every panel to one LB job.

## Ports

| Service | Host port |
|---|---|
| LB client traffic | `:8080` |
| LB metrics | `:9090` |
| Prometheus | `:9091` (container `:9090`), off the LB metrics port |
| Grafana | `:3000`, anonymous Viewer access for local use |

## Start

From the repo root:

```sh
docker compose -f deployments/docker/docker-compose.yml up -d --build   # dummy backends
make run                                                                 # the LB
docker compose -f deployments/docker/observability/docker-compose.yml up -d
```

Open <http://localhost:3000> (Grafana) and <http://localhost:9091> (Prometheus).

The gauge panels show data from the first scrape. At startup the LB seeds `lb_backend_healthy=1`, `lb_circuit_state=closed` and `lb_active_requests=0` for each backend, through the same collector methods that real transitions use (ADR-0013 decision 9). The rate and latency panels stay empty until traffic flows, because `rate()` needs two samples of a series.

## Smoke tests

With the LB running, `./deployments/docker/observability/smoke.sh` checks that the LB metrics answer, the Prometheus target is up, the seeded gauges exist, and the dashboard is provisioned. Then send traffic:

```sh
for i in $(seq 1 100); do curl -s http://127.0.0.1:8080/ >/dev/null; done
```

To test failure, stop a backend:

```sh
docker compose -f deployments/docker/docker-compose.yml stop backend-c
```

- After 3 failed probes (`probe_interval` default 5s), `lb_backend_healthy` for `backend-c` reads `0`. The LB logs `health_ejected` with `reason=probe_failures`.
- Round robin still sends requests to `backend-c`, and they fail. After 3 consecutive failures, the circuit opens. The LB logs `circuit_opened` with `reason=consecutive_failures`.

Run `docker compose -f deployments/docker/docker-compose.yml start backend-c`. After the cooldown (`circuit.cooldown`, default 30s), the next selection is a half-open trial. A good trial and 2 good probes return the backend to healthy and closed.

To inject 500s instead, run `BACKEND_C_FAIL_RATE=1.0 docker compose -f deployments/docker/docker-compose.yml up -d backend-c`. The circuit opens the same way. The active probe uses `/health`, which ignores the failure rate.

To stop the stack, run `docker compose -f deployments/docker/observability/docker-compose.yml down`.

The stack has no alert rules and no TLS or auth on the metrics endpoint (ADR-0005, ADR-0013 decision 17).
