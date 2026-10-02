# Design decisions

This document explains the main design choices in the load balancer. Each topic states the problem, the options considered, the choice, and what the choice costs. The reasoning comes first and the mechanism second. Each topic links the decision records (ADRs) that hold the full detail. The benchmark compares against Nginx, a widely used web server and proxy. Numbers come from the retained benchmark output in [`RESULTS.md`](../RESULTS.md), from the repository's tests, or from the ADRs. Each number carries its unit and its meaning.

## 1. `net/http/httputil` rather than a framework or a hand-built proxy

**The problem.** A load balancer here works at Layer 7. That means it reads HTTP requests, not just raw network bytes. It acts as a reverse proxy: it accepts a request from a client, forwards it to one of several backend servers, and relays the response. Forwarding correctly involves chores that have nothing to do with choosing a backend. Hop-by-hop headers must be dropped, because they describe one network connection and must not cross to the next. The client's address must be added in `X-Forwarded-For`. Bodies must be streamed and flushed without buffering them whole.

**The options.**

- A proxy framework or third-party reverse-proxy library.
- A proxy written from scratch on `net/http`.
- The standard library's `net/http/httputil.ReverseProxy`, which does the chores above and exposes hooks around the forwarding step.

**The choice.** The project uses `httputil.ReverseProxy`. The selection layer, the failure handling and the reload logic are where this project does its own work. Rewriting header handling and streaming would add code without adding any decision worth defending. A framework would hide the request lifecycle that the project exists to show. This is recorded in the [ADR index](adr/INDEX.md) (no standalone ADR) and again in [ADR-0007](adr/0007-proxy-request-lifecycle-and-exactly-once-decrement.md), which lists the hand-built proxy and `NewSingleHostReverseProxy` as rejected alternatives.

The proxy handler calls the backend selector itself, before handing the request to `ReverseProxy`. The reason is that a hook inside `ReverseProxy` has no response writer. It cannot answer "503, no healthy backend" on its own. ADR-0007 also fixes how the count of requests in flight on each backend stays correct. The count goes up when a request is dispatched. It goes down exactly once, whether the response completes normally or the round trip fails. A `sync.Once` guards that decrement. That is a Go type that runs a piece of code at most once, however many callers reach it.

**What it costs.**

- The request path is not standard-library-only. Non-test code depends on three third-party modules:
  - `golang.org/x/net` (the HTTP/2 package), used for h2c.
  - `github.com/prometheus/client_golang` (the Prometheus client), used for metrics.
  - `gopkg.in/yaml.v3` (the YAML library), used for configuration.
- Tests also import `testify` and Prometheus's `client_model`.
- **HTTP/2** is the newer version of the HTTP protocol. It sends many requests at once over one connection. **h2c** is HTTP/2 without TLS (encryption). The standard library handles only the form where the client opens with the HTTP/2 preface. The form that upgrades an HTTP/1.1 connection needs `x/net/http2/h2c`. That package is marked deprecated but is still the only implementation of both forms. If the upgrade form is dropped, the standard library alone would suffice. The [ADR index](adr/INDEX.md) records this choice.
- The project inherits `ReverseProxy`'s behaviour where it is awkward. For example, the proxy cannot join a backend URL's path prefix, and ADR-0007 records that as a known limitation.

The project also declines to claim more than it built. [ADR-0005](adr/0005-scope-of-production-grade.md) defines "production-grade" as patterns that can be defended. It excludes security hardening, capacity planning and operations.

## 2. Bounded-loads consistent hashing

**The problem.** Some applications need *session affinity*. The same client should reach the same backend each time, so the backend can keep that client's state in memory. **Consistent hashing** provides this. The balancer hashes a request key (here, the client's IP address) to a position on a circle called the *ring*. Each backend owns arcs of that circle. The request goes to the owner of the arc it lands on. When a backend joins or leaves, only about `1/n` of the keys move.

The weakness is the **hot key**: one key that carries a large share of the requests. The ring knows nothing about load, so the backend that owns that key takes all of its traffic.

**The options.**

- Plain consistent hashing: simple and stable, but a hot key overloads one backend.
- Bounded loads: the same ring, but a backend that is above a load cap is skipped, and the request moves to the next backend around the ring.
- Rebuilding the ring whenever a backend gets hot. This was rejected in [ADR-0009](adr/0009-consistent-hash-bounded-loads-capacity-and-evidence.md). It would reshuffle every key and discard the affinity the algorithm exists to provide.
- Dropping affinity and using least connections. That gives up the property the algorithm is chosen for.

