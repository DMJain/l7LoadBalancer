# ADR-0015: Zero-downtime reload architecture: in-process snapshot swap, backend identity, and admission

**Status:** Accepted

## Context

An operator edits the backend list or the algorithm and triggers a reload. Routing must move to the new configuration with no dropped request. Unchanged backends must keep what the load balancer learned about them. Added backends must take traffic only when they are proven. Removed backends must finish their in-flight work (ADR-0016).

At startup the backend set is fixed in five places: the registry slice, the health checker's goroutine per backend, the consistent-hash ring, the outlier detector's windows, and the per-backend metric series. All wiring is in `internal/app` behind `Build`/`Run`, so a reload has one place to attach to.

## Decision

### In process, not a process handoff

1. A reload changes the running process. It does not start a second process and hand off the socket (SO_REUSEPORT). The listener never closes, so no connection drops at the accept boundary. Unchanged backends keep their instance and all runtime state: health, circuit, active requests and EWMA latency. A new process cannot keep this state without sending it across a process boundary. `main` handles SIGHUP apart from the SIGINT/SIGTERM shutdown context, so a reload never races shutdown. SO_REUSEPORT handoff is a possible future extension for binary upgrades.

### Backend identity and the diff

2. **Backend identity is the pair `(name, URL)`.** A name kept with a new URL is one removed backend plus one added backend. State learned about one host must never apply to a different host. `config.Validate` rejects duplicate names. Order is not part of identity: the same identities in a different order diff to all unchanged.
3. **The diff is a pure function over two validated configs.** `config.DiffBackends` returns added and unchanged backends in the new file's order, and removed backends in the old file's order. The new order becomes the order of `Registry.All`/`Selectable`, so the file sets the round-robin rotation and the `LeastConnections` tie-break. `config.NonBackendChanges` returns the names of other sections that differ. It compares resolved values, so an omitted field equals its explicit default. Neither function logs or touches runtime state.
4. **The backend list and `algorithm` are reloadable. Any other difference rejects the whole reload.** `NonBackendChanges` checks, in order: `listen`, `tls`, `h2c`, `health`, `circuit`, `metrics`, `health_endpoint`, `reload`, `server`, `transport`. Their consumers hold derived state, for example a probe interval in a running ticker or an armed drain timer. A partial apply is more dangerous than a clear refusal. The old config keeps serving, and the log names the changed fields.

### The snapshot

5. **The registry holds one atomic pointer to an immutable snapshot:** a version number that only increases, and the ordered backend slice. `All`, `Selectable` and `Allow` read the current snapshot without locks. The version and the backends come from one atomic load, so they always match.
6. **`apply` is the only writer.** It walks the new backends in order. It reuses the existing instance for an unchanged identity and makes a new one for an added identity. A removed identity that is added again gets a new instance with no shared state.
7. **Removed backends are marked removed before the swap.** `Backend` has an unexported atomic flag, read through `IsRemoved()`. Because the mark comes first, a request selected just before the swap sees the flag when it completes.
8. **A removed backend reports nothing to observers.** The proxy skips the whole observer fan-out when the backend is removed: normal completions, transport failures and client cancellations. No circuit, health, outlier or metric write can come from a backend that left the fleet. A new backend with the same name therefore shows only its own traffic. The active-request count is not observer-driven and still decrements, so a removed backend can drain.

### Selection

9. **The consistent-hash ring rebuilds on demand, keyed on the snapshot version.** `ConsistentHashBoundedLoads` caches `(version, ring)` behind an atomic pointer. When `Select` sees a new version, it builds a ring from that snapshot and stores it with CAS. Two concurrent builds after a swap are both correct, and one loses the CAS. The reload path does not need to know that a ring exists. Load stays live `ActiveConns()` (ADR-0009), and the ring holds only placement (ADR-0008), so a rebuild loses nothing. Round-robin's index may start mid-cycle after a reload, as after a health change.

### Admission and blue/green

