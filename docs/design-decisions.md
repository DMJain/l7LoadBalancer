# Design decisions

This document explains the main design choices in the load balancer. Each topic states the problem, the options considered, the choice, and what the choice costs. The reasoning comes first and the mechanism second. Each topic links the decision records (ADRs) that hold the full detail. The benchmark compares against Nginx, a web server and proxy. Numbers come from the retained benchmark output in [`RESULTS.md`](../RESULTS.md), from the repository's tests, from fixed values in the code, from the tracking file, or from the ADRs. Each number carries its unit and its meaning.

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
- Dropping affinity and using least connections, which sends each request to the backend with the fewest requests in flight. That gives up the property the algorithm is chosen for.

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
3. *The benchmark against Nginx.* In the benchmark, the program that sends the test requests (the load generator) has a single address, so every request is a hot key. Nginx uses its own unbounded hash. A **matched** comparison runs the same algorithm on both sides. A **nearest-equivalent** comparison uses the closest Nginx setting when Nginx has no identical algorithm. This one is nearest-equivalent: Nginx has no bounded-loads option. "Peak" is the highest request rate a proxy sustained in the benchmark; topic 6 defines how it is found. In every HTTP/2 Nginx result, at least 97.97% of requests went to one backend. At the 10 KiB (10 kibibytes, 10,240 bytes) response size and peak load, the bounded ring put 82.27% on the owning backend and 17.19% on a second. At that load, Nginx put 99.96% on its owner. This is the largest spill in the table. The ring spills a minority of the hot key. It does not rebalance it.

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

**Why there is no live divergence evidence.** The benchmark has a scenario that slows one backend to see least connections and P2C move traffic away from it. That run is unreliable. Every median latency (the middle value: half the requests were faster) sat near the injected 50 ms, which is consistent with the delay reaching all four backends. The suspected cause is an exported environment variable leaking through a shared Docker Compose definition (the file that describes the test containers). That cause is **UNVERIFIED**. No numbers from that scenario are quoted here. The evidence for P2C is therefore the test above and the arithmetic, not a measured comparison against Nginx.

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

The packaging choice is in [ADR-0019](adr/0019-deployment-target.md): the target is the Docker container (a packaged process that runs in isolation), with the binary as the container's first process (PID 1), so it receives signals directly. A reload is `docker kill -s HUP <container>`. A restart (`docker restart`) sends SIGTERM, the signal that asks a process to shut down. It starts a fresh process, which does drop in-flight requests.

**A worked drain example.** The window is 30 s. A removed backend has 3 requests in flight.

- The balancer stops selecting it at the swap. No new request reaches it.
- If all 3 finish at 2 s, the drain ends at 2 s.
- If one is still running at 30 s, the backend is retired and that request is cancelled. If it was still waiting for headers, the client gets a 502, the HTTP status for a bad response from an upstream server.

This example is illustrative. The benchmark drain finished well inside its window and never reached the 30 s limit.

**The evidence.** The table reports p99, the latency that 99 of every 100 requests stayed under, before and after the reload. The benchmark ran one run per reload scenario, with only round-robin (each backend in turn) and HTTP/2 over h2c at 10 KiB responses. Each ran for 60 s with the reload at 29 s. The generator was asked for 1,750 req/s (requests per second). It delivered less: an average of about 1,121 req/s for the no-op reload and about 1,137 req/s for the drain reload. Every request it sent was answered.

| Run | Errors | p99 before the reload | p99 after the reload |
| --- | --- | --- | --- |
| No-op reload (same list, signal sent) | 0 non-2xx, 0 transport errors | 1.752 ms | 1.762 ms |
| Drain reload (one backend removed) | 0 non-2xx, 0 transport errors | 1.856 ms | 2.140 ms |

The removed backend had received 95,540 requests when the run took its first snapshot and 95,710 when the reload applied. It still showed 95,710 at the end of the run. It received no request after the reload applied. This is one run each.

**What it costs.**

- A reload that replaces every backend leaves a short window with nothing selectable, because added backends start unhealthy. Requests get a 503 (service unavailable) for that window, and the log marks it at WARN, its warning level. Probing added backends before the swap is a proposed change, not built.
- A change to the listening address, the algorithm or the timeouts needs a restart, which drops in-flight requests. There is no socket handoff. That handoff is listed as future work.
- The drain truncates a response that is still streaming when the window ends.