**The choice.** The project uses bounded loads, from the 2016 paper "Consistent Hashing with Bounded Loads". The ring itself ([ADR-0008](adr/0008-consistent-hash-ring-pipeline-and-vnode-layout.md)) uses 150 *virtual nodes* per backend. A virtual node is one of many ring positions a backend occupies, which smooths the arcs. Keys are hashed with FNV-1a-64 followed by a Murmur3-style finishing step. Without that finishing step, 256 consecutive IP addresses landed on only 3 of 4 backends in the ADR-0008 measurement. With it, they landed on all 4.

The load cap is set in [ADR-0009](adr/0009-consistent-hash-bounded-loads-capacity-and-evidence.md):

```
capacity = max(1, ceil(average_in_flight × 1.25))
```

*In flight* means accepted but not yet finished. The average runs over the backends that can currently take traffic. The number 0.25 is called epsilon. It is the tolerated excess over the average. A backend is admitted when its in-flight count is at most the capacity. The balancer computes the average before it counts the incoming request.

**A worked example.** Four backends have 8, 2, 1 and 1 requests in flight.

- The average is (8 + 2 + 1 + 1) ÷ 4 = 3 requests.
- The capacity is ceil(3 × 1.25) = ceil(3.75) = 4 requests.
- The first backend has 8 in flight, which is above 4. If a key hashes to it, the balancer skips it and walks clockwise to the next backend within capacity.
- When the system is idle, the average is 0 and the formula gives 0. The floor of 1 prevents a zero cap that would reject every first request.

**The evidence.** Three results from the repository and the benchmark support this choice.

1. *One fixed workload.* A test sends 10,000 requests from 100 client addresses with Zipf-skewed popularity to 4 backends. Zipf-skewed means a few clients send most of the requests. Plain consistent hashing put 4,005 requests (40.0%) on the busiest backend. Bounded loads put 3,126 (31.3%). The cap was ceil(10,000 ÷ 4 × 1.25) = 3,125 requests. The result is one over, because admission uses "at most" rather than "below". The random seed (2253) was chosen from a sweep of 2,500 seeds because it reproduces the figures fixed in the original design. It is one workload, not a typical one.
2. *Sixty seeds.* The same workload with 60 different seeds is the fairer statement. The busiest backend under plain hashing took between 29.9% and 51.3% of requests, with a median of 40.0%. Under bounded loads it took between 30.0% and 31.3%. When the first-choice backend was already under the cap, bounded loads did nothing. The cap is a ceiling, not a rebalancing target.
3. *The benchmark against Nginx.* In the benchmark, a single load-generator address makes every request a hot key. Nginx uses its own unbounded hash. This is a nearest-equivalent comparison, not a matched one: Nginx has no bounded-loads option. In every core Nginx result, at least 97.97% of requests went to one backend. At the 10 KiB (10 kibibytes, 10,240 bytes) response size and peak load, the bounded ring put 82.27% on the owning backend and 17.19% on a second. At that load, Nginx put 99.96% on its owner. This is the largest spill in the table. The ring spills a minority of the hot key. It does not rebalance it.

At the 1 MiB (1 mebibyte) response size and 30% of peak load, the bounded ring did not spill: the owner took 97.74%. This is expected, and the run does not measure it. The capacity floor is 1 request in flight. If few requests are in flight at once, the cap never binds. By Little's law, average requests in flight equals the arrival rate times the time each request takes. As an illustration only, 60 requests per second at 8 ms each averages 0.48 requests in flight, below the floor of 1. The run did not record those two figures.

**What it costs.**

- The load measure is the in-flight count. It reacts to concurrency, not to request rate or latency. A slow backend is not noticed until requests pile up on it.
- The cap protects only against skew that is large enough to push a backend past the average by more than 25%.
- The hash key is the client's address with the port removed. Behind another proxy, that address belongs to the proxy.
- The bounded ring does not reproduce what Nginx does. Any throughput difference between the two partly measures the extra capacity check, so this document does not quote one.

## 3. Power of two choices with EWMA latency

**The problem.** A backend can be up, answering, and slow. Perhaps its hardware is degraded or a dependency is struggling. Count-based algorithms such as least connections cannot see this until requests queue up. The balancer needs a signal that is available before the queue forms: how long the backend takes to respond.

