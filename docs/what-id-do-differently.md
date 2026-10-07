# What I'd do differently

These are the weaknesses I know about in the load balancer and its benchmark. For each one I say what is wrong, what it costs, and what I would change. They are not ranked. [Design decisions](design-decisions.md) explains how the system works.

## 1. Make the latency estimate decay

P2C sends each request to the faster of two random backends, by an EWMA of latency. The estimate changes only when a request to that backend finishes. Nothing lowers it while the backend is idle. So a backend that was slow once can keep a high estimate, lose every comparison, and never get the sample that would correct it. A backend that recovers from 500 ms to 10 ms can stay at zero traffic.

I would let the estimate drift back toward a neutral value after a period with no samples. Another option is to send an occasional probe request to a backend with an old estimate. See [ADR-0010](adr/0010-p2c-ewma-backend-latency-state-cold-start-and-failure-penalty.md).

## 2. Warm up before the first benchmark step

The peak search starts at a seed rate right after a container starts. There is no warm-up and no retry. The round-robin runs through the load balancer over HTTP/2 at 200 B and 10 KiB failed the p99 check at the seed rate, so the run skipped them. Every other algorithm passed those sizes at much higher rates. So the skips look like a cold start, not a real ceiling. The result is a gap: there is no HTTP/2 round-robin comparison with Nginx at small sizes.

I would send a short warm-up burst after each container start, or retry the first step once, before I accept "no sustainable rate".

## 3. Isolate the slow backend in the degraded scenario

The degraded scenario should slow one of four backends and show which algorithms move traffic away. Instead, every median was near the injected 50 ms. That suggests the delay reached all four backends. I suspect the benchmark script exports the delay as an environment variable, and all four backends read it through one shared Compose definition. I have not verified this. As a result, I have no measured evidence for how P2C or least connections handle a slow backend.

I would first find the leak. Then I would give the slow backend its own Compose definition and check each backend's own median before I trust the scenario. See [Degraded backend](../RESULTS.md#degraded-backend).

## 4. Benchmark on separate, real hardware

The whole benchmark runs on one laptop: an Apple M3 with 8 vCPUs and 8 GiB for Docker. The generator shares that machine with the proxies. For large responses, the generator cannot send fast enough to reach the proxy's ceiling. The harness rejects steps that the generator under-delivered, but a passing peak is still a lower bound. The pinned cores also sit inside a virtual machine, so the ceiling is soft. The results only support an ordering: over HTTP/1.1 with round-robin, Nginx is ahead at all three sizes. They do not support a ratio.

I would run the generator on a different machine from the proxies, on hardware with no virtual machine between them. I would also repeat each scenario so that I measure the run-to-run spread instead of guessing it. See [ADR-0022](adr/0022-honest-benchmarking-under-generator-limits.md).

## 5. Fix the "Time to detection" column

The failure scenario stops one backend at 29 s. The results table has a "Time to detection" column, but the harness computes it as the last failed request minus the first. In the published run that is 0.009 s. That is the width of the error window, not the delay before the load balancer noticed. The first failure came 0.73 s after the stop command. A reader can wrongly conclude that the load balancer detected the failure in 9 ms.

I would rename the column to the error-window width. I would add a separate column for the time from the stop command to the first failed request.

## 6. Keep health probes out of the backend counters

The benchmark reads a request counter from each backend. The active health check sends its probes to the backend's own URL, and the benchmark config points that URL at the same path as the benchmark traffic. So every counter includes a few probe requests. The number is small, but it means "more than one backend got traffic" is always true. The results generator works around this with a minimum share before it reports a hot-key spill. The cause is still there.

I would point the health check at a dedicated health path, or have the backend count only the benchmark path.

## 7. Fix the number check in the results generator

[`bench/generate-results.sh`](../bench/generate-results.sh) fails if a number in the written commentary does not match the generated table. It reads tables line by line and tracks the column units. It does not reset that state at a section boundary. So the first header of the next section reads as a data row, and the next separator takes the previous section's units. Cells in the degraded and hot-key tables are never checked, and nothing warns about it. Today the commentary quotes only cells that the check can see.

I would reset the state at every section boundary. I would also add a test that quotes a degraded-table cell and fails before the fix.
