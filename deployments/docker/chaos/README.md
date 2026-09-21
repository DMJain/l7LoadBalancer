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

Both scripts share this README's prereqs and conventions.

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

## `circuit.sh` — what success looks like

The script asserts the `lb_circuit_state` series for `backend-a` at each step.
Failure injection recreates `backend-a` with `FAIL_RATE=1` (a `docker exec`
cannot change a running process's environment, so the service is recreated via
the compose file's per-backend `${BACKEND_A_FAIL_RATE:-…}` override). The
manual equivalent, with the LB and backends running:

```sh
# Baseline — backend-a circuit closed
curl -fsS http://127.0.0.1:9090/metrics | grep 'lb_circuit_state{backend="backend-a",state="closed"}'
# → lb_circuit_state{backend="backend-a",state="closed"} 1

# Inject 5xx: recreate backend-a with FAIL_RATE=1, then send traffic.
BACKEND_A_FAIL_RATE=1 docker compose -f deployments/docker/docker-compose.yml up -d --force-recreate backend-a
for _ in $(seq 1 30); do curl -sS -o /dev/null http://127.0.0.1:8080/; done
# After the consecutive-failure threshold (3) the circuit opens:
curl -fsS http://127.0.0.1:9090/metrics | grep 'lb_circuit_state{backend="backend-a",state="open"}'
# → lb_circuit_state{backend="backend-a",state="open"} 1
# The LB log shows: event=circuit_opened reason=consecutive_failures backend=backend-a

# Restore: recreate backend-a with FAIL_RATE=0, then keep sending traffic.
# After the cooldown (circuit.cooldown, default 30s) the first request that
# selects backend-a is admitted as the half-open trial; its success closes it.
BACKEND_A_FAIL_RATE=0 docker compose -f deployments/docker/docker-compose.yml up -d --force-recreate backend-a
# → lb_circuit_state{backend="backend-a",state="closed"} 1
# The LB log shows: event=circuit_closed reason=trial_success backend=backend-a
```

Expected result: the gauge flips `closed → open` after the 5xx injection and
back `open → closed` after the restore, with exactly one `circuit_opened`
(`consecutive_failures`) and one `circuit_closed` (`trial_success`) line.

Traffic must keep flowing after the restore: a half-open trial fires only when
a real request selects the recovered backend (round-robin round-robins onto it),
not on a timer — the same lazy, timer-free property `circuit.sh` relies on.

## Scope

Scripted and documented here; the end-to-end `docker compose up` run is
recorded as `[MANUAL VERIFICATION PENDING]` in the session log (Docker daemon
availability), matching S3.T7's precedent.
