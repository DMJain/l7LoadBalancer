# What the project would do differently

This document lists the weaknesses the project knows about. Each item has four parts: what happened, what it cost, what the project would do instead, and a source that can be checked. The sources are decision records (ADRs), entries in the [tracking file](../PROGRESS.md), the [benchmark results](../RESULTS.md), and commit hashes. Most items below are recorded as proposals that were not built. The items are not ranked.

Two terms are used throughout. A **load balancer** is a program that accepts requests and forwards each one to one of several backend servers. A **backend** is one of those servers. The [design decisions](design-decisions.md) document explains how this project's load balancer works.

## 1. The latency estimate never decays

**What happened.** One selection policy, power of two choices (P2C), picks two backends at random and sends the request to the one with the lower latency estimate. The estimate is an **EWMA** (exponentially weighted moving average): a running average that gives recent requests more weight than old ones. The estimate changes only when a request to that backend finishes. Nothing lowers it while the backend gets no traffic. That absence is called a missing **decay**. A backend that was slow once can hold a high estimate, lose every comparison, receive no requests, and so never get the samples that would correct the estimate.

**What it cost.** A backend that was slow at 500 ms and later recovers to 10 ms keeps its 500 ms estimate until it wins a comparison. It wins only when it is paired with a backend whose estimate is higher. The policy can therefore starve a healthy backend. The design-decisions document states this limit in its topic on P2C. The local live demo script must not promise a recovery that the policy does not perform.

**What the project would do instead.** Let an estimate drift back toward a neutral value when the backend has had no samples for some time, or occasionally send a probe request to a backend with an old estimate. Either change needs its own decision record and tests.

**Source.** [ADR-0010](adr/0010-p2c-ewma-backend-latency-state-cold-start-and-failure-penalty.md) (where the estimate is stored and updated) and [ADR-0023](adr/0023-local-live-demo.md), decision 5 and the "Negative" consequence about decay.

## 2. The benchmark has no guard for a cold first step

**What happened.** The benchmark compares the load balancer with Nginx, a web server and proxy. It finds each proxy's **peak**: the highest request rate, in requests per second (req/s), that the proxy sustained. The harness starts at a seed rate (the first rate tried) and raises it step by step until a step fails. A step fails if the 99th-percentile latency (the time within which 99 of every 100 requests finish) is too high, if too many requests fail, or if the load generator (the program that sends the test requests) does not deliver the requested rate. A **cold** container is one that has just started and has not yet handled traffic. A cold container is slower than a warmed one. The first step of each search runs right after a container starts, with no warm-up and no retry.

The published run produced 67 of 71 result sets. It skipped two scenarios: round-robin (backends take requests in turn) through the load balancer over HTTP/2 (the newer HTTP version, which sends many requests at once over one connection) at 200 B and at 10 KiB responses. Both failed the 99th-percentile check at the seed rate. Every other policy passed its 200 B and 10 KiB scenarios at much higher rates later in the run. The skips therefore read as a cold start, not as a real ceiling.

**What it cost.** The results have no HTTP/2 round-robin comparison against Nginx at those two response sizes. The HTTP/1.1 round-robin comparison covers all three response sizes, and any text that cites it must name the missing HTTP/2 comparison in the same sentence. The tracking file also records that this gap decides whether to re-run the whole published matrix.

**What the project would do instead.** Retry the first step once, or send a short warm-up burst right after each container start, before accepting a result of "no sustainable rate". The change touches the harness, so it needs its own decision record and tests.

