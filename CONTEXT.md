# l7LoadBalancer glossary

Domain terms for this Layer 7 HTTP load balancer. Use these words with these meanings in code, comments and docs. For structure, see `docs/architecture.md`.

## Routing

**Backend**:
A server that the load balancer can send requests to. The registry keeps its identity, health and active-connection count.
_Avoid_: server, upstream, node ("node" is for ring positions).

**Selector**:
The pluggable policy that chooses the backend for a request. A Selector implements an algorithm. "Algorithm" is fine in prose about behavior.
_Avoid_: algorithm (for the interface).

**Selectable**:
A backend that can get traffic now. It is healthy and its circuit is not open. `Registry.Selectable()` is the list that every selector uses. `IsHealthy()` shows only the health signal and ignores the circuit.
_Avoid_: healthy (as a synonym), available.

**Ring**:
The structure of virtual nodes, sorted by hash position, that consistent-hash selection walks to map a hash key to a backend. It is internal to `balancer` and is not a Selector.
_Avoid_: hash ring, consistent hashing (that is the algorithm family).

**Virtual node**:
One of the 150 ring positions of one backend. Each position is the hash of `backend.Name` plus a replica index. Many positions give a more even distribution than one.
_Avoid_: replica, node (alone).

**Hash key**:
The string that the balancer hashes to find a ring position. It is the client IP from `r.RemoteAddr`, without the port. Limitation: behind another proxy, it is the IP of that proxy.
_Avoid_: routing key, partition key.

**Hot key**:
A hash key with a very large share of requests. With one client address, an unbounded ring sends all traffic to one backend. Bounded loads moves the excess to the next backends on the ring.
_Avoid_: hotspot, sticky client.

**Load** (bounded loads):
The current `ActiveConns()` of a backend, which is its in-flight request count. It is not a total count over time. The average load uses healthy backends only.
_Avoid_: weight, score, connections.

**Capacity** (bounded loads):
The maximum load a candidate backend can have and still get the request: `max(1, ceil(avg_load * (1 + ε)))`, with ε = 0.25. The floor of 1 is necessary because an idle system has `avg_load = 0`. Without it, every first request would fail.
_Avoid_: cap without the floor, threshold.

**EWMA latency** (p2c-ewma):
The exponentially weighted moving average of the backend round trip: `latency_new = α·observed + (1-α)·latency_old`, with α = 0.1. `Backend.RecordLatency` writes it and `PowerOfTwoChoicesEWMA` reads it. It measures from just before dispatch to the response headers. It is not the client-facing duration in the `latency_ms` log field. A failed round trip records a fixed 2s penalty, so a fast failure does not look fast. The first sample is stored directly. See ADR-0010.
_Avoid_: latency (alone), response time, EWMA (alone).

## Health and circuit breaking

**Probe** (active health check):
One out-of-band GET from the health checker to the backend URL. There is no separate health endpoint. Only 2xx is success. 3xx is a failure, because `httputil.ReverseProxy` sends redirects to clients and does not follow them. 3 failed probes in a row eject a backend, and 2 successful probes in a row reinstate it. A backend that a reload adds is the exception: one successful probe admits it, with reason `initial_probe`. Only probes reinstate a backend that passive detection ejected. The thresholds are Go constants, not config. See ADR-0011 and ADR-0015.
_Avoid_: health check (the subsystem), ping.

**Outlier** (passive health check):
A backend whose recent live requests fail too often. A failure is a 5xx response or a transport failure, such as connection refused or a timeout. `health.OutlierDetector` counts the last 10 outcomes for each backend. This is a count window, not a time window and not a streak. An outlier can still answer, but badly. See ADR-0011.
_Avoid_: unhealthy (the result, not the concept).

**Ejection** (passive health check):
`MarkUnhealthy` on a backend that has 5 failures in its last 10 outcomes. Selection stops at once, before the next probe. Passive detection has no timer and never calls `MarkHealthy`. Only a successful probe reinstates the backend. See ADR-0011.
_Avoid_: circuit-open, removal.

**Circuit breaker**:
A per-backend gate with three states: Closed, Open and Half-Open. It stops traffic to a backend whose round trips fail. It never calls `MarkHealthy` or `MarkUnhealthy`. Selectable is health AND circuit, two separate facts. Closed counts consecutive failures, and any success resets the count. It opens at 3. Open denies all requests until the cooldown ends. Half-Open admits one trial. `Backend` keeps the state as one CAS-guarded snapshot. `circuit.Breaker` keeps the policy. See ADR-0011 and ADR-0012.
_Avoid_: unhealthy, ejection, outage.