**The options.**

- Least connections: a full scan of the backends. Simple, but blind to speed.
- Scan every backend and choose the lowest latency. Every request arriving in the same instant would pick the same backend. That backend would slow down, and the traffic would then swing to another one.
- **P2C** (power of two choices): pick two backends at random and send the request to the better of the two. Randomness spreads the load, and the comparison still avoids the worst backend.

**The choice.** The project uses P2C, scored by an **EWMA** (exponentially weighted moving average). An EWMA is a running average that gives recent samples more weight than old ones. Each backend keeps one estimate, updated after every request as follows ([ADR-0010](adr/0010-p2c-ewma-backend-latency-state-cold-start-and-failure-penalty.md)):

```
new_estimate = 0.1 × observed + 0.9 × old_estimate
```

The weight 0.1 makes the estimate stable against single slow requests. It adapts to a lasting change in about ten requests. The measured window is the backend round trip only, from just before dispatch to the arrival of the response headers. A slow client reading a streamed body does not penalise a fast backend.

Two rules handle the edges. These are both in ADR-0010.

- **Cold start** means the state of a backend with no samples yet. The first sample is stored as it is, not blended with zero. A backend with no sample reads zero, which counts as the fastest possible. It wins its next comparison, receives a real sample, and stops reading zero.
- **Failure penalty.** A round trip that fails before any response is recorded as 2 seconds, not as its real elapsed time. A backend that fails instantly, for example with "connection refused", would otherwise record almost zero and look attractive.

**A worked example.** Four backends: one estimated at 10 ms and three at 500 ms.

- Two backends are sampled per request. There are 6 possible pairs. The fast backend is in 3 of them, which is 50%, and it wins each time.
- In the other 3 pairs, two slow backends compete. Each slow backend therefore takes about 50% ÷ 3 ≈ 16.7% of requests.
- A test makes 4,000 selections with these estimates. In one recorded run the fast backend took 2,043 (51.1%) and the slow ones took 671, 647 and 639 (16.8%, 16.2% and 16.0%). The exact split varies from run to run, so the 50% and 16.7% figures are the ones to remember.
- If the 10 ms backend serves one 500 ms request, its estimate moves to 0.1 × 500 + 0.9 × 10 = 59 ms. A second such request moves it to 0.1 × 500 + 0.9 × 59 = 103.1 ms.
- One failure from 10 ms moves it to 0.1 × 2,000 + 0.9 × 10 = 209 ms.

**What it costs.** The estimate has no decay. *Decay* would mean the estimate drifts back toward "unknown" over time. Here nothing lowers it while a backend receives no traffic. This has a direct consequence.

- A backend with the highest estimate loses every comparison it appears in. It therefore receives no requests, and no new sample can arrive to correct the estimate.
- If that backend recovers to 10 ms and the others stay at 500 ms, it is still ranked last. It keeps its stale high estimate. Its share of traffic can stay at zero.
- The active health checker does not feed the estimate either. Only proxied responses do.
- The state is also stale after a switch of load balancer. In the live demo, which runs one load balancer per algorithm ([ADR-0023](adr/0023-local-live-demo.md)), a load balancer that carried traffic earlier keeps the estimates from when it last did. A load balancer that never carried traffic starts cold, with every backend reading zero. In both cases, the demo allows a convergence period before any claim about the traffic split.

Two smaller costs apply. A client that cancels a request still records the failure penalty in this design (ADR-0010 notes this). The 0.1 weight and the 2-second penalty are constants, not settings.

**Why there is no live divergence evidence.** The benchmark has a scenario that slows one backend to see least connections and P2C move traffic away from it. That run is unreliable. Every median latency sat near the injected 50 ms, which is consistent with the delay reaching all four backends. The suspected cause is an exported environment variable leaking through a shared compose definition. That cause is **UNVERIFIED**. No numbers from that scenario are quoted here. The evidence for P2C is therefore the test above and the arithmetic, not a measured comparison against Nginx.

## 4. Reload architecture

**The problem.** An operator edits the list of backends and wants the change applied without dropping a request. Three things must hold. Backends that stay should keep everything the balancer has learned about them. New backends should receive traffic only once they are proven. Removed backends should finish the requests they are already handling.

**Terms.** **SIGHUP** is a Unix signal that many servers treat as "reread your configuration". A **reload** here means the process applies a new backend list in place when it receives that signal. An **immutable snapshot** is a read-only copy of the state. Readers use it without locking, and a writer replaces it as a whole. To **drain** a backend is to stop sending it new requests and let its current requests finish.