## 5. Failure-mode interaction

**The problem.** A backend can fail in several ways. It can be down. It can be up but answer with errors. It can fail in bursts and then recover. The balancer has three mechanisms that each decide "stop sending traffic here". They work on different timescales. If they are not composed with care, they fight. [ADR-0011](adr/0011-health-passive-outlier-and-circuit-breaker-composition.md) names two failures it set out to avoid.

- If the circuit breaker cleared the same health flag the other two mechanisms use, a tripped backend would leave the candidate pool. Nothing would ever route the one request that tests whether it has recovered.
- If three mechanisms write one flag, the last writer wins and silently overrides the others.

**The three mechanisms.**

- **Active health check.** The balancer sends a plain GET to each backend's configured URL every 5 s. A 2xx response passes. Anything else fails, including a redirect, because the proxy forwards redirects instead of following them. 3 failed probes in a row mark the backend unhealthy. 2 passing probes in a row mark it healthy again.
- **Passive health check** (outlier detection). It watches real requests instead of probing. It keeps the outcomes of the last 10 requests per backend. A failure is a 5xx response or a round trip that failed before any response. 5 failures among those 10 eject the backend, which marks it unhealthy. The window counts requests, not seconds. An ejected backend returns only when an active probe passes, so there is one way in and one way out.
- **Circuit breaker.** One per backend, always in one of three states. *Closed* is normal: requests flow, and the breaker counts failures in a row. Any success resets that count. After 3 failures in a row the circuit becomes *open*: no request goes to that backend. After a cooldown (30 s by default, a setting) the circuit becomes *half-open*. Half-open admits exactly one trial request. If the trial succeeds, the circuit closes. If it fails, the circuit opens again with a fresh cooldown. The move from open to half-open is checked when the state is read. No timer or background goroutine drives it.

**The options.**

- One shared health flag that all three mechanisms write. Rejected: it creates both failures above.
- The circuit breaker as the only authority, with the two health checks feeding it signals. Rejected: it needs a new dependency between packages or a new mediating component.
- Three separate gates combined at selection time. Rejected: more parts, and no difference in behaviour.
- Two independent facts combined when a request is selected.
- For failed requests: retry on another backend, or never retry.

**The choice.**

- **Two facts, combined at read time** ([ADR-0011](adr/0011-health-passive-outlier-and-circuit-breaker-composition.md), decision 1). A backend is eligible when it is healthy and its circuit is not open. Only the two health checks write the health flag. The circuit breaker never does. A half-open backend therefore stays in the pool, and the breaker's own gate enforces "one trial".
- **A two-part gate** ([ADR-0012](adr/0012-circuit-gate-interface-and-backend-state-methods.md), decision 1). One part asks "may this backend be in the pool?" and must not take the trial. The other asks "may this request go?" and does take it. Merging them would let every selection consume the one trial.
- **Every outcome reaches every observer** (ADR-0011, decision 9). An observer is a component that is told how each round trip ended. The three observers are the latency estimate, the outlier detector and the circuit breaker. Each finished round trip is reported to all three, whatever the circuit state. A guard such as "circuit already open, skip recording" looks like a simplification. It is wrong, because this report is the only way a trial's result reaches the breaker.
- **Different postures on purpose** (ADR-0011, decision 8). The circuit breaker is strict and fast: failures must be consecutive. The outlier detector is tolerant and slow: it allows scattered failures.
- **A client that disconnects is not a backend failure** ([ADR-0017](adr/0017-client-gone-classification-and-trial-rearm.md)). A failed round trip falls into one of three tiers: cancelled by a drain, abandoned by the client, or a real transport failure. A client-gone request is recorded as 499, a status code nginx uses for "client closed request", and feeds no observer. If it was the half-open trial, the trial is re-armed so that a cancelled client cannot leave the circuit stuck.
- **Never retry** ([ADR-0018](adr/0018-no-retry-ever.md)). Three reasons carry the decision. A retry would count one client request twice in the in-flight count that least connections and bounded loads read. It would record two failures in the outlier window for one client-visible failure. It would let a failing backend hide behind a retry that succeeded. A retry could not be added later without redesign, because request bodies cannot be replayed and the balancer cannot know whether a request is safe to repeat. A failed round trip is classified, logged, and returned as 502 (transport failure) or 499 (client gone). The client decides what to do next.
- **Transitions are visible** ([ADR-0013](adr/0013-observability-metrics-logging-and-integration.md), [ADR-0014](adr/0014-health-endpoint-contract-and-probe-semantics.md)). Each ejection, reinstatement and circuit change logs one line with a fixed event name. Per-backend gauges (metrics that hold a current value) expose health and circuit state. A separate network port answers `/livez`, `/readyz` and `/startupz`. `/readyz` returns 503 when no backend is selectable, so a balancer placed in front can take this instance out of rotation.

