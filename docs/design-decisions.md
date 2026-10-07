# Design decisions

Each section gives one major decision, the reason for it, the trade-off, and the ADRs that hold the full detail. The [ADR index](adr/INDEX.md) lists all decisions. Numbers come from [`RESULTS.md`](../RESULTS.md), the tests, fixed values in the code, or the ADRs.

## 1. Use `httputil.ReverseProxy`

**Decision.** The proxy uses the standard library's `net/http/httputil.ReverseProxy`. It does not use a framework or a hand-built proxy.

**Reason.** `ReverseProxy` removes hop-by-hop headers, sets `X-Forwarded-For`, and streams and flushes bodies. Rewriting these chores adds code but no decision worth defending. A framework would hide the request lifecycle. The project does its own work in backend selection, failure handling and reload.

The proxy handler calls the selector before it gives the request to `ReverseProxy`. A hook inside `ReverseProxy` has no response writer, so it cannot answer "503, no healthy backend". The in-flight count of a backend goes up at dispatch. A `sync.Once` makes it go down exactly once, when the body closes or when the round trip fails.

**Trade-off.**

- The request path is not standard-library-only. Non-test code uses `golang.org/x/net` (h2c), `github.com/prometheus/client_golang` (metrics) and `gopkg.in/yaml.v3` (config).
- The standard library supports h2c only with the prior-knowledge preface. The HTTP/1.1 `Upgrade` form needs `x/net/http2/h2c`. That package is deprecated, but it is the only implementation of both forms.
- The project inherits the limits of `ReverseProxy`. For example, the proxy does not join a path prefix in a backend URL.
- "Production-grade" means defensible patterns only. It excludes security hardening, capacity planning and operations.

