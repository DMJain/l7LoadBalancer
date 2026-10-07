# ADR-0014: Health endpoint contract and probe semantics

**Status:** Accepted

## Context

A container runtime or orchestrator needs to ask the process three questions: is it alive, is it ready for traffic, and has it finished starting. The proxy listener cannot answer them, because it proxies every path or returns 503.

The process already has the signals. The active checker (`health.Checker`, ADR-0011) knows when the first full probe sweep is done. `backend.Registry.Selectable()` knows if any backend is eligible now (healthy and circuit not open). `config.Validate` runs before the process serves.

## Decision

1. **A separate listener.** A third `http.Server` binds `health_endpoint.listen` (default `:8081`, `DefaultHealthEndpointListen`). It starts and stops with the client and metrics servers. Probe requests never enter the proxy path, so they never touch `lb_requests_total` or the latency histogram.
2. **Three paths with Kubernetes names:** `/livez`, `/readyz`, `/startupz`.
3. **`/livez` always returns 200** with `{"status":"alive"}`. A deadlocked handler does not answer, so the probe timeout is the failure signal. There is no watchdog.
4. **`/startupz` is a one-shot gate.** It returns 503 until both `config_loaded` and `initial_probe_complete` are true. Then it returns 200 for the life of the process, also after a full-fleet eviction. `config_loaded` is fixed at construction. `initial_probe_complete` is `Checker.ProbeRoundComplete()`, which latches. A reload never clears the latch.
5. **`/readyz` adds a live check.** It returns 200 only when `config_loaded`, `initial_probe_complete` and `len(Registry.Selectable()) >= 1` are all true at query time. Otherwise it returns 503. This is the same condition that gives clients a 503 through `ErrNoHealthyBackends`.
6. **An empty selectable set makes `/readyz` return 503.** An upstream load balancer or a Kubernetes Service can then remove the instance from rotation during a full outage. `/startupz` does not share this gate: startup is a one-way change, readiness is continuous.
7. **A fixed JSON envelope with each check:**

   ```json
   {
     "status": "ready",
     "checks": {
       "config_loaded": true,
       "initial_probe_complete": true,
       "selectable_backends": 3
     }
   }
   ```

   On 503 the status is `"not_ready"` and the failing checks stay visible (for example `"selectable_backends": 0`). `/startupz` uses the same envelope without `selectable_backends`. An operator can see which condition failed without reading logs.
8. **`lb_health_probe_total{endpoint, status}`** counts probe responses through `Collector.RecordProbe(endpoint, statusCode)`. `endpoint` is the path. `status` is the status class (`2xx`, `5xx`), not the raw code. Cardinality is about 3 × 2.
9. **Config block `health_endpoint: { listen }`.** It uses the nil-means-omitted pointer pattern. `Validate` rejects an explicit value that is empty or not `host:port`. It is always on, with no disable switch.
10. **`Checker.ProbeRoundComplete() bool`** is true after every configured backend has answered at least one active probe. An `atomic.Int32` counts the backends that answered. When it reaches the backend count, an `atomic.Bool` is set once and stays true.

### Orchestrator mapping

| Orchestrator | Mechanism | Path |
|---|---|---|
| Docker | `HEALTHCHECK` with the `probe` subcommand | `/livez` |
| Fly.io | HTTP check | `/readyz` |
| Kubernetes | `livenessProbe` | `/livez` |
| Kubernetes | `readinessProbe` | `/readyz` |
| Kubernetes | `startupProbe` | `/startupz` |

## Consequences

- The process answers native liveness, readiness and startup probes, and the request path and its metrics do not change.
- The live check in `/readyz` lets an upstream route around an instance whose whole fleet is down.
- The endpoint adds no new detection logic. It only shows signals the health subsystem already has.
- A third listener is a third port to expose and a third server to start and stop.
- `config_loaded` is always true in production, because the process exits before serving if config fails. It stays in the envelope so the response is clear to an operator.
- Rejected alternatives:
  - Probe paths on the proxy listener (mixes probe and client traffic).
  - A single `/healthz` (loses the three meanings).
  - A `/readyz` that checks only startup (a started instance with no backends must report not ready).