**A worked example.** Two backends misbehave in different ways.

- Backend A returns 500 on every request. Its circuit sees 3 failures in a row and opens at the third request it receives. The outlier window would need 5 failures, so the circuit acts first.
- Backend B returns 500 on every other request. Each success resets the circuit's count, so the circuit never reaches 3 in a row. The outlier window holds 5 failures among its last 10 requests by the tenth request it receives at the latest, and it ejects B.

**The evidence.** One benchmark run stopped one of four backends during steady load: round-robin, HTTP/2, 10 KiB responses, 60 s, with a graceful container stop at 29 s. The generator's target was 1,750 req/s, set at half of the peak that run found. It delivered an average of about 1,210 req/s. 3 requests failed, all within a 9 ms span, and the first came about 0.7 s after the stop command. After that, none failed for the rest of the run. The balancer never retries, so those 3 requests reached their clients as 502. The three fixed values make this consistent with the circuit breaker opening after 3 failures in a row: the outlier detector needs 5 failures, and the active probe needs 3 failed probes 5 s apart. The run did not record a circuit transition. The mechanism is an inference, not an observation.

`RESULTS.md` labels the 9 ms figure with a detection column. That figure is the width of the error window, the last failed request's time minus the first. It is not a delay before the balancer noticed the failure.

**What it costs.**

- The 3 requests that hit the stopped backend failed. Retrying would have hidden them, at the price described above.
- A passively ejected backend waits for two passing probes in a row, 5 s apart by default.
- A backend that receives no traffic during its cooldown is not seen to recover until traffic reaches it (ADR-0011, decision 6).
- A request admitted while the circuit was closed can finish while it is half-open. The breaker cannot tell its outcome from the trial's, so it may settle the trial early (ADR-0012, Consequences).
- The consistent-hash walk can still choose a backend whose circuit is open. That client gets a 503 and is not rerouted (ADR-0012, decision 7).
- The failure thresholds and the outlier window size are fixed values in the code. Only the probe interval, the probe timeout and the cooldown are settings (ADR-0011, decision 10).
- The two health checks and the circuit breaker can disagree about why a backend is out. That is by design.

## 6. An honest benchmark comparison with Nginx

**The problem.** A benchmark number misleads when it hides what was compared. This project's load balancer and Nginx are different programs with different features. The program that sends the test requests (the load generator) runs on the same laptop as both proxies. A figure can then describe the laptop and not the proxy. The reader needs to know what was compared and how far to trust it.

**Terms.**

- A **peak** is the highest request rate a proxy sustained. A rate counts as sustained when the p99 latency stays under 100 ms, fewer than 1% of requests fail, and the generator delivers at least 95% of the requested rate. The benchmark raises the rate until a test fails, then narrows in on the last rate that passed. The result is accurate only to the search's step size.
- A **matched** comparison runs the same algorithm on both sides. Round-robin and least connections are matched. A **nearest-equivalent** comparison uses the closest Nginx setting when Nginx has no identical algorithm. Consistent hashing is compared with Nginx's hash, which has no bounded loads. Power of two choices is compared with Nginx's two-choice setting, which looks at active connections and has no latency signal.
- A peak is **rig-limited** when it reflects the load generator or the test machine instead of the proxy. A rig-limited peak is a lower bound on what the proxy can do.

**The options.**

- Load generator: wrk (another load-generation tool) for throughput and vegeta for latency; wrk for HTTP/1.1 and vegeta for HTTP/2; a generator written for the project; or vegeta alone.
- Nginx setup: Nginx tuned to its best, or a constrained setup that matches the load balancer.
- Generator limits: give the Docker virtual machine more memory, drop the 1 MiB responses, or bound the generator and check what it delivered.