**ADRs.** [ADR-0007](adr/0007-proxy-request-lifecycle-and-exactly-once-decrement.md), [ADR-0005](adr/0005-scope-of-production-grade.md), and the [index entries](adr/INDEX.md#decisions-without-a-dedicated-adr) for `ReverseProxy` and h2c.

## 2. Consistent hashing with bounded loads

**Decision.** The consistent-hash algorithm uses bounded loads ("Consistent Hashing with Bounded Loads", 2016). The key is the client IP without the port. Each backend has 150 virtual nodes on the ring. Keys are hashed with FNV-1a-64 and then a Murmur3-style `fmix64` finish. A backend is admitted when its in-flight count is at most:

```
capacity = max(1, ceil(average_in_flight × 1.25))
```

The average runs over the eligible backends and does not include the incoming request. If the owning backend is over capacity, the walk continues clockwise to the next backend within capacity.

**Reason.** Consistent hashing gives session affinity. When a backend joins or leaves, only about `1/n` of keys move. Plain consistent hashing knows nothing about load, so one hot key overloads one backend. Bounded loads keeps affinity and moves only the excess. A ring rebuild on overload would reshuffle every key. Least connections would drop affinity.

The `fmix64` finish matters. Without it, 256 consecutive IP addresses landed on only 3 of 4 backends. With it, they landed on all 4.

**Evidence.**

- With 10,000 Zipf-skewed requests from 100 clients to 4 backends (seed 2253), plain hashing put 40.0% on the busiest backend. Bounded loads put 31.3% (3,126 requests, against a cap of 3,125). Over 60 seeds, plain hashing gave 29.9% to 51.3% (median 40.0%). Bounded loads gave 30.0% to 31.3%.
- In the benchmark, the load generator has one address, so every request is a hot key. At 10 KiB and peak load, the bounded ring put 82.27% on the owner and 17.19% on a second backend. Nginx's hash put 99.96% on its owner. At 1 MiB and 30% of peak, the ring did not spill (owner 97.74%), because too few requests were in flight for the cap to bind.

**Trade-off.**

- The load signal is the in-flight count. It sees concurrency, not latency. A slow backend shows only when requests pile up.
- The cap binds only when one backend is more than 25% above the average.
- Behind another proxy, the client IP is the proxy's IP.
- The cap is a ceiling, not a rebalancer. It spills a minority of a hot key.

**ADRs.** [ADR-0008](adr/0008-consistent-hash-ring-pipeline-and-vnode-layout.md), [ADR-0009](adr/0009-consistent-hash-bounded-loads-capacity-and-evidence.md).

## 3. Power of two choices with EWMA latency

**Decision.** The P2C algorithm picks two backends at random and sends the request to the one with the lower latency estimate. Each backend keeps an EWMA of its round-trip time:

```
new_estimate = 0.1 × observed + 0.9 × old_estimate
```

The measured time runs from just before dispatch to the arrival of the response headers. The first sample is stored as it is. A backend with no sample reads zero, so it wins its next comparison and gets a real sample. A round trip that fails before any response records 2 s.

**Reason.** A backend can be up and slow. Least connections does not see this until requests queue. A full scan for the lowest latency sends every concurrent request to the same backend, and the traffic swings. Two random choices spread the load and still avoid the worst backend. The 2 s penalty stops a backend that fails instantly from looking fast.

**Example.** With one backend at 10 ms and three at 500 ms, the fast backend is in 3 of the 6 pairs and wins each time. It gets about 50% of requests and each slow backend gets about 16.7%. A test of 4,000 selections gave 51.1%, 16.8%, 16.2% and 16.0%.

**Trade-off.**

- The estimate does not decay. A backend with the highest estimate loses every comparison, gets no traffic, and so never gets the sample that would correct it.
- Active health probes do not feed the estimate. Only proxied responses do.
- A client that cancels a request still records the 2 s penalty.
- The weight 0.1 and the 2 s penalty are constants, not settings.
- The benchmark has no reliable evidence for P2C against a slow backend (see section 6).

**ADRs.** [ADR-0010](adr/0010-p2c-ewma-backend-latency-state-cold-start-and-failure-penalty.md).

## 4. Reload by an in-process snapshot swap

**Decision.** A reload swaps the backend set inside the running process. `SIGHUP` triggers it. `POST /admin/reload` with a YAML body on the metrics listener also triggers it. One mutex serializes both triggers, and `GET /admin/reload/last` shows the last outcome.

- The registry holds one atomic pointer to an immutable snapshot: a version number and the ordered backend list. Readers load it once and never lock.
- A backend's identity is its name and URL together. A new URL under an old name is one removal and one addition.
- Unchanged backends keep the same object and all their state. Added backends start unhealthy and join after one passing probe. At process start, backends start healthy.
- The backend list and the algorithm are reloadable. Any other change rejects the whole reload and names the fields.
- A new algorithm is built first and then swapped in with one atomic store. A request in flight keeps its backend. The new algorithm starts warm, because the counts and latency estimates live on the backends.
- Removed backends are marked removed before the swap, so they report nothing to the failure detectors or metrics.

A removed backend drains. One goroutine per removed backend checks its in-flight count every 100 ms. If the count does not reach zero within `reload.drain_window` (default 30 s), the backend's context is cancelled. A request still waiting for headers gets a 502 with reason `window_expired`. A request already streaming its body is cut short.

**Reason.** A restart drops in-flight requests. A second process with `SO_REUSEPORT` cannot keep the health, circuit and latency state of unchanged backends. Starting added backends unhealthy means that a typo in a new URL cannot send errors to clients. A clear refusal is better than a silent no-op.

The deployment target is a Docker container with the binary as PID 1, so it gets signals directly. A reload is `docker kill -s HUP <container>`.

**Evidence.** One run each, round-robin, HTTP/2 over h2c, 10 KiB, 60 s, reload at 29 s:

| Run | Errors | p99 before | p99 after |
| --- | --- | --- | --- |
| No-op reload | 0 non-2xx, 0 transport errors | 1.752 ms | 1.762 ms |
| Drain reload (one backend removed) | 0 non-2xx, 0 transport errors | 1.856 ms | 2.140 ms |

The generator delivered about 1,121 and 1,137 req/s against a 1,750 req/s target. The removed backend received no request after the reload applied.

**Trade-off.**

- A reload that replaces every backend leaves a short window with no eligible backend. Requests get 503 in that window, and the log marks it at WARN.
- A change to the listen address or the timeouts needs a restart, which drops in-flight requests. There is no socket handoff.
- The drain cuts a response that is still streaming when the window ends.
- The admin endpoint has no authentication. It is not a production control plane.

**ADRs.** [ADR-0015](adr/0015-reload-architecture.md), [ADR-0016](adr/0016-drain-lifecycle.md), [ADR-0019](adr/0019-deployment-target.md).

## 5. Health checks and circuit breaker as separate facts

**Decision.** Three mechanisms can take a backend out of traffic:

- **Active health check.** A GET to the backend URL every 5 s. Only 2xx passes. 3 failures in a row mark the backend unhealthy. 2 passes in a row mark it healthy.
- **Outlier detection.** A window of the last 10 requests per backend. A failure is a 5xx or a failed round trip. 5 failures in the window eject the backend. Only an active probe brings it back.
- **Circuit breaker.** 3 failures in a row open the circuit. After the cooldown (default 30 s) it goes half-open and admits one trial request. Success closes it. Failure opens it again. The open-to-half-open step happens when the state is read, with no timer.

A backend is eligible when it is healthy and its circuit is not open. Only the two health checks write the health flag. The circuit breaker never writes it. The gate has two parts: one asks "may this backend be in the pool?" and does not take the trial, and one asks "may this request go?" and does take it. Every finished round trip goes to all three observers (latency estimate, outlier detector, circuit breaker), whatever the circuit state.

A client that disconnects is not a backend failure. The proxy records it as 499 and reports it to no observer. If it held the half-open trial, the trial is re-armed. The proxy never retries. A failed round trip returns 502 (transport failure) or 499 (client gone).

**Reason.** If the circuit breaker cleared the health flag, a tripped backend would leave the pool and never get its trial request. If three mechanisms wrote one flag, the last writer would win. The circuit breaker is strict and fast, and the outlier detector is tolerant and slow, so they catch different failures. For example, a backend that always returns 500 trips the circuit at its third request. A backend that fails every other request never trips the circuit, but the outlier window ejects it.

The proxy does not retry for three reasons. A retry counts one client request twice in the in-flight count. It records two failures in the outlier window for one client-visible failure. It hides a failing backend behind a success. Also, the proxy cannot replay a body or know whether a request is safe to repeat.

Transitions are visible. Each ejection, reinstatement and circuit change logs one line with a fixed event name and updates a per-backend gauge. A separate listener serves `/livez`, `/readyz` and `/startupz`. `/readyz` returns 503 when no backend is eligible.

**Evidence.** One run stopped one of four backends at 29 s under round-robin, HTTP/2, 10 KiB load. 3 requests failed, all within 9 ms, starting about 0.7 s after the stop. None failed after that. The count of 3 is consistent with the circuit opening after 3 failures in a row. The run did not record the circuit transition, so this is an inference.

**Trade-off.**

- Without retry, those 3 requests reached the client as 502.
- An ejected backend waits for two passing probes, 5 s apart by default.
- A backend that gets no traffic during the cooldown does not show recovery until traffic reaches it.
- A request admitted while the circuit was closed can finish while it is half-open. The breaker cannot tell it from the trial.
- The consistent-hash walk can choose a backend with an open circuit. That client gets 503 and is not rerouted.
- The failure thresholds and the window size are constants. Only the probe interval, probe timeout and cooldown are settings.

**ADRs.** [ADR-0011](adr/0011-health-passive-outlier-and-circuit-breaker-composition.md), [ADR-0012](adr/0012-circuit-gate-interface-and-backend-state-methods.md), [ADR-0013](adr/0013-observability-metrics-logging-and-integration.md), [ADR-0014](adr/0014-health-endpoint-contract-and-probe-semantics.md), [ADR-0017](adr/0017-client-gone-classification-and-trial-rearm.md), [ADR-0018](adr/0018-no-retry-ever.md).

## 6. An honest benchmark against Nginx

**Decision.**

- Use vegeta as the only load generator.
- Run a constrained Nginx with the same algorithm, backend count, topology and connection pool. Do not tune Nginx to its best.
- Pin cores on one machine: an Apple M3 with 8 vCPUs and 8 GiB for Docker. The load balancer and Nginx get two cores each, each backend gets one, and the generator gets two.
- Cap the generator's memory and workers. Count a step only if it delivered at least 95% of its target rate.

A peak is the highest rate where p99 stays under 100 ms, fewer than 1% of requests fail, and the 95% delivery rule holds. Round-robin and least connections are matched comparisons. Consistent hashing and P2C are nearest-equivalent, because Nginx has no bounded loads and no latency signal.

**Reason.** wrk has no HTTP/2. Two tools would give every HTTP/1.1 versus HTTP/2 difference a second cause. Vegeta sends at a constant rate and measures from the planned send time, so it does not hide queueing. A matched Nginx means a gap points at the implementation, not at tuning. The memory cap stopped the 1 MiB runs from failing. The delivery check stops a slow generator from passing as a proxy peak. "No sustainable rate on this rig" is a valid result.

**Evidence.** One run from commit `8c2b7d4` produced 67 of 71 result sets and was flagged noisy. Matched round-robin over HTTP/1.1:

| Response size | Nginx peak (req/s) | Load balancer peak (req/s) | Gap | Search step |
| --- | --- | --- | --- | --- |
| 200 B | 11,500 | 8,000 | 3,500 | 500 |
| 10 KiB | 6,000 | 4,500 | 1,500 | 500 |
| 1 MiB | 350 | 275 | 75 | 25 |

Nginx is ahead at all three sizes, and every gap is larger than one search step. The HTTP/2 round-robin runs at 200 B and 10 KiB through the load balancer were skipped. The HTTP/2 peaks and tail-latency comparisons are not quoted. The degraded-backend scenario is unreliable: every median was near the injected 50 ms, which suggests the delay reached all four backends. The suspected cause is an environment variable that leaks through a shared Compose definition. This cause is not verified.

**Trade-off.**

- The claim is an ordering, not a ratio, on one machine, from one run.
- Large-response peaks are rig-limited and are lower bounds.
- The cores sit inside a virtual machine, so the ceiling is soft.
- A constrained Nginx is not the fastest Nginx.

**ADRs.** [ADR-0020](adr/0020-benchmark-tool-vegeta-over-wrk.md), [ADR-0021](adr/0021-bounded-memory-in-benchmark-harness.md), [ADR-0022](adr/0022-honest-benchmarking-under-generator-limits.md). See also the [limitations in RESULTS.md](../RESULTS.md#limitations-and-caveats).

## 7. Concurrency: one owner and one mechanism per piece of state

**Decision.** Each piece of shared state has one named writer and one mechanism. The mechanism follows the shape of the data:

| State | Mechanism | File |
| --- | --- | --- |
| Round-robin index | atomic counter | `internal/balancer/roundrobin.go` |
| In-flight count per backend | atomic counter | `internal/backend/backend.go`, incremented in `internal/proxy/proxy.go` |
| Healthy and removed flags | atomic flags | `internal/backend/backend.go` |
| Backend list | atomic pointer to an immutable snapshot | `internal/backend/registry.go` |
| Loaded config | atomic pointer | `internal/app/app.go` |
| Latency estimate | CAS loop in `RecordLatency` | `internal/backend/backend.go` |
| Circuit state | CAS on one immutable snapshot | `internal/backend/backend.go` |
| Cached hash ring | CAS, rebuilt when the snapshot version changes | `internal/balancer/consistent_hash.go` |
| Outlier windows | one mutex, held only for the window update | `internal/health/outlier.go` |
| Running probers | mutex, off the request path | `internal/health/checker.go` |
| In-flight release | `sync.Once` | `internal/proxy/proxy.go` |
| Drain cancel | context with cause, joined by `context.AfterFunc` | `internal/proxy/proxy.go`, `internal/backend/registry.go` |
| `SIGHUP` | channel of depth 1, so a burst collapses into one reload | `cmd/l7LoadBalancer/main.go` |
| Server errors | channel of depth 3, one slot per server | `internal/app/app.go` |
| Prober exit | channel closed on exit and awaited by `Remove` | `internal/health/checker.go` |

Least connections scans every healthy backend on each selection. It reads only the atomic counts, so it needs no lock or heap.

**Reason.** One global lock makes every request wait. A single owner goroutine adds a hop and a queue to every request. Atomics keep the request path free of locks. Snapshots keep values that change together consistent. Mutexes guard only small structures, and never while waiting on the network or disk. `context.AfterFunc` cancels a drained backend's requests without a goroutine per request.

**Trade-off.**

- Rules such as "only active checks call `MarkHealthy`" are conventions. Go cannot enforce them, so they rest on review.
- The proxy's per-request state has no lock. It relies on `ReverseProxy` handling one request on one goroutine. The race detector checks this.
- CAS loops can retry under contention, and each circuit transition allocates a new snapshot.
- The checking is `make test-race` plus one one-hour soak run inside a test process against stub backends. The soak sent 59.66 M requests with 12 reloads, 7,175 cancellations and 2,789 failures. Goroutines went from 35 to 30, and the post-GC heap went from 930 KB to 804 KB. No output from the soak was kept.

**ADRs.** [ADR-0007](adr/0007-proxy-request-lifecycle-and-exactly-once-decrement.md), [ADR-0010](adr/0010-p2c-ewma-backend-latency-state-cold-start-and-failure-penalty.md), [ADR-0011](adr/0011-health-passive-outlier-and-circuit-breaker-composition.md), [ADR-0012](adr/0012-circuit-gate-interface-and-backend-state-methods.md), [ADR-0015](adr/0015-reload-architecture.md), [ADR-0016](adr/0016-drain-lifecycle.md).
