# Sprint 5 Spec: Benchmark Execution & Publication (S5.T5–T12)

Status: ready-for-agent

Synthesised from the 2026-09-30 grilling session (decisions Q1–Q30). It supersedes the owner's pre-grilling S5.T5–T12 ticket list. Vocabulary follows `CONTEXT.md`: **competitor**, **matched**, **nearest-equivalent**, **hot key**, **no-op reload**, **drain reload**, **draining**, **drain window**, **hash key**, **selectable**.

---

## Problem Statement

The benchmark harness from S5.T4-infra and S5.T4-harness exists, but it has never produced a published number. Its current shape would make any numbers it did produce hard to defend:

- Two of the four algorithms (consistent-hash, p2c-ewma) run solo, with no Nginx comparison, although open-source Nginx has nearest equivalents for both. The "honest comparison with Nginx" that Sprint 5 promises covers only half the algorithms.
- Nothing isolates CPU. The load balancer, Nginx, four backends and the load generator all compete for the same vCPUs, so a throughput ceiling may reflect the load generator starving the proxy. Nginx's automatic worker count ignores any CPU pinning, so pinning alone would be unfair to Nginx.
- The dummy backend writes a structured log line per request. At tens of thousands of requests per second, the backends' logging may become the ceiling, and both competitors would then report the same number for a reason unrelated to either.
- The SIGHUP-under-load run reloads an unchanged config. Nothing is added, removed or drained, so it barely exercises the reload path and says nothing about the actual claim of zero drops while draining.
- No scenario shows the algorithms behaving differently under a degraded backend, and there is no competitor-agnostic way to see which backend served each request.
- The load generator reports no p99.9, although the published tables promise p50/p99/p99.9.
- No provenance is recorded: toolchain versions, core counts, commit, dirty state.
- No single command reproduces the results from a clean checkout. The Sprint 5 exit criterion ("reproduces all published numbers from a clean checkout") is a claim with no evidence behind it.
- The consistent-hash hash key is the client address, and the load generator is one container with one address. Every consistent-hash run is therefore a single hot-key scenario, and nothing documents that.

Two of the original ticket titles also contradict accepted decisions. S5.T6 names wrk, which ADR-0020 dropped. S5.T11 asks for deployment manifests, which ADR-0019 rejects as unused surface.

## Solution

Close the harness gaps first, as small independent changes, each verified only by smoke. Then add a one-command reproducer that refuses any condition that would make results unreproducible. Run it **exactly once**, from a fresh clone, on the final harness, and commit its raw output untouched. Finally, generate every published table and the methodology section from that raw output with a script, so that no published number is typed by hand.

The owner gets:

- every algorithm compared head-to-head with Nginx, with nearest-equivalent comparisons labelled and their gaps stated;
- competitors pinned to identical, verified CPU budgets;
- a degraded-backend scenario whose headline result is the per-backend request distribution;
- two reload runs (no-op and drain) with pass/fail verdicts fixed before any run;
- full provenance for every result set;
- one command that reproduces everything;
- a results document whose tables can't drift from the raw data.

## User Stories

### Comparison fairness