**Source.** The tracking-file entry "Cold-start guard for the first peak-search step" in [PROGRESS.md](../PROGRESS.md), and the first bullet of [Limitations and caveats](../RESULTS.md#limitations-and-caveats).

## 3. The degraded-backend scenario did not isolate the slow backend

**What happened.** The degraded scenario was meant to slow down one of four backends and show which policies move traffic away from it. Every median latency (the middle value, where half the requests were faster) in the results sat near the injected delay, which is consistent with the delay reaching all four backends. Least-connections (send each request to the backend with the fewest requests in flight) and P2C did not shift traffic away from any backend, and the expected difference between policies did not appear.

The suspected cause is **UNVERIFIED**. The hypothesis is that the benchmark script exports an environment variable for the delay, that all four backends read it from one shared definition in the Docker Compose file (the file that lists the containers), and that a later restart of the stack therefore recreated all four backends with the delay. Nobody has diagnosed this.

**What it cost.** The scenario produced no evidence about how P2C behaves against a slow backend. The results document records the scenario as a failure to isolate the backend, and no figure from it is quoted in the README or in the design-decisions document. The unreliable scenario is also why no live evidence of the no-decay limit in item 1 exists.

**What the project would do instead.** Diagnose the leak first. Give the slow backend its own definition in that file so that nothing shared can reach the others. Check each backend's own median before trusting the scenario. Only then re-run the published matrix.

**Source.** The tracking-file entry on the degraded-scenario delay leaking to every backend, in [PROGRESS.md](../PROGRESS.md), and the [Degraded backend](../RESULTS.md#degraded-backend) section of the results.

## 4. The published numbers are rig-limited

**What happened.** The benchmark ran on one laptop. The machine is an Apple M3 with 8 vCPUs (virtual CPU cores) and 8 GiB assigned to Docker. The load balancer and Nginx each get two pinned cores (cores reserved for that program alone), each backend gets one core, and the load generator gets two cores. A **rig-limited** number is a measured peak that reflects the limits of the load generator or the test machine, not of the proxy. For larger responses the generator cannot send requests fast enough to reach the proxy's real ceiling. The harness refuses steps that the generator under-delivered, but a number that passes is still a lower bound on what the proxy can do.

The cores also sit inside a virtual machine that shares the host's physical cores through a hypervisor. Pinning removes contention inside the virtual machine and keeps the comparison fair. It cannot give a true core. The absolute throughput ceiling is therefore soft.

**What it cost.** The results support only an ordinal claim, which says which side is ahead and not by how much: over HTTP/1.1 (the older HTTP version, one request at a time per connection), with round-robin, Nginx is ahead at all three response sizes. They do not support a ratio. The HTTP/2 peaks are not quoted, because that run was flagged noisy.

**What the project would do instead.** Run the load generator on a separate machine from the system under test, on hardware without a virtual machine in between, and repeat each scenario so that run-to-run spread is measured and not guessed.

**Source.** [ADR-0022](adr/0022-honest-benchmarking-under-generator-limits.md), and the "ceiling is soft" and "load generator is the limit" bullets of [Limitations and caveats](../RESULTS.md#limitations-and-caveats).

## 5. P2C state is stale after a switch between load balancers

**What happened.** The local live demo runs four load balancers, one per selection policy, and the viewer can point traffic at any of them. P2C estimates live inside each load balancer. A load balancer that has never carried traffic starts with every estimate at zero, which reads as "fastest possible". A load balancer that carried traffic earlier keeps the estimates from when it last did, because nothing updates them while it is idle.

**What it cost.** After a switch, the distribution of requests across backends does not reflect current latencies until the estimates converge. The demo script has to allow a convergence period after every switch, before it makes any claim about the distribution.

**What the project would do instead.** Make the estimate age, which is the fix for item 1, or reset the estimates when a load balancer goes from idle to active. The demo would then need no convergence period.

**Source.** [ADR-0023](adr/0023-local-live-demo.md), decision 5.

## 6. The "Time to detection" column measures the wrong thing

**What happened.** The failure scenario stops one of four backends 29 seconds into a 60-second run. The results table has a column named "Time to detection". The harness computes it as the time of the last failed request minus the time of the first. In the published run that value is 0.009 s, which is the width of the **error window**, the span in which failures occurred. It is not the delay between the failure and its detection. In the same run the first failed request came 0.73 s after the stop command, at 29.734 s.

In total 3 requests failed, all within the 9 ms window. The load balancer never retries (ADR-0018), so all 3 reached the client. A circuit breaker is a per-backend switch that stops sending requests to a backend after repeated failures. It opening after three consecutive failures is consistent with that count ([ADR-0012](adr/0012-circuit-gate-interface-and-backend-state-methods.md), decision 6), but the run did not record a state change, so the mechanism is an inference.

**What it cost.** A reader who takes the column at face value concludes that the load balancer noticed the failure within 9 ms. The evidence supports no such conclusion. Until the column is fixed, the README says where it links the results that the column measures the error-window width.

**What the project would do instead.** Rename the column to the error-window width, and add a separate column for the delay between the stop command and the first failed request. The change touches the benchmark script and the results generator, and it needs a regeneration check of the results file.

**Source.** The tracking-file entry "Relabel the 'Time to detection' column" in [PROGRESS.md](../PROGRESS.md), the [Failure and reload under load](../RESULTS.md#failure-and-reload-under-load) section of the results, and [ADR-0018](adr/0018-no-retry-ever.md).

## 7. Health probes count as backend arrivals

**What happened.** The benchmark harness reads a counter from each backend to learn how many requests it received. The load balancer also runs an **active health check**: it sends its own periodic probe requests to each backend to see whether the backend is up. The probe goes to the backend's own configured URL, and the benchmark configuration points that URL at the same path as the benchmark traffic. The backend counts the probes as arrivals.

**What it cost.** Every counter reading includes a few probe arrivals. The number is negligible at benchmark rates. It is large enough that the literal rule for a spill, "more than one backend received traffic", always reads yes. A spill happens when a **hot key** (one key carrying a large share of the requests) lands on a backend that is over its load limit, and the balancer moves part of that traffic to another backend. The results generator therefore reports a spill only above a minimum share of requests. That threshold is a workaround on the reading side. The cause is unfixed.

**What the project would do instead.** Point the health check at a dedicated health path on the backend, or have the backend's counter count only the paths that carry benchmark traffic.

**Source.** The tracking-file entry "Health probes count as backend arrivals" in [PROGRESS.md](../PROGRESS.md), and [ADR-0011](adr/0011-health-passive-outlier-and-circuit-breaker-composition.md), decision 11 (probes use the backend's own URL).

## 8. The number check in the results generator misses some tables

**What happened.** The results generator fails if a number quoted in the written commentary differs from the matching number in a generated table. The check reads the tables line by line and tracks whether it is inside a table and which column units apply. It does not reset that tracking where one generated section ends and the next begins. The first header line of a section that follows a table is read as a data row. The next separator line then takes its column units from the previous section's header.

**What it cost.** Cells in the degraded and hot-key tables are never registered as valid targets for the check. A number quoted from those tables is not verified, and the check gives no warning. The results commentary works around the bug by quoting only cells that the check can see.

**What the project would do instead.** Reset the tracking state at every section boundary, and add a test that quotes a degraded-table cell and fails before the fix.

**Source.** The tracking-file entry "Narrative number check misses section-boundary tables" in [PROGRESS.md](../PROGRESS.md), and the generator in [`bench/generate-results.sh`](../bench/generate-results.sh).

## 9. Seven commits carry AI-attribution trailers

**What happened.** Seven commits contain a trailer line that names an AI assistant as co-author: `25c1952`, `fd5ce03`, `9c25a11`, `c7de908`, `84b7dab`, `7939ade` and `cf68104`. All seven are ancestors of `8c2b7d4`, the commit that the published benchmark results name. All seven are already pushed to the remote.

The project has no commit-message hook that would reject such a trailer. The project's guidance defers pre-commit hooks until a working prototype exists, and then only if the owner asks, so the hook does not exist.

**What it cost.** The history contains attribution that no hook screened out. Changing those commits would change the hash of every later commit. The published results and the sprint retrospective tables cite commits by hash, so a rewrite would break those citations.

**What the project would do instead.** Add a commit-message hook as soon as the deferral ends. This document does not recommend a rewrite of history.

**Source.** The commits listed above (`git log --format=%H%n%B <hash>` shows each trailer), the deferral list in [AGENTS.md](../AGENTS.md), and the benchmark commit named in [RESULTS.md](../RESULTS.md).

## Items in the owner's own voice

These slots are left empty for the project owner. Nothing below is written as the owner's opinion.

### Owner item 1

_(empty — to be written by the owner)_

### Owner item 2

_(empty — to be written by the owner)_