**The choice.**

- **One tool, vegeta** ([ADR-0020](adr/0020-benchmark-tool-vegeta-over-wrk.md)). wrk cannot speak HTTP/2, and two tools would leave every HTTP/1.1 versus HTTP/2 difference open to a second explanation: the tool. Vegeta sends at a constant rate. When the server slows down, it keeps its schedule and measures latency from the planned send time. A tool that waits for each reply would under-report queueing. Vegeta negotiates HTTP/2 through ALPN, the step in the TLS handshake where client and server agree on a protocol.
- **A constrained match, not Nginx at its best.** Both proxies get the same algorithm, backend count, topology and connection pool, on the same container network. A gap then points at the implementation and not at tuning.
- **A pinned rig.** The machine is an Apple M3 with 8 virtual CPUs and 8 GiB assigned to Docker. The load balancer and Nginx each get two pinned cores. Each backend gets one core. The generator gets two. The cores sit inside a virtual machine, so the throughput ceiling is soft.
- **A bounded generator that checks its own delivery** ([ADR-0022](adr/0022-honest-benchmarking-under-generator-limits.md), amending [ADR-0021](adr/0021-bounded-memory-in-benchmark-harness.md)). Capping the generator's workers stopped the memory failure. It also let the generator quietly send less than asked. A step now counts only if it delivered at least 95% of its target. The search starts from a rate chosen per response size. "No sustainable rate on this rig" is a valid result and is reported as such.

**A worked reading.** At 1 MiB, Nginx's peak is 350 req/s and the load balancer's is 275 req/s. The gap is 75 req/s. The search step is 25 req/s, so the gap is three steps. At 200 B the gap is 3,500 req/s and the step is 500 req/s, which is seven steps. A gap smaller than one step could not be told apart from a tie.

**The evidence.**

- **One run, incomplete.** The published results come from one uninterrupted run from a clean checkout of commit `8c2b7d4`. It produced 67 of 71 result sets and was flagged noisy. Two scenarios were skipped: round-robin through the load balancer over HTTP/2, at 200 B and at 10 KiB responses.
- **The Nginx comparison, over HTTP/1.1.** In the matched round-robin comparison over HTTP/1.1, Nginx's peak is higher at all three response sizes. That is an ordering from a single run. It states no ratio. The same comparison over HTTP/2 at 200 B and 10 KiB is absent, because the load-balancer scenarios were skipped.

| Response size (HTTP/1.1) | Nginx peak (req/s) | Load balancer peak (req/s) | Gap (req/s) | Search step (req/s) |
| --- | --- | --- | --- | --- |
| 200 B (bytes) | 11,500 | 8,000 | 3,500 | 500 |
| 10 KiB | 6,000 | 4,500 | 1,500 | 500 |
| 1 MiB | 350 | 275 | 75 | 25 |

  No round-robin comparison with small responses exists over HTTP/2, so the table above is labelled HTTP/1.1 and stands alone. Every gap is larger than its step.
