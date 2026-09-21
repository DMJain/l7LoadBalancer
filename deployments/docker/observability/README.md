# Observability demo stack (S3.T7)

Prometheus + Grafana for the load balancer's Sprint 3 observability. Prometheus
scrapes the load balancer's always-on `metrics.listen` endpoint; Grafana renders
the committed five-panel dashboard. The stack is intentionally separate from
`deployments/docker/docker-compose.yml` (S1.T9, dummy backends only) and from
Sprint 5's future bench compose — see ADR-0013 decision 17.

The load balancer is **not** a service here: it runs locally via `make run`,
matching S1.T9's convention. Prometheus reaches its metrics endpoint through the
host gateway.

## Contents

| Path | Purpose |
|------|---------|
| `docker-compose.yml` | Prometheus + Grafana services |
| `prometheus/prometheus.yml` | Scrape config → `host.docker.internal:9090`, 15s interval |
| `grafana/provisioning/datasources/prometheus.yml` | Prometheus datasource (uid `prometheus`) |
| `grafana/provisioning/dashboards/dashboards.yml` | Loads the dashboards directory |
| `grafana/dashboards/l7loadbalancer.json` | The five-panel dashboard |
| `smoke.sh` | Scripted smoke test (see below) |

## Ports

| Service | Host | Container | Notes |
|---------|------|-----------|-------|
| Load balancer | `:8080` | — | client traffic (`configs/example.yaml`) |
| Load balancer metrics | `:9090` | — | Prometheus scrape target |
| Prometheus | `:9091` | `:9090` | `:9091` avoids colliding with the LB metrics port |
| Grafana | `:3000` | `:3000` | anonymous read-only viewing enabled (local demo) |

## Bring it up

From the repo root, in three terminals (or background the first two):

```sh
# 1. Fleet of dummy backends (S1.T9)
docker compose -f deployments/docker/docker-compose.yml up -d --build

# 2. The load balancer
make run

# 3. This observability stack
docker compose -f deployments/docker/observability/docker-compose.yml up -d
```

Then open <http://localhost:3000> — the `l7LoadBalancer` dashboard is already
provisioned (anonymous, Viewer role) — and <http://localhost:9091> for
Prometheus.

Every **gauge** panel — Circuit state, Backend healthy, Active connections —
is populated from the very first scrape, **before any client request is sent**,
because `main.go`'s startup seeding materializes each backend's gauge series
(`lb_backend_healthy=1`, `lb_circuit_state=closed`, `lb_active_connections=0`)
through the same collector methods real transitions use (ADR-0013 decision 9).
The two **traffic-derived** panels — Request rate and Latency — are
necessarily empty until requests flow (a counter/histogram `Vec` has no series
until first observed, and `rate()` needs two samples); send traffic in the next
section to fill them.

## Smoke test 1 — gauge panels populated on first scrape

`smoke.sh` automates the checks below and prints the URLs:

```sh
./deployments/docker/observability/smoke.sh
```

The manual equivalent:

```sh
# The LB must be running (make run) before this.
curl -s http://127.0.0.1:9090/metrics | grep -E '^lb_(backend_healthy|circuit_state|active_connections)'
# → every backend present: healthy=1, closed=1 (open=0, half_open=0), active=0
curl -s http://127.0.0.1:9091/api/v1/targets | grep -o '"health":"up"' | head -1
# → "health":"up" for the l7loadbalancer target
```

Now drive some traffic and confirm the request panels move:

```sh
for i in $(seq 1 100); do curl -s http://127.0.0.1:8080/ >/dev/null; done
```

Request rate and latency panels should show data for all three backends within
a scrape interval.

## Smoke test 2 — chaos-test exit criterion

`MILESTONES.md` Sprint 3: *"`docker stop` a backend → ejected within health
threshold, circuit trips, recovers on restart."*

```sh
docker compose -f deployments/docker/docker-compose.yml stop backend-c
```

Within one probe interval plus the failure threshold (`probe_interval` default
5s, 3 consecutive failures), on the dashboard:

- **Backend healthy** — `backend-c` flips `UP` → `DOWN` (`lb_backend_healthy`
  → `0`).
- A WARN `health_ejected` / `reason=probe_failures` line appears in the LB's
  log output.
- **Circuit state** — with round-robin continuing to route to the stopped
  backend, its round trips fail; after 3 consecutive failures
  (`circuitFailuresBeforeOpen`) `backend-c` moves `closed` → `open`, and a WARN
  `circuit_opened` / `reason=consecutive_failures` line is logged. Its
  `lb_circuit_state{state="open"}` series reads `1`.

Bring it back and watch recovery:

```sh
docker compose -f deployments/docker/docker-compose.yml start backend-c
```

After the cooldown (`circuit.cooldown`, default 30s) the circuit half-opens on
the next selection; a successful trial (and two consecutive successful probes)
returns `backend-c` to healthy/closed.

To reproduce the *injecting 500s* variant instead of stopping the backend, set
its failure rate to `1.0` and restart it:

```sh
BACKEND_C_FAIL_RATE=1.0 docker compose -f deployments/docker/docker-compose.yml up -d backend-c
```

The circuit opens on the 5xx signal the same way; the backend stays
"healthy" to the active probe only until its probe also fails.

## Tear down

```sh
docker compose -f deployments/docker/observability/docker-compose.yml down
```

## Scope

Dashboard panels only — no alerting rules, no TLS/auth on the metrics endpoint
(both out of scope per ADR-0005 / ADR-0013 decision 17).