**The options.**

- Start a second process and hand over the listening socket (`SO_REUSEPORT`). A fresh process cannot keep the health, circuit and latency state of unchanged backends without moving that state across processes.
- Restart the process. This drops requests that are in flight.
- Swap the backend set inside the running process.

**The choice.** The project swaps the backend set in place ([ADR-0015](adr/0015-reload-architecture.md)).

- The registry (the balancer's list of backends) holds one atomic pointer to an immutable snapshot. An atomic pointer is a pointer that can be replaced in one indivisible step. The snapshot is a version number and the ordered backend list. Readers load it once and never lock. The reload builds the next snapshot and swaps the pointer.
- A backend's identity is its name and URL together. A name kept with a new URL counts as one removal plus one addition. State learned about the old host is never applied to a different host.
- Unchanged backends keep the same object, with all their state. Added backends start unhealthy and join after one successful health probe, which is a small request the balancer sends to check that a backend answers. A typo in an added URL therefore cannot send clients errors while the old backends serve correctly. At process start, backends begin healthy, because nothing is serving yet.
- Only the backend list can be reloaded. Any other difference rejects the whole reload and names the fields. A silent no-op is worse than a clear refusal. The consistent-hash ring rebuilds when it sees a new snapshot version.
- Removed backends are marked removed before the swap. They report nothing to the failure detectors or to the metrics, so no ghost failures appear. The failure detectors are the circuit breaker, which stops sending to a backend that keeps failing, and the outlier detector, which ejects a backend that fails too often in recent requests.

The drain is described in [ADR-0016](adr/0016-drain-lifecycle.md). The drain window is a setting, `reload.drain_window`, with a default of 30 s. Each removed backend gets one drain goroutine. A goroutine is Go's lightweight thread. It waits for the backend's in-flight count to reach zero, checking every 100 ms. If the window ends first, it retires the backend. This cancels what remains. A request still waiting for response headers gets a 502 with the reason `window_expired`. A request already streaming its body is cut short, and the success recorded when its headers arrived stands.

The packaging choice is in [ADR-0019](adr/0019-deployment-target.md): the target is the Docker container, with the binary as the container's first process (PID 1), so it receives signals directly. A reload is `docker kill -s HUP <container>`. A restart (`docker restart`) sends SIGTERM, the signal that asks a process to shut down. It starts a fresh process, which does drop in-flight requests.

**A worked drain example.** The window is 30 s. A removed backend has 3 requests in flight.

- The balancer stops selecting it at the swap. No new request reaches it.
- If all 3 finish at 2 s, the drain ends at 2 s.
- If one is still running at 30 s, the backend is retired and that request is cancelled. If it was still waiting for headers, the client gets a 502, the HTTP status for a bad response from an upstream server.

This example is illustrative. The benchmark drain finished well inside its window and never reached the 30 s limit.

**The evidence.** The table reports p99, the latency that 99 of every 100 requests stayed under, before and after the reload. The benchmark ran one run per reload scenario, with round-robin only and HTTP/2 over h2c at 10 KiB responses. Each ran for 60 s with the reload at 29 s. The generator was asked for 1,750 req/s (requests per second). It delivered less: an average of about 1,121 req/s for the no-op reload and about 1,137 req/s for the drain reload. Every request it sent was answered.

| Run | Errors | p99 before the reload | p99 after the reload |
| --- | --- | --- | --- |
| No-op reload (same list, signal sent) | 0 non-2xx, 0 transport errors | 1.752 ms | 1.762 ms |
| Drain reload (one backend removed) | 0 non-2xx, 0 transport errors | 1.856 ms | 2.140 ms |

The removed backend had received 95,540 requests when the run took its first snapshot and 95,710 when the reload applied. It still showed 95,710 at the end of the run. It received no request after the reload applied. This is one run each.

**What it costs.**

- A reload that replaces every backend leaves a short window with nothing selectable, because added backends start unhealthy. Requests get a 503 (service unavailable) for that window, and the log marks it at WARN, its warning level. Probing added backends before the swap is a proposed change, not built.
- A change to the listening address, the algorithm or the timeouts needs a restart, which drops in-flight requests. There is no socket handoff. That handoff is listed as future work.
- The drain truncates a response that is still streaming when the window ends.