1. As the project owner, I want every load-balancer algorithm to have an Nginx competitor, so that the benchmark comparison covers the whole system rather than half of it.
2. As the project owner, I want round-robin and least-connections labelled as **matched** comparisons, so that readers know the algorithms really are the same.
3. As the project owner, I want consistent-hash and p2c-ewma labelled as **nearest-equivalent** comparisons, so that no reader mistakes an approximate match for an exact one.
4. As a reader of the results, I want each nearest-equivalent comparison to state its gap (no bounded loads for Nginx's consistent hash, no latency signal for Nginx's two-choice random), so that I can tell which differences come from the features the project built.
5. As a reader of the results, I want every result file to record whether its comparison was matched or nearest-equivalent, so that the label can't be lost between raw data and the writeup.
6. As the project owner, I want the Nginx competitor configs organised by protocol and then algorithm, mirroring the load balancer's bench configs, so that a config and its competitor are found in the same place and the harness maps between them trivially.
7. As a maintainer, I want renaming the existing Nginx configs kept separate from adding new ones, so that history and blame stay readable.
8. As the project owner, I want each competitor pinned to the same two vCPUs, so that throughput differences come from implementation, not scheduling luck.
9. As the project owner, I want each backend on its own vCPU and the load generator on two separate vCPUs, so that neither the backends nor the load generator steal cycles from the competitor under test.
10. As the project owner, I want Nginx's worker count fixed at two, matching its cpuset, so that Nginx isn't unfairly run with eight workers on two cores.
11. As a maintainer, I want the fixed worker count commented with the reason, so that a future agent doesn't "fix" it back to automatic or parameterise it.
12. As the project owner, I want the harness to verify at run time that the load balancer reports GOMAXPROCS of 2, so that the pinning is known to have taken effect for the Go competitor.
13. As the project owner, I want the harness to verify at run time that Nginx is running exactly two workers, counted only after Nginx is serving, so that the pinning is known to have taken effect for the Nginx competitor.
14. As the project owner, I want a slice to abort, naming the failed check, when either verification fails, so that an unfair run can never produce numbers.
15. As the project owner, I want per-request backend logging switched off in the bench stack, so that backend logging is not the throughput ceiling.
16. As a demo-stack user, I want per-request backend logging to stay on by default outside the bench stack, so that the existing observability demo is unchanged.

### Instrumentation

17. As the harness, I want each backend to expose a request counter that anything on the compose network can read, so that the per-backend distribution is measurable for both competitors in the same way.
18. As the harness, I want that counter to increment on arrival, before any injected latency, so that a request still in flight has already been counted.
19. As the harness, I want health probes and counter reads excluded from the counter, so that it counts only benchmark traffic.
20. As the harness, I want the counter to report the backend's name alongside the count, so that results name backends unambiguously.
21. As the project owner, I want the load balancer's startup line to report its effective GOMAXPROCS and Go version, so that the methodology can state both from observed values.
22. As a maintainer, I want those two fields added to the canonical logging vocabulary, so that the field names stay consistent.

### Degraded-backend scenario

