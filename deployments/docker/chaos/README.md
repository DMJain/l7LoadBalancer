# Chaos smoke tests (S3.T8 / S3.T9)

Shell-scripted chaos scenarios that close `MILESTONES.md`'s Sprint 3 exit
criteria against the real docker-compose backends, asserting through the load
balancer's own Prometheus exposition endpoint (`metrics.listen`, default
`:9090`) rather than by eyeballing Grafana.

The load balancer runs as a **host process** — a containerized LB is S3.T10,
out of this bundle's scope. The backends come from the existing S1.T9 compose
at `deployments/docker/docker-compose.yml`; there is no new compose file.

| Script | Ticket | Scenario |
|--------|--------|----------|
| `eviction.sh` | S3.T8 | `docker stop` a backend → ejected → `docker start` → reinstated |
| `circuit.sh` | S3.T9 | inject 5xx (`FAIL_RATE=1`) → circuit opens → restore → circuit closes |

`circuit.sh` is added by S3.T9 and shares this README's prereqs and conventions.

## Prereqs

- Docker daemon running, `docker compose` available.
- Go toolchain on the host (the scripts build the LB binary).
- `curl`, `awk`, `grep`.
- Ports `:8080` (LB client traffic) and `:9090` (LB metrics) free. The scripts
  use `configs/example.yaml`, whose backends (`backend-a/b/c`) point at the
  compose ports `9001/9002/9003`.

## Running

From the repo root:

```sh
./deployments/docker/chaos/eviction.sh
```

Each script is idempotent-ish: it builds the LB, brings the backends up,
starts the LB, runs the scenario, then leaves the LB stopped (its `EXIT` trap
kills the process). Tear the backends down with:

```sh
docker compose -f deployments/docker/docker-compose.yml down
```

Override the metrics URL with `LB_METRICS_URL` if `metrics.listen` is changed.

## `eviction.sh` — what success looks like

The script asserts the `lb_backend_healthy` series for `backend-a` at each
step. The manual equivalent, with the LB and backends running:

```sh
# Baseline — backend-a healthy
curl -fsS http://127.0.0.1:9090/metrics | grep 'lb_backend_healthy{backend="backend-a"}'
# → lb_backend_healthy{backend="backend-a"} 1

docker compose -f deployments/docker/docker-compose.yml stop backend-a
# Within 3 probe intervals (probe_interval default 5s, 3 consecutive failures):
curl -fsS http://127.0.0.1:9090/metrics | grep 'lb_backend_healthy{backend="backend-a"}'
# → lb_backend_healthy{backend="backend-a"} 0
# The LB log shows: event=health_ejected reason=probe_failures backend=backend-a

docker compose -f deployments/docker/docker-compose.yml start backend-a
# Within 2 probe intervals (2 consecutive successes):
curl -fsS http://127.0.0.1:9090/metrics | grep 'lb_backend_healthy{backend="backend-a"}'
# → lb_backend_healthy{backend="backend-a"} 1
# The LB log shows: event=health_reinstated reason=probe_recovered backend=backend-a
```

Expected result: the gauge flips `1 → 0` after the stop and back `0 → 1`
after the start, with exactly one `health_ejected` and one `health_reinstated`
transition line each.

## Scope

Scripted and documented here; the end-to-end `docker compose up` run is
recorded as `[MANUAL VERIFICATION PENDING]` in the session log (Docker daemon
availability), matching S3.T7's precedent.
