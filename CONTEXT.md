# l7LoadBalancer

A Layer 7 HTTP load balancer. This glossary covers domain vocabulary — algorithm
and routing concepts — not implementation structure (see `AGENTS.md` and
`docs/design/` for that).

## Language

**Backend**:
An upstream server the load balancer can route requests to, tracked by the
registry for identity, health, and active-connection count.
_Avoid_: server, upstream, node (reserve "node" for hash-ring positions)

**Selector**:
The pluggable policy that chooses which backend handles a given request.
_Avoid_: algorithm (an algorithm is what a Selector implements — the Selector
is the seam; "algorithm" is fine in prose describing behavior)

**Ring**:
The sorted-by-hash-position structure of virtual nodes that consistent-hash
selection walks to map a hash key to a backend. Internal to `balancer`; not
itself a Selector.
_Avoid_: hash ring (redundant once "ring" is established here), consistent
hashing (that's the algorithm family; the ring is its data structure)

**Virtual node**:
One of several ring positions a single backend occupies — 150 per backend,
each hashed independently from `backend.Name` plus a replica index. Exists to
smooth the distribution a single backend would get from occupying only one
ring position.
_Avoid_: replica, node alone (ambiguous with "backend")

**Hash key**:
The per-request string hashed to find a ring position — the client's IP
(`r.RemoteAddr`, port stripped). Determines which backend a request's session
sticks to under consistent-hash selection. Known limitation: behind another
proxy, this is the upstream proxy's IP, not the original client's.
_Avoid_: routing key, partition key

**Load** (consistent-hash-bounded-loads context):
A backend's current `ActiveConns()` — in-flight request count — not a
cumulative/lifetime assignment counter. Bounded-loads' average is computed
over healthy backends only.
_Avoid_: weight, score, connections (use ActiveConns when referring to the
method; "load" is the domain concept it backs)

**Capacity** (consistent-hash-bounded-loads context):
The ceiling a candidate backend's load must not exceed to be selected:
`max(1, ceil(avg_load * (1 + ε)))`, where `avg_load` is mean `ActiveConns()`
across healthy backends and ε = 0.25 (Mirrokni-Thorup-Zadimoghaddam's cited
production value). The floor of 1 exists because an idle system has
`avg_load = 0`, which would otherwise fail every backend's very first request.
_Avoid_: cap used without the floor, threshold

**EWMA latency** (p2c-ewma context):
A backend's exponentially weighted moving average of recent round-trip
durations, `latency_new = α·observed + (1-α)·latency_old` with α = 0.1,
maintained by `Backend.RecordLatency` and read by `PowerOfTwoChoicesEWMA`. It
measures the backend round trip only — from just before dispatch to the
response headers — deliberately not the client-facing request duration the
`latency_ms` log field measures. A failed round trip records a fixed 2s
penalty rather than the real time-to-failure, so a fast failure cannot look
attractively fast. The first-ever sample is stored directly rather than blended
from a zero baseline. See ADR-0010.
_Avoid_: latency (unqualified), response time, EWMA alone

**Probe** (active health check context):
A single out-of-band GET issued by the health checker to a backend's
configured URL (not the request-serving path — no separate health endpoint
is configured). Only a 2xx response counts as success; 3xx is treated as a
failure because `httputil.ReverseProxy` forwards redirects verbatim to
clients rather than following them, so a redirecting backend is unusable
even though it is "up." Consecutive probes drive the health state machine:
3 consecutive failed probes eject a backend, 2 consecutive successful ones
reinstate it — and active probes are the *only* path that reinstates a
backend ejected by passive detection. The thresholds are Go constants, not
config. See ADR-0011 decisions 2 and 10.
_Avoid_: health check (the subsystem), ping

**Outlier** (passive health check context):
A backend whose recent live request outcomes are disproportionately
failures — a 5xx response or a transport failure (connection refused,
timeout). Detected in-band by `health.OutlierDetector` from the proxy's
round-trip observer fan-out, over a count-based sliding window of the last
10 outcomes per backend (not a time window, and not a consecutive streak).
Distinct from an unhealthy-by-probe backend: an outlier may still be
answering, just badly. See ADR-0011 decisions 8 and 12.
_Avoid_: unhealthy (that is the state it may lead to, not the concept)

**Ejection** (passive health check context):
Marking a backend unhealthy (`MarkUnhealthy`) because enough failures
appeared within its outlier window — 5 of the last 10. Ejection excludes the
backend from selection immediately, without waiting for the next scheduled
probe. Only a successful active probe reinstates it, so passive detection has
no independent timer and never calls `MarkHealthy`; it resets its own episode
once it observes the backend healthy again. See ADR-0011 decisions 2, 3, and 9.
_Avoid_: circuit-open (a separate gate), removal

**Selectable** (Sprint 3 context):
A backend eligible for routing right now: healthy (per active/passive
detection) *and* its circuit is not open. Distinct from `IsHealthy()`,
which reflects only the active/passive signal and says nothing about
circuit state. `Registry.Selectable()` is what every selector iterates;
`IsHealthy()` stays the narrower, honest name for the raw signal. See
ADR-0011.
_Avoid_: healthy (as a synonym once circuit state exists), available

**Trial** (circuit-breaker context):
The single request a half-open circuit admits to decide whether it
returns to Closed (trial succeeds) or back to Open (trial fails). Not a
synthetic probe — it is whatever real request the selector happens to
route to the backend next while half-open. Admission is enforced by the
breaker's own `Allow()` gate, not by pool membership: a half-open backend
stays fully `Selectable()`. See ADR-0011.
_Avoid_: probe (reserved for the active health-check context), health check
