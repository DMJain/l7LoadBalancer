# Chaos smoke tests

Two scripts run failure scenarios against the dummy backends in `deployments/docker/docker-compose.yml`. They check the result through the LB metrics endpoint (`metrics.listen`, default `:9090`).

| Script | Scenario | Pass |
|---|---|---|
| `eviction.sh` | `docker stop backend-a`, then `docker start` | `lb_backend_healthy` goes `1 → 0 → 1`. One `health_ejected` (`probe_failures`) line and one `health_reinstated` (`probe_recovered`) line. |
| `circuit.sh` | Recreate `backend-a` with `FAIL_RATE=1`, send traffic, then recreate it with `FAIL_RATE=0` | `lb_circuit_state` goes `closed → open → closed`. One `circuit_opened` (`consecutive_failures`) line and one `circuit_closed` (`trial_success`) line. |

## Requirements

Docker with `docker compose`, a Go toolchain, `curl`, `awk` and `grep`. Ports `:8080` and `:9090` must be free.

## Run

From the repo root:

```sh
./deployments/docker/chaos/eviction.sh
./deployments/docker/chaos/circuit.sh
docker compose -f deployments/docker/docker-compose.yml down   # stop the backends
```

Each script builds the LB, starts the backends, and starts the LB as a host process with `configs/example.yaml` (backends on ports `9001/9002/9003`). Then it runs the scenario. Its `EXIT` trap stops the LB. Set `LB_METRICS_URL` if you change `metrics.listen`.

## Timing

- **Ejection:** after 3 failed probes (`probe_interval` default 5s).
- **Reinstatement:** after 2 good probes.
- **Circuit open:** after 3 consecutive failed requests.
- **Circuit close:** after the cooldown (`circuit.cooldown`, default 30s), the next request that selects the backend is the half-open trial. Its success closes the circuit.

The circuit has no timer. A trial starts only when a real request selects the backend, so traffic must continue after the restore.

`circuit.sh` recreates the container because `docker exec` cannot change the environment of a running process. It uses the compose override `BACKEND_A_FAIL_RATE`:

```sh
BACKEND_A_FAIL_RATE=1 docker compose -f deployments/docker/docker-compose.yml up -d --force-recreate backend-a
```

To check by hand, read the metrics:

```sh
curl -fsS http://127.0.0.1:9090/metrics | grep -E 'lb_(backend_healthy|circuit_state)\{backend="backend-a"'
```