**Cooldown**:
The time a circuit stays Open before it can go to Half-Open. It is the global `circuit.cooldown` YAML field, default 30s. There is no timer. The next request or selection that reads the state after the cooldown does the change with a CAS. If no traffic goes to the backend, nothing sees it recover. See ADR-0011 and ADR-0012.
_Avoid_: timeout, retry delay.

**Trial**:
The one request that a Half-Open circuit admits. If it succeeds, the circuit closes. If it fails, the circuit opens again. It is a real request, not a probe. The breaker's `Allow()` controls admission, so a Half-Open backend stays `Selectable()`. See ADR-0011.
_Avoid_: probe, health check.

**Client cancellation**:
The client disconnects before a response arrives, so its request context is done. This is not a backend failure. The proxy tells no observer, records no EWMA latency, releases the active-connection slot and records status 499 (class `4xx`). The 5xx class stays for backend failures. A backend or transport timeout is different: the client context is still live, so it is a real failure.
_Avoid_: timeout, backend failure.

## Reload

**Reload**:
The process replaces its backend set and selection algorithm from a config file, without a restart and without dropping in-flight requests. A reload starts on SIGHUP (re-read the file) or on `POST /admin/reload`. Only the backend list and the algorithm can change. If any other field changes, or the config does not parse or validate, the reload is rejected and the old config continues. Reloads never overlap. Each one is compared with the current config.
_Avoid_: restart, hot-restart.

**Backend identity**:
The (name, URL) pair. Two configs have the same backend only when both match. A name with a new URL is a different backend: the old one is removed and drains, and a new one starts with new state. For a short time, two backends with the same name can exist: one draining and one new.
_Avoid_: same backend (when only the name matches).

**Unchanged / added / removed backend**:
The three results of a reload diff by backend identity. An unchanged backend keeps all runtime state: health, circuit, active connections and EWMA latency. An added backend starts unhealthy and becomes selectable after one successful probe. A removed backend starts to drain. After that, its late results and probes change no health, circuit or metric state. There is no "updated" result.
_Avoid_: updated, modified.

**Draining**:
The state of a removed backend that still has in-flight requests. It is never selected again. Its requests can finish until none remain or the drain window ends. Later reloads do not change it. If the same identity comes back, that is a new backend.
_Avoid_: disabled, unhealthy, ejection.

**Drain window**:
The maximum time a draining backend can keep in-flight requests. Set it with `reload.drain_window` (default 30s). When it ends, the proxy cancels the remaining requests, the clients get 502, and the registry forgets the backend. A drain cancellation is not a backend failure.
_Avoid_: grace period, timeout.

**No-op reload**:
A reload where all backends are unchanged. It measures the bare cost of the reload path. A **drain reload** removes at least one backend.
_Avoid_: empty reload, dry reload.

## Benchmarks

**Competitor**:
The proxy under test in one benchmark run: the load balancer or Nginx. Both use the same backends and the same load generator.
_Avoid_: target, baseline.

**Matched**:
A comparison where both proxies use the same algorithm, so differences come from implementation and setup. Round-robin and least-connections are matched.
_Avoid_: like-for-like, apples-to-apples.

**Nearest-equivalent**:
A comparison against an Nginx config that is close but not the same: `hash $remote_addr consistent` for consistent-hash (no bounded loads) and `random two least_conn` for p2c-ewma (no latency signal). These results always state the gap.
_Avoid_: equivalent (alone).

**Peak**:
The highest sustained request rate, in req/s, of a proxy in one benchmark. A rate is sustained when p99 latency is below 100 ms, fewer than 1% of requests fail, and the load generator sends at least 95% of the requested rate. The benchmark increases the rate until a test fails, then searches for the last rate that passed. The step size is 500 req/s for small responses and 25 req/s for 1 MiB responses.
_Avoid_: max throughput, capacity.

**Rig-limited**:
A peak that shows the limit of the load generator or test machine, not of the proxy. The generator has two CPU cores, so for large responses it cannot reach the proxy's limit. A rig-limited number is a lower bound, never the ceiling.
_Avoid_: bottlenecked, saturated.