10. **A backend added by reload starts unhealthy and is admitted after one successful probe. Backends at process startup start healthy.** At startup there is no serving fleet to protect, so the process trusts the file and lets the checker correct it. During a reload, traffic is already flowing, and a typo in a new URL must not cause client 502s. The first successful probe is a reinstatement with reason `initial_probe`. A failed first probe logs no transition, because the backend was never healthy. Recovery after an ejection still needs two consecutive successes. The `/startupz` latch is never cleared (ADR-0014).
11. **A reload that replaces every backend (blue/green) is allowed.** All added backends start unhealthy, so `Selectable()` is empty until the first probe succeeds. In that window `/readyz` reports not ready and clients get 503. The `config_reloaded` line logs at WARN when `unchanged=0`.
12. **The loaded config is one atomic pointer on the application.** A successful reload replaces it, so each reload diffs against the current config, not the startup config.

### Two triggers, one operation

13. **SIGHUP reloads from the config file. `POST /admin/reload` on the metrics listener reloads from a YAML body.** The body uses the same strict decoder and `${VAR}` expansion as the file (`config.Parse`, which `config.Load` calls). The response is 200 on success and 422 with the error text on any rejection (parse, validation, non-backend change, apply). The body limit is 1 MiB (413 above it). Before `Run` starts, the endpoint returns 503.
14. **Every reload goes through one operation that parses, validates, applies and records the outcome, under one mutex.** Both triggers log the same way (`config_reload_failed` with `parse_error` or `validation_error`). `App.Reload` takes an already-validated config and the same mutex.
15. **After a body reload, the file on disk is not authoritative until the next SIGHUP.** The next reload diffs against the body's config. A SIGHUP applies the file again. The endpoint never writes the file.
16. **`GET /admin/reload/last` returns the last outcome.** Before any attempt it returns `{"result":"none"}`. After an attempt it returns `{"result":"success"|"rejected","time":<RFC 3339>,"error":<text>}`. The record is one atomic pointer, covers both triggers, and is lost on restart.
17. **An endpoint reload runs on the application's run context, not the request context.** A reload starts long-lived work (a prober per added backend, a drain per removed backend) that must outlive the HTTP request.

The admin endpoint is not a production control plane. It has no authentication, authorization, persistence or audit beyond the reload log line. It exists for the demo and for tests. No compose file publishes the metrics listener (ADR-0013). The endpoint takes a body because a signal carries no payload and returns no result.

### Algorithm reload

18. **The proxy reads its selector through one atomic pointer.** A reload that changes `algorithm` builds the new selector first. If the build fails, the whole reload is rejected and nothing changes. Then the reload applies the backend diff, stores the new selector with one atomic store, and records the loaded config. Validation still rejects an unknown algorithm.
19. **No request is affected by the selector swap.** A request calls the selector once, at the start of `ServeHTTP`. A dispatched request keeps its backend. A request that arrives during the swap sees either the whole old selector or the whole new one. The listener stays open, so no connection resets.
20. **The new selector starts warm.** Every selector is built from the registry, and the state that steers selection lives on the backend: the P2C-EWMA latency (ADR-0010, fed by an observer for every algorithm) and the active-request count (ADR-0007, ADR-0009). The ring is built from the current snapshot. Only the round-robin index starts again, which does no harm.
21. **The swap is visible.** The `config_reloaded` log line carries `previous_algorithm` and `algorithm`. `lb_algorithm_info` (ADR-0013) moves to the new algorithm.

## Consequences

- Reload keeps all learned state for unchanged backends, with no second process, no file-descriptor passing and no accept-boundary drop.
- The diff is pure, so it is fully table-testable with no registry, server or clock.
- One snapshot behind one atomic pointer, plus one mark before the swap, hides reload from every observer.
- The algorithm changes in place, warm, with no effect on in-flight requests.
- `Backend` grows the removed-flag accessor, another method-only convention.
- A blue/green reload has an empty-selectable window that lasts until the first successful probe.
- Changes to other config sections need a restart.
- Rejected alternatives:
  - SO_REUSEPORT handoff (loses learned state).
  - Identity by name only (moves state to a different host).
  - An "updated" diff entry (a second code path for remove plus add).
  - A sorted diff (file order matters).
  - Added backends starting healthy (client 502s from a typo).