- **Hot-key behaviour** is reported in topic 2. It belongs to the consistent-hash comparison and not to a throughput claim.
- **The degraded-backend scenario is unreliable.** It slows one backend to see which algorithms steer around it. Every median sat near the injected 50 ms, which is consistent with the delay reaching all four backends. The suspected cause, an exported environment variable leaking through a shared compose definition, is **UNVERIFIED**. This document quotes no numbers from that scenario.
- **Deliberately not quoted:** the peaks from the HTTP/2 matrix and any comparison of tail latency (the latency of the slowest few percent of requests). The results document itself calls its HTTP/2 round-robin figures the least trustworthy in the matrix. See its [limitations section](../RESULTS.md#limitations-and-caveats).

**What it costs.**

- The claim is an ordering and nothing more. It does not say how much faster either proxy is, and it does not say which is faster on other hardware.
- Peaks for large responses are rig-limited. Read them as lower bounds.
- The ceiling is soft, and the run is one run. It was not repeated.
- A constrained Nginx is not the fastest Nginx. A nearest-equivalent result partly measures a feature, such as the bounded-loads check, and not raw speed.
- The generator's heap cap trades some generator speed for memory. It applies equally to both proxies, so it does not favour either.
- The two skipped scenarios leave the HTTP/2 round-robin comparison at small sizes empty.

## 7. Concurrency model

**The problem.** Many requests run at once, each in its own goroutine. They share state: a count of requests in flight per backend, a health flag, the list of backends, the circuit state. A *data race* occurs when two goroutines touch the same memory, at least one writes, and nothing orders them. The result is silent corruption. The balancer needs a rule for each piece of shared state, one the code and a reviewer can check.

**Terms.**

- An **atomic operation** is a read or write the CPU performs as one indivisible step. No other goroutine sees it half done.
- **Compare-and-swap** (CAS) writes a new value only if the current value is still the one the caller read. If another goroutine changed it first, the write fails and the caller retries or stops.
- A **mutex** is a lock. Only one goroutine holds it at a time and the others wait.
- A **channel** is a typed queue that goroutines use to pass values or signals to each other.
- **Context cancellation** is a "stop" signal that Go code can wait on or register a callback for.
- The **race detector** is a Go tool that reports data races while tests run. `make test-race` runs the whole suite under it.

**The options.**

- One lock around all shared state. Simple, but every request waits on it.
- A lock per structure. Fewer waits, but each lock needs its own rules and its own review.
- Atomics for single values and immutable snapshots for groups of values that change together.
- A single owner goroutine that other goroutines message. Correct, but it adds a hop and a queue to every request.

**The choice.** Every piece of shared state has one named owner who writes it and one named mechanism. The ownership table in the [frozen contracts](design/sprint-1-contracts.md#concurrency-ownership-table) lists them. The mechanism follows the shape of the data:

- A single number or flag read on the request path is an atomic.
- Several values that must change together are one immutable snapshot, replaced whole through an atomic pointer.
- A small structure that is away from the code every request runs, or that is not a single word, sits behind a mutex held for a few instructions and never while waiting on the network or disk.
- Waiting on or signalling another goroutine uses a channel or a context.

Each primitive below was found by searching the non-test code and is cited to a file and line. The search found no `sync.RWMutex`, `sync.WaitGroup` or `errgroup` there, and this document claims none.

**Atomics and compare-and-swap.**

| Primitive | Use site | Why this and not the alternative |
| --- | --- | --- |
| Atomic counter, round-robin index | `internal/balancer/roundrobin.go:23` | The algorithm needs only a rotating index, so there is no lock on the code every request runs ([index entry](adr/INDEX.md)). |
| Atomic counter, requests in flight | `internal/backend/backend.go:118`, incremented at `internal/proxy/proxy.go:93` | Many request goroutines write it. Least connections and bounded loads read it. A lock would serialise every dispatch. The matching metrics gauge changes at the same call sites, so it cannot drift from the count ([ADR-0013](adr/0013-observability-metrics-logging-and-integration.md), decision 7). |
| Atomic flags, healthy and removed | `internal/backend/backend.go:117` and `:121` | One writer class per flag and many readers. The circuit breaker never writes `healthy` (ADR-0011, decision 1). |
| Atomic pointer to a snapshot of the backend list | `internal/backend/registry.go:52` | Readers load once and never lock. A reload builds a new snapshot and swaps it ([ADR-0015](adr/0015-reload-architecture.md), decision 5). |
| Atomic pointer to the loaded config | `internal/app/app.go:69` | It holds the last applied config. The reload replaces it last (ADR-0015). |
| CAS loop, latency estimate | `internal/backend/backend.go:248`, in `RecordLatency` | The update reads the old estimate and writes a blend. A CAS loop keeps the read path P2C depends on free of locks ([ADR-0010](adr/0010-p2c-ewma-backend-latency-state-cold-start-and-failure-penalty.md)). |
| CAS, circuit state | `internal/backend/backend.go:286`, `:345`, `:375`, `:403`, `:436`, `:442`, `:449`, on the pointer at `:120` | The state is one immutable snapshot replaced as a unit. A separate timestamp field would race the transition. A mutex was rejected ([ADR-0012](adr/0012-circuit-gate-interface-and-backend-state-methods.md), decision 3 and its alternatives). |
| CAS, cached hash ring | `internal/balancer/consistent_hash.go:88`, on the pointer at `:44` | The ring is rebuilt only when the backend list's version changes. Concurrent rebuilds right after a swap are correct, and the loser's CAS fails (ADR-0015, decision 9). |

The round-robin counter and the least-connections scan are index entries in the [ADR index](adr/INDEX.md). Least connections scans every healthy backend on each selection and uses no priority queue (a structure that keeps items sorted by rank). It needs no lock of its own, because it only reads the atomic in-flight counts.

**Mutexes.**

| Primitive | Use site | Why this and not the alternative |
| --- | --- | --- |
| Mutex over the outlier windows | `internal/health/outlier.go:67` | Each window holds ten outcomes and a failure count that change together. The comment on the type sets the rule: held only for the window update and one atomic store, never while waiting on the network or disk. One mutex covers all backends' windows. |
| Mutex over the running probers | `internal/health/checker.go:83` | It keeps a concurrent add and remove from corrupting the map. Both run off the request path, so the lock is not contended by requests. |

**Exactly-once, contexts and channels.**

| Primitive | Use site | Why this and not the alternative |
| --- | --- | --- |
| `sync.Once` around the in-flight release | `internal/proxy/proxy.go:74`, used at `:102` | The decrement must happen exactly once however the response ends: the body closing or the round trip failing ([ADR-0007](adr/0007-proxy-request-lifecycle-and-exactly-once-decrement.md)). |
| Context with cause, joined by `context.AfterFunc` | `internal/proxy/proxy.go:304` and `:307`; created at `internal/backend/registry.go:93` | A removed backend's context is cancelled when its drain window ends. That cancels its in-flight requests. `AfterFunc` registers the callback with no goroutine while the request is live, so there is no goroutine per request and no registry of cancel functions ([ADR-0016](adr/0016-drain-lifecycle.md), decision 3). |
| Channel of depth 1 for SIGHUP | `cmd/l7LoadBalancer/main.go:206`, buffer at `:33` to `:38` | A burst of signals collapses into one pending reload (ADR-0015, decision 1). |
| Channel of depth 3 for server errors | `internal/app/app.go:294` | One slot per server, so no serving goroutine blocks. `Run` is the only reader and owns shutdown. |
| Channel closed when a prober exits | `internal/health/checker.go:174`, closed at `:182`, awaited at `:200` | `Remove` waits so that a removed backend's probe cannot log or update its metrics after they are deleted. |
| Timer polling the in-flight count | `internal/app/drain.go:73` | A drain goroutine checks every 100 ms until the removed backend's in-flight count reaches zero (ADR-0016, decision 7). |

On goroutine cost: ADR-0016's rejected alternative is a goroutine per in-flight request waiting on a done channel. That is the cost its chosen design avoids. The chosen design uses no goroutine per request while the request lives. The two statements are consistent.

**What it costs.**

- Method-only access to atomics is a convention. Go cannot check which package calls a method, so rules such as "only active checks call `MarkHealthy`" rest on review (ADR-0011, decision 2).
- The proxy's per-request state has no lock. Its safety argument is that `ReverseProxy` handles one request on one goroutine. That argument is implicit, so the race detector carries the checking (ADR-0007, Consequences).
- CAS loops can retry under contention, and each circuit transition allocates a new snapshot.
- The outlier detector's single mutex is shared by all backends' windows. Its critical section is a few instructions.
- The proxy cannot tell a stale request's outcome from the half-open trial's. See topic 5.
- The checking is tests under the race detector, plus one soak run. The soak ran for one hour on 2026-09-27 and is recorded in the tracking file. No output was kept. It ran inside one test process against stub backends (fake backends): 59.66 M requests with 12 reloads, 7,175 cancellations and 2,789 failures. Goroutines went from 35 to 30. The heap (the memory the program has allocated) after garbage collection, Go's automatic memory reclaim, went from 930 KB to 804 KB. Do not read those request counts beside the benchmark figures. The benchmark drives real containers.

## Decisions not covered here

This document covers seven decisions. The [ADR index](adr/INDEX.md) lists the rest. Among them are the interface and schema freeze, the scope of "production-grade", the health endpoint, the deployment target, the local demo, and decisions recorded in the index itself with no standalone ADR.