23. As the project owner, I want a scenario in which one backend is 50 ms slow for the whole run, so that algorithms that react to load or latency can be seen doing so.
24. As the project owner, I want that scenario run for all four algorithms against both competitors, so that the eight results can be compared.
25. As the project owner, I want all eight degraded runs at one absolute rate (half the h2/round-robin/10 KiB load-balancer peak), so that their distributions and latencies are directly comparable.
26. As the project owner, I want the degraded slice to find that peak itself when it runs alone, so that it can be smoke-tested without a full core run.
27. As a reader of the results, I want the per-backend request share recorded for every degraded run, so that I can see where the traffic went, not just the aggregate latency.
28. As a reader of the results, I want the expected shape stated (p2c-ewma and least-connections shift away from the slow backend; round-robin splits evenly; the load balancer's consistent-hash spills if the slow backend owns the hot key, and Nginx's doesn't), so that I can check the result against the prediction.
29. As the project owner, I want the slow backend restored to normal speed when the slice ends, including on failure, so that later slices aren't silently degraded.
30. As the project owner, I want the slow backend to keep passing health checks, so that the scenario measures routing under degradation, not ejection.

### Hot key

31. As a reader of the results, I want every consistent-hash run, core and degraded alike, to record the per-backend distribution, so that the hot-key owner and any spill are visible.
32. As a reader of the results, I want each consistent-hash result to name the hot-key owner and say whether spill occurred, so that the bounded-loads claim is backed by evidence.
33. As a reader of the results, I want the single-address load generator disclosed as a hot-key scenario, so that I understand why Nginx sends everything to one backend.

### Reload under load

34. As the project owner, I want a **no-op reload** run, so that the bare cost of the reload path under load is measured.
35. As the project owner, I want a **drain reload** run that removes one backend under load, so that the "zero drops while draining" claim is tested directly.
36. As the project owner, I want both reload runs judged against criteria fixed before any run, so that the writeup reports a verdict, not a judgment call.
37. As the project owner, I want both reload runs to pass only with zero non-2xx responses and zero transport errors over the whole run, so that any dropped or drain-cancelled request fails the run.
38. As the project owner, I want both reload runs to pass only if the post-reload p99 is within a fixed factor (2×) of the same run's pre-reload p99, so that a latency spike from reloading is caught.
39. As the project owner, I want the drain reload to pass only if the removed backend receives zero arrivals after the reload is applied, so that "never selected again" is verified from the backend's side.
40. As the project owner, I want arrivals between the signal and the reload being applied recorded but not judged, so that legitimate pre-swap traffic isn't counted as a failure.
41. As the project owner, I want the reloaded config rewritten in place (same file identity) and never replaced by rename, so that the bind-mounted config really changes and the run isn't silently useless.
42. As the project owner, I want the harness to assert that the config file's identity is unchanged across the rewrite and abort otherwise, so that the rename mistake can't pass unnoticed.
43. As a maintainer, I want the committed benchmark configs never modified by a run, so that the tree stays clean and the configs stay authoritative.
44. As a reader of the results, I want each reload result to carry a PASS or FAIL verdict naming any failed criterion, so that the outcome is unambiguous.

### Smoke and progress

45. As an agent developing the harness, I want a smoke slice that brings up every (protocol, algorithm, competitor) combination the matrix uses and requires 100% success in a short, low-rate attack, so that a broken config is caught in about a minute instead of two hours into a full run.
46. As the project owner, I want the smoke slice excluded from the full matrix, so that it gates the published run without being part of it.
47. As the person running a multi-hour benchmark, I want a progress line before every run showing its position, elapsed time and an ETA, so that a long run isn't mistaken for a hang.
48. As the person piping harness output, I want progress on stderr, so that the summary tables on stdout stay clean.

### Provenance

49. As a reader of the results, I want each result set to record the commit it was produced from, and separately whether the tree was dirty, so that the numbers trace to exact code.
50. As a reader of the results, I want the host OS and CPU model, Docker and Compose versions, Docker's vCPU and memory allocation, the cpuset split, the load balancer's Go version and GOMAXPROCS, Nginx's version and worker count, and the load generator's version recorded, so that I can interpret the numbers.
51. As a reader of the results, I want start and finish timestamps recorded, so that the measured wall-clock time is reported rather than predicted.
52. As an agent developing the harness, I want the harness itself to run on a dirty tree, so that I can smoke-test uncommitted work.

### Reproducer

53. As a stranger with the repo, I want one command that goes from a fresh clone to the full result set, so that I can reproduce every published number.
54. As a stranger, I want the reproducer to check up front for the tools it needs, so that it fails in seconds with a clear message rather than halfway through.
55. As a stranger, I want the reproducer to refuse to run with fewer than eight vCPUs available to Docker, so that I get a clear error instead of an obscure cpuset failure, and never a silently different split.
56. As the project owner, I want the reproducer to refuse to run while any container is running, listing each one, so that a forgotten stack on the same cores can't skew results.
57. As the project owner, I want the reproducer never to stop containers itself, so that it never interferes with unrelated work.
58. As the project owner, I want the reproducer to refuse a dirty tree and list the dirty files, so that published results always come from a commit.
59. As a stranger, I want the reproducer to generate certificates, build images, run the smoke slice and then the full matrix, so that no manual step is needed.
60. As a stranger, I want the prerequisites, the command and the expected duration documented, so that I know what I'm committing to before I start.

### Publication

61. As the project owner, I want the published run produced by the reproducer from a fresh clone, so that the exit criterion is evidenced by the run itself.
62. As the project owner, I want the published commit to contain only the raw output, with no hand-edited tables or narrative, so that raw data and interpretation stay separate.
63. As the project owner, I want the recorded commit to be the one the results commit sits on, so that the chain from code to numbers can be checked.
64. As the project owner, I want every published table and the methodology section generated from the raw output, so that no number is transcribed by hand.
65. As a reader, I want p50, p90, p95, p99, p99.9 and max, with p99.9 taken from the HDR histograms, so that the promised tail percentile is real.
66. As the project owner, I want the hand-written narrative and the generated regions to live in one document, separated by markers, so that readers get one page and regeneration never clobbers prose.
67. As the project owner, I want regeneration to fail when a latency or throughput figure in the narrative (including one marked approximate with `≈` or `~`) doesn't match a generated cell, so that the narrative can't drift from the data.
68. As the project owner, I want configuration constants and percentages exempt from that check, so that it has no false positives.
69. As the project owner, I want regeneration to refuse raw output recorded from a dirty tree, so that dirty results can't be published by accident.
70. As the project owner, I want a second regeneration to produce no diff, so that the committed tables are known to be exactly what the raw data implies.
71. As a reader, I want the methodology to disclose that pinning inside Docker Desktop's VM still shares physical cores through a hypervisor, so that I treat the throughput ceiling as soft.
72. As a reader, I want the methodology to state that per-request backend logging was off and why, so that the backend-ceiling concern is addressed.
73. As a later documentation author, I want the results document to be self-contained, so that the design-decisions document can link to it rather than duplicate it.

## Implementation Decisions

### Work breakdown and order

- The work is nine units, identified by the ticket IDs below. The owner will write the agent prompts, and this spec is their shared source.
  - **S5.T5**: Nginx competitor configs matched per algorithm, plus the smoke slice.
  - **S5.T5.5**: dummy-backend instrumentation (logging switch, arrival counter).
  - **S5.T5.6**: load-balancer startup runtime fields.
  - **S5.T5.7**: harness fairness and provenance (CPU pinning, run-time verification, progress, provenance record).
  - **S5.T12**: the one-command reproducer.
  - **S5.T8**: the degraded slice and distribution logging.
  - **S5.T9**: the two reload runs.
  - **S5.T6**: the single published run. It absorbs **S5.T7**, because the latency sweep's rates are fractions of the peak found in the same invocation.
  - **S5.T10**: the results generator and results document.
- **S5.T11 is dropped.** ADR-0019 makes the distroless container the deployment target and rejects orchestration manifests as unused surface. The reproducer is a benchmark tool, not a deployment, so ADR-0019 stands.
- Order: S5.T5, S5.T5.5 and S5.T5.6 run in parallel (they touch disjoint modules) → S5.T5.7 → S5.T12 → S5.T8 → S5.T9 → S5.T6 → S5.T10. Everything from S5.T5.7 onward is serial because each unit edits the harness script.
- S5.T12 ships before S5.T8 and S5.T9. Its own verification covers the slices that exist when it lands. S5.T8 and S5.T9 each smoke-test their own slice, and S5.T6 is the first run of everything.
- No unit before S5.T6 commits benchmark results. All published numbers come from one invocation on the final harness.

### Competitors (S5.T5)

- Nginx competitor configs are organised by protocol, then algorithm, using the same hyphenated algorithm tokens as the load balancer's bench configs. The three existing configs (HTTP/1.1 round-robin, h2 round-robin, h2 least-connections) move into this layout in a rename-only change that comes first.
- New h2 nearest-equivalents:
  - consistent-hash uses Nginx's consistent `hash` on the remote address, which is the same hash key the load balancer uses, with no bounded loads;
  - p2c-ewma uses Nginx's `random two least_conn`, which is two-choice over active connections with no latency signal.

  Each config's header states its gap.
- All competitor configs fix `worker_processes` at 2, with a comment tying the value to the competitors' cpuset.
- The harness maps (protocol, algorithm) to its Nginx competitor by path, and the solo-algorithm path is removed. The core slice becomes 4 algorithms × 3 sizes × 2 competitors × {peak, latency} = 48 runs.
- Every result's header line records `comparison=matched` or `comparison=nearest-equivalent`.
- Smoke slice: for each (protocol, algorithm) pair the matrix uses (h2 × all four, HTTP/1.1 × round-robin) and each competitor, it runs a 2-second, 50 req/s attack on the 10 KiB endpoint, and requires 100% success or fails naming the combination. It is not part of `all`.

### Fairness (S5.T5.7, plus the S5.T5 worker count)

- Fixed 8-vCPU split:
  - load balancer and Nginx share cores 0–1 (only one is under load at a time);
  - backend1–4 each get one core, 2 to 5;
  - the load generator gets cores 6–7.
- The split is never scaled from the available CPU count, because scaled splits give incomparable numbers. The compose file states the split in one sentence, and that sentence is reused verbatim as the methodology statement.
- Whenever the harness (re)creates the load balancer, it waits for readiness and reads `gomaxprocs` from the startup line. The slice aborts unless the value is 2.
- Whenever the harness (re)creates Nginx, it waits until Nginx serves a 200, then counts worker processes. The slice aborts unless there are 2.
- The bench stack turns per-request backend logging off.

### Dummy-backend instrumentation (S5.T5.5)

- A `LOG_REQUESTS` environment switch, parsed with the same strictness as the existing `SLEEP_MS` and `FAIL_RATE`:
  - unset or empty means on;
  - `true` and `false` are accepted;
  - anything else is a startup error naming the variable.

  When it's off, no per-request log line is written on any path. The startup line still logs and records the switch's value.
- `/stats` contract: `GET` returns 200 with a JSON body of the form `{"backend":"<name>","requests":<n>}`.
  - It bypasses the injected latency and failure rate, as `/health` does.
  - The count is a single per-process atomic integer, not a map, because each backend runs as its own container.
  - It increments as the handler's first action, before any injected sleep, so it counts arrivals.
  - `/health` and `/stats` don't increment it.
- The dummy backend's concurrency note documents the atomic counter.
- The repo-root demo stack keeps the default (logging on).

### Load-balancer startup fields (S5.T5.6)

- The existing `"l7LoadBalancer starting"` startup line gains `gomaxprocs` (the effective value) and `go_version` (the runtime version string). It is the same log call, with no new statement.
- Both field names join the canonical logging vocabulary. No other log line, metric or behaviour changes.
- A Prometheus Go collector was rejected: it would change the metrics exposition the contract docs describe and add unrequested series.

### Provenance (S5.T5.7)

- Every harness invocation writes a provenance record at the root of the results directory when it starts, and updates it when it finishes. Fields:
  - `git_sha` and `git_dirty`, a boolean kept as a separate field, never a suffix on the SHA;
  - slices run;
  - start and finish timestamps (UTC);
  - host OS and host CPU model, taken from the host (the harness runs there);
  - Docker and Compose versions;
  - Docker's vCPU count and memory;
  - the cpuset per service;
  - the load balancer's Go version and GOMAXPROCS, from its startup line;
  - Nginx's version and worker count;
  - the load generator's version.
- The harness refuses neither a dirty tree nor running containers. It is a development tool, and enforcement sits at the publication boundary (the reproducer and the results generator).
- Progress goes to stderr before every run, in the form `[run 14/71] h2/consistent-hash/10kb/lb peak-search  elapsed … eta …`. The total comes from the selected slices, and the ETA is elapsed × total / completed.

### Reproducer (S5.T12)

- A single `make` target, `bench-repro`, runs these steps in order and stops at the first failure:
  1. **Preflight**:
     - Docker, Compose v2, openssl and make are present;
     - Docker reports at least 8 vCPUs;
     - no container is running at all, not only bench ones. Otherwise it prints each container's ID and image and never stops anything;
     - the working tree is clean, counting untracked files. Otherwise it prints the dirty files.
  2. Generate the certificates.
  3. Build the bench images.
  4. Run the smoke slice.
  5. Run the full matrix.
- `make help` lists the target, including its expected duration. The bench documentation gains a "Reproducing the published numbers" section: prerequisites, the one command, and an expected duration of about 2–2.5 hours on Docker Desktop.

### Degraded slice (S5.T8)

- One backend (backend3) is recreated with a 50 ms injected delay. Every other backend stays at 0. It is restored on exit, including on failure.
- Eight runs: four algorithms × two competitors, h2, 10 KiB, a 30-second measurement plus the standard 5-second warmup, which is discarded.
- One absolute rate for all eight: 50% of the h2/round-robin/10 KiB load-balancer peak from the same invocation. When the slice runs alone, it first runs the peak search for that single case.
- Before and after each measured run, the harness reads every backend's `/stats` from inside the compose network. It records the per-backend deltas as counts and as shares.
- Every consistent-hash run, core and degraded, records the same distribution and names the hot-key owner (the plurality backend) and whether spill occurred (traffic reached more than one backend).
- Degraded results get their own results subdirectory, and the slice is part of `all`. Its summary table columns: algorithm, competitor, p50, p99, and the share for each of the four backends.

### Reload runs (S5.T9)

- The failure slice becomes three runs: backend-kill (unchanged), the no-op reload and the drain reload. All three use the existing steady state: h2, round-robin, 10 KiB, 50% of peak, 60 seconds, with the event at T+30s.
- A constant `RELOAD_P99_FACTOR` = 2 sits with the harness's other constants.
- For the reload runs, the load balancer mounts a working copy of the committed round-robin h2 config inside the results scratch area. The drain config (without backend4) is derived from the committed file at run time, not committed as a ninth config.
- The working copy is rewritten by copying over the existing file, so the file keeps its identity. It is never moved or renamed into place. The harness checks the file's identity before and after, and aborts if it changed.
- **No-op reload** PASS iff:
  - zero non-2xx and zero transport errors over the whole run;
  - post-event p99 ≤ `RELOAD_P99_FACTOR` × the pre-event p99 of the same run (post-warmup to T+30s).
- **Drain reload** PASS iff:
  - the no-op criteria hold;
  - the removed backend's arrival count doesn't move between "reload applied" and the end of the run. `/stats` is sampled just before the signal, once the load balancer logs the reload as applied, and at the end. The first interval is recorded, and only the second is judged.

  A drain-window cancellation shows up as a 502, so the zero-non-2xx rule covers it.
- Each result carries a `verdict=PASS|FAIL` line naming any failed criterion, and the summary shows the verdict. After the drain reload, the load balancer is recreated on the committed config.
- **Refinement made while drafting, not voted on in the grilling:** the p99 baseline is the same run's own pre-event window, not the core latency@50% result. The conditions are identical and it removes a cross-slice dependency. The owner should confirm or revert this.

### Published run (S5.T6)

- It is performed from a fresh clone at a commit where every earlier unit in this bundle is done, by running `make bench-repro`, which must exit 0.
- The commit contains only the raw results directory: 71 result sets plus the provenance record with `git_dirty` false. It must sit directly on the commit named by `git_sha`.
- Its tracking entry records the observed wall-clock time.
- If the numbers look noisy or implausibly low, the fallback is a rented Linux host running the same command. That decision is the owner's and is outside this bundle.

### Results generator and document (S5.T10)

- A results-generator script reads the raw output: percentiles from the JSON reports, p99.9 from the HDR files, verdicts and distributions from the result headers, and methodology facts from the provenance record. It writes only inside marker regions (`<!-- BEGIN GENERATED: <section> -->` … `<!-- END GENERATED -->`) of a single results document.
- It takes an optional results directory and document path, defaulting to the real ones. This is the new test seam.
- Generated sections:
  - methodology (hardware, versions, cpuset sentence, logging-off statement, measured wall-clock);
  - core tables;
  - protocol tables;
  - failure verdicts;
  - degraded distributions;
  - hot-key distributions.
- Hand-written narrative sits outside the markers:
  - per-algorithm analysis;
  - each nearest-equivalent gap;
  - the hot-key paragraph ("all requests hash to one backend; Nginx pins 100% to it; the load balancer spills once in-flight load exceeds the bound");
  - the Docker Desktop VM disclosure.
- Narrative number check: every token outside the markers matching `[≈~]?[0-9][0-9,]*(\.[0-9]+)?\s?(ms|µs|req/s)` must equal a generated cell once the approximate prefix is stripped and commas and spacing are normalised. Otherwise the script exits nonzero and names the token. Percentages, core counts, sizes, ADR numbers and percentile names are not matched.
- The script refuses a provenance record with `git_dirty` true.
- The document is self-contained, so the later design-decisions document links to it.

### Decisions explicitly not taken

- No new ADR. No decision here is at once hard to reverse, surprising and the result of a real trade-off. The methodology is recorded in this spec and in the results document.
- The consistent-hash hash key source is unchanged (frozen behaviour).

## Testing Decisions

What makes a good test here: it exercises externally observable behaviour through the highest available seam (HTTP responses, the built binary's log output, the harness's exit status and its files), never internal helpers or variables. Harness checks prove both directions: the success path, and that each guard actually fires.

- **Dummy backend** (TDD, Red-first), through the existing handler constructor driven by `httptest`, with only HTTP-visible assertions. Cases:
  - the logging switch parses unset, empty, `true`, `false` and invalid values correctly;
  - no per-request output is written when logging is off;
  - `/stats` returns the documented shape;
  - arrivals on the payload and default paths are counted, and `/health` and `/stats` are not;
  - **counts on arrival**: with an injected delay, `/stats` reflects a request that hasn't completed yet;
  - concurrent requests are counted exactly, under `-race`.

  Prior art: `TestHandlerEndpoints` and `TestHealthBypassesChaos` (handler via `httptest`), `TestTLSFilesFromEnv` (env parsing).
- **Load-balancer startup fields** (TDD, Red-first), through the existing built-binary seam. The test builds the binary, runs it with `GOMAXPROCS=2` in its environment and a temp config pointing at an `httptest` backend, reads the startup JSON line, then stops the process with SIGTERM. It asserts that `gomaxprocs` is 2 and that `go_version` is present and matches the toolchain's. No new seam in `main`. Prior art: `TestBinaryLogsInjectedVersionAndCommit`.
- **Harness, compose and reproducer** (TDD-exempt: shell and config). The seam is the harness's own entry points. Each unit's gate:
  - static checks: `shellcheck`, `bash -n`, `docker compose config`, and `nginx -t` on every competitor config;
  - a green smoke slice;
  - a targeted run of the unit's own slice, with durations reduced locally and uncommitted if needed, stated in the session log;
  - deliberate negative checks recorded in the session log:
    - a wrong Nginx worker count aborts;
    - swapping the in-place copy for a rename trips the file-identity assertion;
    - the preflight refuses a dirty tree, a running container and too few vCPUs (the last simulated by raising the threshold locally).

  Prior art: the S5.T4-harness verification (static checks plus a live reduced-constant smoke).
- **Results generator**, through the new seam: run it against a small committed fixture result set with a committed shell test script. The fixture includes a JSON report, an HDR file, a failure verdict, a distribution and a provenance record. The test checks:
  - p99.9 comes from the HDR file;
  - a second run produces no diff;
  - a narrative number with no matching cell makes the script exit nonzero, including when it has a `≈` or `~` prefix;
  - percentages and constants aren't flagged;
  - `git_dirty` true is refused.
- **Published run**, verified against the data: `make bench-repro` exits 0 from a fresh clone; there are 71 result sets; the provenance record shows a clean tree and a SHA equal to the results commit's parent.
- The standard gates apply to every unit that touches Go: `make test`, `make test-race`, `go vet ./...`, `make fmt`.

## Out of Scope

- The README architecture diagram, `docs/design-decisions.md` and `docs/what-id-do-differently.md`: to be proposed as S5.T13–T15 for owner approval. The results document is written so they can link to it.
- Changing the consistent-hash hash key source, or using several load-generator addresses (frozen behaviour; it would need an ADR).
- Mid-run latency injection or any runtime toggle on the dummy backend.
- Renting or provisioning a Linux benchmark host (an owner decision after the published run).
- Kubernetes or any orchestration manifests (ADR-0019), which is why S5.T11 is dropped.
- Any change to load-balancer request-path, reload or selection behaviour. The only load-balancer change is two startup log fields.
- The deferred reload-outcome counter metric.
- A Prometheus Go collector.
- Configurable thresholds via flags: harness parameters stay constants (existing harness principle).

## Further Notes

- The nine units are split into 20 tickets under `issues/` (01 is the tracking amendment, S5.D0). The results generator (16–18) is built against fixtures in parallel with the harness work, and only the narrative (20) waits for the published run.
- Final matrix: core 48, protocol 12, failure 3, degraded 8, so **71** runs in `all`, with smoke separate. That's about 2–2.5 hours on Docker Desktop. The published figure is the measured one, never an estimate.
- Tracking updates are not part of this spec and have not been made: the `PROGRESS.md` entries for S5.T5–T12 (including the new S5.T5.5, S5.T5.6 and S5.T5.7 with their blocked-by lines, S5.T7 recorded as merged into S5.T6, S5.T11 recorded as dropped per ADR-0019, and S5.T13–T15 recorded as proposed), and the `MILESTONES.md` Sprint 5 deliverables (nearest-equivalent rows, the degraded slice, the reproducer). They should land before the first unit is claimed, per AGENTS.md Step 1.
- `CONTEXT.md` gained **No-op reload**, **Competitor**, **Nearest-equivalent** and **Hot key** during the grilling. Those entries are uncommitted.
- The p99 baseline refinement in the reload-runs section is the only decision here that the owner hasn't explicitly approved.
