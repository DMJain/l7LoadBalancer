# Sprint 5 Spec: Local Live Demo (S5.T16–T17)

Status: ready-for-agent

Synthesised from the 2026-10-02 grilling session (Round 1, the owner's scope correction, and Round 2 with the owner's amendments). It supersedes the owner's pre-grilling titles "S5.T13 — Live public deployment: LB + backends across regions" and "S5.T14 — Demo script"; those IDs stay with the proposed README / design-decisions / what-I'd-do-differently tickets. The decision record is ADR-0023 (local live demo, no public deployment), landed with S5.D1. Vocabulary follows `CONTEXT.md`: **backend**, **selector**, **hash key**, **hot key**, **EWMA latency**, **load**, **selectable**, **probe**, **outlier**, **ejection**, **circuit breaker**, **reload**, **draining**, **drain window**.

---

## Problem Statement

The owner needs to show the load balancer working, live, to a viewer — in an interview over screen-share, or in a recorded video — and to explain the selectors while the viewer watches them act. Today there is nothing to show that with:

- The repo-root demo stack (one LB, three backends, Prometheus, Grafana) carries no traffic unless someone drives it by hand, and nothing in it varies, so every selector looks the same.
- The one published scenario meant to show selectors diverging — the `degraded` slice in `RESULTS.md` — failed: the injected 50 ms reached all four backends, so least-connections and p2c-ewma had nothing faster to prefer. The thing the owner most wants to show is the thing the published evidence could not show.
- Backend latency and failure can only be changed by an environment variable and a container recreate, which restarts the backend under load and is the very mechanism suspected of leaking into every backend in the published run.
- The algorithm cannot be switched on a running LB: only the backend list hot-reloads, and a reload that changes `algorithm` is rejected whole (ADR-0015 decision 4).
- The consistent-hash **hash key** is the client IP, so a single load-generator container is a single key — every consistent-hash run is a **hot key**, and "skewed keys" cannot be shown with one client.
- The existing Grafana dashboard computes every rate over a 5-minute window, so a change made on screen takes minutes to appear, and Grafana is not configured to be embedded in another page.
- The original idea — a public deployment across cloud regions — collides with ADR-0005's non-goals (no hardening against adversarial internet traffic, no cross-region failover, no multi-region validation), costs money and upkeep, and gives noisier, less controllable latency than injection does.

## Solution

A local-only live-demo stack the owner brings up with one command on their own machine, every published port bound to `127.0.0.1`, never internet-reachable. In it:

- Four LBs — one per algorithm — share the same four backends. "Switching algorithm" means pointing the traffic at a different LB, so every comparison is "same backends, same traffic, only the selector changed".
- Eight traffic-generator clients, each with its own container IP and therefore its own **hash key**, send Poisson-arrival traffic with mixed sizes, splitting a total rate by Zipf rank so the hash keys are genuinely skewed. The total rate and the target LB change at runtime.
- Each backend gains an opt-in admin listener so its latency, jitter and failure rate change at runtime, without a restart — giving per-backend latency profiles such as near / mid / far / flaky.
- A control service serves a single page from which the owner switches the active LB, sets each backend's profile, kills and revives backends, and sets the load rate. The charts on the page are embedded panels of the existing Grafana dashboard, tuned by URL to a short window and fast refresh; there is no new charting code.
- Before any UI is built, a scripted acceptance check proves the rig isolates injected latency to one backend and that p2c-ewma moves traffic off it — the guard against the `degraded`-slice failure.
- A written demo script gives the scenario sequence, the click path, and the expected on-screen state at each step, and is what the owner follows to record the demo video.

The repo-root demo stack and the benchmark rig are unaffected: the root stack renders as before, and bench runtime behaviour is unchanged.

## User Stories

### Running the demo locally

1. As the owner, I want to bring the whole demo up with one command, so that I can start a demo in seconds before an interview.
2. As the owner, I want to tear the whole demo down with one command, so that nothing is left running on my machine afterwards.
3. As the owner, I want every published port bound to `127.0.0.1`, so that nothing in the demo is reachable from my network or the internet.
4. As the owner, I want the demo to live in its own compose file, separate from the repo-root stack and the benchmark rig, so that bringing it up never changes either of them.
5. As the owner, I want the demo to cost nothing to run, so that I can rehearse as often as I like.
6. As a reviewer reading the repo, I want an ADR explaining why there is no public deployment, so that the absence of a live URL reads as a decision, not an omission.
7. As a reviewer, I want the demo's numbers clearly separated from `RESULTS.md`, so that I never mistake a demo chart for a benchmark result.

### Algorithms side by side

8. As the owner, I want one LB per algorithm, all sharing the same four backends, so that switching algorithm is instant and needs no reload.
9. As the owner, I want each LB to have its own config file, so that the only difference between the four is the algorithm.
10. As the owner, I want to switch the active LB from the control page, so that I can show a different selector without touching a terminal.
11. As the owner, I want a switch to move all eight clients to the new LB together, so that the new selector sees the whole traffic mix at once.
12. As a viewer, I want the dashboard to show which LB is being viewed, so that I know which selector produced the chart in front of me.
13. As the owner, I want the dashboard to filter by LB using Prometheus's scrape job, so that no LB code has to change to tell the four apart.
14. As the owner, I want the demo script to allow a convergence pause after each switch, so that I never make a claim about a distribution before the selector has had time to learn.
15. As the owner, I want the ADR to state that an LB's EWMA state is cold on its first activation and stale on every later one, so that I narrate what the viewer sees correctly (ADR-0010 cold start).
16. As the owner, I want SIGHUP kept in the demo only for what it actually does — changing the backend list — so that I can show a drain under load without implying the algorithm is reloadable.

### Realistic traffic

17. As the owner, I want traffic with Poisson arrivals, so that the load looks like independent users rather than a metronome.
18. As the owner, I want a mix of response sizes, so that the traffic exercises small and large bodies like real traffic does.
19. As the owner, I want a mix of request-body sizes, so that uploads are part of the traffic too.
20. As the owner, I want eight client services, each with its own container IP, so that the consistent-hash selectors see eight distinct hash keys.
21. As the owner, I want each client to take a share of the total rate by its Zipf rank, so that the hash keys are skewed the way real keys are.
22. As the owner, I want the eight clients defined as explicit services from one YAML anchor with `RANK=1..8`, so that each has a fixed, known rank (replicas cannot carry per-instance ranks).
23. As the owner, I want to set the total request rate at runtime, so that I can raise and lower the load during the demo.
24. As the owner, I want the total rate fanned out to all eight clients by the control service, so that one control on the page drives the whole traffic mix.
25. As the owner, I want each client to change its target LB at runtime, so that switching LB needs no restart.
26. As the owner, I want the generator's workers and memory bounded, as ADR-0021 and ADR-0022 require of the bench generator, so that a high rate cannot exhaust my laptop and make the demo itself the bottleneck.
27. As the owner, I want a client that cannot keep up to show that it is behind, rather than silently send less, so that I don't narrate a rate the LB never received.

### Backend latency profiles

28. As the owner, I want to set any backend's latency at runtime, so that I can make one backend slow on screen without restarting it.
29. As the owner, I want to add jitter to a backend's latency, so that "near / mid / far" look like real latency with spread, not a fixed delay.
30. As the owner, I want jittered latency clamped at zero, so that a large jitter never yields a negative sleep.
31. As the owner, I want to set any backend's failure rate at runtime, so that I can create a "flaky" backend and show passive outlier detection and the circuit breaker react.
32. As the owner, I want the admin listener on its own port, separate from the port the LB proxies to, so that admin calls can never be routed through the LB.
33. As the owner, I want the admin listener to start only when an explicit switch is on, and that switch set only in the demo stack, so that the benchmark rig and root stack cannot have their backends changed mid-run.
34. As the owner, I want the admin endpoint to accept only `POST`, so that a stray browser fetch cannot change a backend.
35. As the owner, I want the admin endpoint to reject unknown fields, so that a typo fails loudly instead of being silently ignored.
36. As the owner, I want the existing `SLEEP_MS`/`FAIL_RATE` environment path left as it is, so that the benchmark rig keeps working exactly as published.
37. As the owner, I want `/health` and `/stats` to keep bypassing injected latency and failure, so that a slow or flaky backend is not ejected by its own health probe unless I mean it to be.
38. As the owner, I want runtime injection to replace env-and-recreate in the demo, so that the demo cannot repeat the `degraded`-slice leak.

### Killing and reviving backends

39. As the owner, I want to kill a backend from the control page, so that I can show the active health checker eject it and traffic move away.
40. As the owner, I want to revive a killed backend from the control page, so that I can show it being reinstated after it passes its probes.
41. As the owner, I want the control service to act only on an allowlist of the demo's own backend containers, so that it cannot touch any other container on my machine.
42. As a reviewer, I want the Docker-socket mount documented in ADR-0023 as demo-only and loopback-only, so that the root-equivalent access is a stated, bounded choice.

### Live view

43. As a viewer, I want the charts to react within seconds of a change, so that cause and effect are visible while the owner talks.
44. As the owner, I want the dashboard to take its rate window from a variable, so that the demo page can use a short window while the root stack keeps the 5-minute default.
45. As the owner, I want the demo page to set the window to 15 s and the refresh to 5 s in its embed URLs, so that the root dashboard JSON needs no demo-specific copy.
46. As the owner, I want a per-backend request-share panel, so that the viewer sees the selector's choice directly, not inferred from rates.
47. As a viewer, I want per-backend p50 next to per-backend request share, so that I can see a backend get slower and then see the selector stop choosing it.
48. As the owner, I want the demo Prometheus to scrape every second, so that short windows have enough samples, while the root stack's scrape interval is unchanged.
49. As the owner, I want Grafana embedding and anonymous Viewer access enabled only in the demo stack, so that the root stack's Grafana settings do not change.
50. As the owner, I want the dashboard to remain the single dashboard JSON the root stack ships, so that the demo and the root stack never drift apart.

### Control page

51. As the owner, I want every demo action on one page, so that a screen-share shows one window, not a terminal and a browser.
52. As the owner, I want the page to show the current state — active LB, each backend's profile, which backends are up, the total rate — so that I always know what I have set.
53. As the owner, I want the page to report a failed action, so that I never narrate a change that did not happen.
54. As the owner, I want the control service to reach the generators and backend admin listeners only over the demo's internal network, so that none of those control ports needs publishing.

### Acceptance check

55. As the owner, I want a scripted acceptance check that runs against the live demo stack, so that I know the rig is sound before I build a UI on it or record a video.
56. As the owner, I want phase 1 to set backend3 to 200 ms with the round-robin LB active and, after 30 s, require backend3's p50 ≥ 150 ms and backends 1, 2 and 4 ≤ 20 ms, so that I know injected latency stays on one backend.
57. As the owner, I want phase 2 to switch to the p2c-ewma LB and, after convergence, require backend3's request share < 15%, so that I know the selector actually moves off the slow backend.
58. As the owner, I want phase 2 to assert on share rather than p50, so that the check does not depend on a p50 computed from the handful of requests p2c still sends to backend3.
59. As the owner, I want the check to run with jitter 0, so that its thresholds are deterministic.
60. As the owner, I want the check to exit non-zero and name the failed assertion with the measured values, so that a failure tells me what leaked.
61. As the owner, I want the check to restore every backend's profile when it finishes or fails, so that a failed check leaves the stack usable.

### Demo script and recording

62. As the owner, I want a written scenario sequence, so that every recording tells the same story in the same order.
63. As the owner, I want the click path for every step, so that I can drive the demo without improvising.
64. As the owner, I want the expected on-screen state after every step, so that I can tell immediately when the demo is not behaving.
65. As the owner, I want the script to say what to narrate at each step and which ADR backs it, so that every claim on camera is defensible.
66. As the owner, I want the script to state the known behaviours of each selector honestly — p2c-ewma's rank-based split and possible starvation of a recovered backend, least-connections' tie-break at low concurrency, consistent-hash's per-client stickiness and bounded spill — so that I never promise behaviour the algorithm does not have.
67. As the owner, I want the script to set a demo rate high enough that in-flight counts are above zero, so that least-connections has something to balance.
68. As the owner, I want a recorded demo video that follows the script, so that a reviewer can watch the system without running it.

## Implementation Decisions

### Work breakdown and order

- S5.D1 — tracking amendment and ADR-0023. **Done.**
- S5.T16.1 — dummy-backend admin listener (issue 01).
- S5.T16.2 — traffic generator (issue 02).
- S5.T16.3.1 — dashboard `$window`, LB variable, request-share panel (issue 03).
- S5.T16.3.2 — `demo/` compose stack, demo Prometheus, demo Grafana (issue 04).
- S5.T16.5 — latency-isolation acceptance check (issue 05).
- S5.T16.4.1 — control service and page: switch, rate, profiles, embedded panels (issue 06).
- S5.T16.4.2 — kill and revive through the Docker socket (issue 07).
- S5.T17.1 — demo script (issue 08).
- S5.T17.2 — record the demo video, `ready-for-human` (issue 09).
- Order: D1 → {T16.1, T16.2, T16.3.1} in parallel (disjoint modules) → T16.3.2 → T16.5 → T16.4.1 → T16.4.2 → T17.1 → T17.2. T16.5 deliberately precedes the control service: the rig is validated before a UI is built on it.

### Scope framing (ADR-0023)

- No public deployment. The originally titled live public deployment is superseded. ADR-0005 is unchanged; no non-goal is weakened and no production claim is added.
- Every published port in the demo stack binds to `127.0.0.1`. The stack is not designed, configured or documented for any other bind address.
- The demo is a separate compose file in a top-level demo area, independent of the repo-root stack and the benchmark rig. Neither changes behaviour because the demo exists.

### Load balancers

- Four LB instances, one per algorithm (`round_robin`, `least_conn`, `consistent_hash_bounded`, `p2c_ewma`), all built from the existing LB image, each with its own config file listing the same four backends. Configs differ only in `algorithm` (and anything that must be unique per instance).
- No LB code changes. The algorithm is never reloaded; switching is done by re-pointing the traffic.
- Prometheus scrapes each LB as its own `job`; that label is the LB identity everywhere downstream.
- SIGHUP stays available for backend-list changes (a drain reload under load), per ADR-0015/ADR-0016.
- EWMA state on switch: cold on first activation (every backend reads `EWMALatency() == 0`, the ADR-0010 decision 2 bootstrap), stale thereafter (only proxied responses update it).

### Dummy-backend admin listener (S5.T16.1)

- A second `http.Server` on its own port (`:9091`), started only when `ADMIN_ENABLED=true`. Strict parsing in the style of the existing `envBool`: unset or empty means off; only `true`/`false` are accepted; anything else is a startup error naming the variable.
- Only the demo stack sets `ADMIN_ENABLED=true`. The listener is never a path on the proxied port and never routed through any LB.
- Contract: `POST` only (other methods → 405 with `Allow: POST`); a JSON body with the fields `sleep_ms`, `jitter_ms`, `fail_rate`; unknown fields rejected (the `DisallowUnknownFields`/KnownFields style) with 400; out-of-range values (negative ms, `fail_rate` outside [0, 1]) rejected with 400. Whether a field omitted from the body keeps its current value or resets is decided at the start of T16.1 and pinned by a test (recommendation: omitted = unchanged, so the page can set one knob at a time).
- The admin listener also needs a way to read the current profile, so that the control page can show state. Recommendation: the `POST` response returns the full profile it now holds; the control service is the source of truth for display.
- Runtime state: the current profile is shared between the admin handler (writer) and the request handler (reader). It is a single immutable value swapped atomically (an `atomic.Pointer` to the profile), so a request reads one consistent `(sleep, jitter, fail_rate)` triple; the file's concurrency comment names it alongside the existing arrival counter.
- Startup: the env values (`SLEEP_MS`, `FAIL_RATE`, jitter 0) are the initial profile. With `ADMIN_ENABLED` unset nothing can change the profile, so runtime behaviour is unchanged.
- Jitter: effective sleep is `sleep_ms` plus a uniform offset in `[-jitter_ms, +jitter_ms]`, clamped at ≥ 0.
- `/health` and `/stats` keep bypassing injected sleep and failure.
- Startup log line records whether the admin listener is enabled and its address.

### Traffic generator (S5.T16.2)

- A new Go program in the repository's single module (the dummy backend's precedent), shipped as its own small image. No new third-party dependency.
- Configuration at startup by environment: `RANK` (1..8), the number of clients N (8), the Zipf exponent, the initial total rate, the initial target LB, and the size mix. Strict parsing with the dummy backend's conventions.
- Share: client k's rate is `total × w_k / Σ w_i`, with `w_k = 1 / k^s` over ranks 1..N. Each client computes its own share from `RANK`; there is no coordination between clients.
- Arrivals: Poisson — exponentially distributed inter-arrival times at the client's own rate, scheduled open-loop (an arrival is due whether or not earlier requests finished), so a slow backend shows up as latency, not as silently reduced load.
- Mix: each request picks a response size from the dummy backend's existing payload paths (200 B, 10 KiB, 1 MiB) and a request-body size, by weights. Default weights are an open decision (see Further Notes).
- Bounded resources, per ADR-0021/ADR-0022: a fixed maximum number of in-flight requests per client and a bounded Go heap (`GOMEMLIMIT`/`GOGC` set by the compose file). Response bodies are drained and discarded, never retained.
- Delivery honesty: when an arrival is due and the in-flight bound is full, the arrival is counted as dropped by the generator, not queued without limit. The generator exposes its offered, sent, dropped, completed and error counts, so a viewer-facing claim about rate can be checked against what was actually sent.
- Runtime control: an HTTP control endpoint on the generator's own port (internal network only) that sets the total rate and the target LB, and reports the current settings and counters. Changing the target takes effect for the next arrival; in-flight requests to the old target complete normally.
- The target is an LB base URL from a fixed allowlist of the four demo LBs; any other value is rejected.

### Demo stack (S5.T16.3)

- One compose file containing: the four LBs; four dummy backends (`ADMIN_ENABLED=true`, request logging on or off as the stack's resource budget requires); eight generator services expanded from one YAML anchor with `RANK=1..8`; a demo Prometheus; a demo Grafana; and, from S5.T16.4, the control service.
- Published ports: only what the owner's browser needs — Grafana and the control page (and, if useful for rehearsal, the active LB's client port) — each bound to `127.0.0.1`. Generators, backend admin listeners, LB metrics and health ports stay on the internal network.
- Demo Prometheus: scrapes each LB as its own job at a 1 s interval. The root stack's Prometheus config is not changed.
- Demo Grafana: provisions the same single dashboard JSON as the root stack; sets `GF_SECURITY_ALLOW_EMBEDDING=true` and anonymous access with the Viewer role. The root stack's Grafana environment is not changed.
- Dashboard changes (in the single JSON, visible to both stacks):
  - a `$window` interval variable, default `5m`, replacing every hard-coded `[5m]` range, so the root stack renders exactly as before;
  - an LB variable populated from the Prometheus `job` label, filtering every panel; in the root stack it resolves to the single LB job;
  - a per-backend request-share panel (each backend's request rate divided by the total, for the selected LB).
- Embed URLs from the control page pass `var-window=15s&refresh=5s` and the selected LB.

### Acceptance check (S5.T16.5)

- A scripted check against the running demo stack, in the style of the existing observability smoke script and chaos scripts: shell, `curl` against the Prometheus HTTP API and the internal control endpoints (via `docker compose exec` or a published loopback port), explicit `fail()` messages, non-zero exit on failure.
- It drives the generators and backend admin listeners directly; it does not depend on the control service or page.
- Preconditions: stack up, all four backends selectable on both LBs used, every backend at `sleep_ms=0, jitter_ms=0, fail_rate=0`, a fixed demo rate high enough to give each backend a meaningful p50.
- Phase 1, round-robin LB active: set backend3 to 200 ms (jitter 0). After 30 s, from the LB's per-backend request-duration histogram over a short window: backend3 p50 ≥ 150 ms; backends 1, 2, 4 p50 ≤ 20 ms.
- Phase 2, p2c-ewma LB active (backend3 still at 200 ms): after a convergence period, backend3's request share < 15%.
- Every failure message names the assertion, the measured values and the LB.
- A trap restores every backend's profile and the generators' rate and target on exit, pass or fail.

### Control service and page (S5.T16.4)

- A new Go service in the same module, its own small image, serving a single static page plus a small JSON API, built with `net/http` only — no web framework, no frontend build step, no new charting code.
- Actions: switch the active LB (sends the new target to all eight generators); set a backend's latency/jitter/failure (forwards to that backend's admin listener); kill and revive a backend (Docker Engine API); set the total rate (fans the same total to all eight generators, each of which derives its own share).
- Fan-out is a T16.4 requirement: the control service sends to all eight generators and reports which, if any, failed; a partial fan-out is reported, not hidden.
- Docker access: the Docker socket is mounted into the control service in the demo stack only. The service talks to the Docker Engine API over the unix socket with the standard library's HTTP client — no Docker SDK, consistent with AGENTS.md's no-heavyweight-dependencies rule. Only the demo's four backend containers are allowed targets; any other name is rejected before a call is made.
- The page shows the current state: active LB, each backend's profile, each backend's container state, the total rate and the generators' offered/sent/dropped counters.
- Charts are embedded panels of the existing dashboard (iframes against the demo Grafana, `var-window=15s&refresh=5s`, the active LB selected).
- The control service's own port is published on `127.0.0.1` only.

### Demo script (S5.T17)

- A markdown document: an ordered scenario sequence; for each step, the click path, what to narrate (with the ADR that backs it), and the expected on-screen state; a convergence pause after every LB switch; and a recovery/reset step between scenarios.
- It must state, and never contradict, the known behaviours below (verified against the code during the grilling):
  - p2c-ewma is rank-based: it compares two random **selectable** backends and takes the lower **EWMA latency**, so with four backends of distinct EWMA the shares settle near 50 / 33 / 17 / 0 % whatever the size of the gaps, and the slowest backend receives no traffic from that LB;
  - EWMA has no decay and is fed only by proxied responses, so a backend restored to fast latency may stay starved by p2c-ewma (how the script handles this is an open decision);
  - least-connections breaks ties first-in-registry-order, so at low concurrency it concentrates on backend1; the demo rate keeps in-flight counts above zero;
  - consistent-hash-bounded sticks each client (each `RANK`) to one backend and spills when the owner's **load** exceeds **capacity**; with Zipf-skewed clients the hottest key's owner is the one that spills;
  - a flaky backend's failures drive passive outlier detection and the circuit breaker, while `/health` keeps passing.
- The recorded video follows the script. Where the video is stored is an open decision.

### Proposed, not in this spec

- S5.T18 — an `lb_p2c_ewma_seconds{backend}` gauge, recorded in `PROGRESS.md` as proposed, decided after a first recording.
- The bench `degraded`-slice leak — recorded in `PROGRESS.md` as proposed, with the unverified hypothesis, to be diagnosed before any re-run of the published matrix.

## Testing Decisions

- A good test exercises external behaviour only: HTTP in and HTTP out, or the observable effect on a running stack. Tests do not reach into unexported state, do not assert on internal call order, and do not mock what can be run for real (`httptest` servers stand in for real peers).
- Strict TDD per AGENTS.md for every Go change (T16.1, T16.2, T16.4): tests written and seen failing before implementation; `make test`, `make test-race`, `go vet ./...`, `make fmt` clean. T16.3 and T17 are config/docs work verified by the T16.5 check and by `docker compose config`; T16.5 is itself the test.

### Seams (fewest, highest)

1. **The running demo stack, observed through Prometheus** — the single highest seam and the one that matters most. The T16.5 acceptance check runs here and is the gate for T16.4 and T17. Prior art: the observability smoke script and the `docker stop` / 5xx-injection chaos scripts.
2. **The dummy backend over HTTP** — the existing seam. The admin listener is tested through its handler with `httptest`, and its effect is tested through the existing request handler: set a profile via the admin handler, then observe the latency/status of requests to the request handler. Prior art: the dummy backend's existing `/stats` and logging-switch tests, which drive the handler via `httptest` and assert only on HTTP-visible behaviour.
3. **The traffic generator over HTTP** — a new seam at the highest point available: the generator runs against an `httptest` server standing in for an LB, and is controlled through its own control endpoint. Tests assert on what the stand-in server receives (rate within tolerance over a window, size mix, target switch) and on what the control endpoint reports (counters, rejected inputs). Rate and distribution assertions use generous statistical tolerances over a fixed window to stay non-flaky under `-race`.
4. **The control service over HTTP** — tested through its JSON API, against `httptest` stand-ins for the eight generators, the four admin listeners and the Docker Engine API (a stand-in serving the few Engine endpoints the service calls). Tests assert fan-out to all eight, partial-failure reporting, the container allowlist (no Engine call is made for a disallowed name), and error propagation.

### Per-module test intent

- Admin listener: off unless enabled (strict env parsing, table-driven: unset/empty/true/false/invalid); `POST` only; unknown fields → 400; out-of-range → 400; a set profile changes request latency and status; jitter bounds respected and never negative; `/health` and `/stats` still bypass; concurrent set-while-serving is race-free; with the admin listener disabled the request handler behaves exactly as today (the existing tests stay green and are the regression gate).
- Traffic generator: share-by-rank arithmetic (table-driven over ranks and exponents); Poisson-ness is checked loosely (mean inter-arrival within tolerance; not a fixed interval); the size mix appears in the requests received; runtime rate and target changes take effect; an unknown target is rejected; the in-flight bound holds and excess arrivals are counted as dropped; bodies are drained.
- Control service: each action reaches the right peers; fan-out is total and partial failures are reported; the allowlist blocks unknown containers; the page is served.
- Acceptance check: phases 1 and 2 as specified, the restore-on-exit trap, and a negative run (the owner deliberately injects latency on two backends once to confirm phase 1 fails with a useful message), recorded in the PROGRESS entry the way S5.T6 recorded its guard negative check.

## Out of Scope

- Any public, hosted, or cloud deployment; any cloud region; DNS; TLS for public names; edge security, rate limiting, WAF or DDoS protection (ADR-0005; ADR-0023).
- Any LB code change: no hot-reloadable `algorithm`, no header-based **hash key**, no EWMA decay or probing, no new selector-internals gauges (S5.T18 is proposed separately).
- Fixing the bench `degraded`-slice leak, the cold-start peak-search guard, the narrative number check boundary bug, or the probe-counting issue — each is its own proposed ticket. The demo takes no dependency on any of them: probes go straight from the health checker to the backends and never pass through the proxy, so `lb_requests_total{backend}` already excludes them.
- Changes to the benchmark rig, its compose file, its harness, or `RESULTS.md`.
- Changes to the repo-root demo stack beyond the shared dashboard JSON (whose defaults keep it rendering as before).
- A demo dashboard JSON separate from the single shipped dashboard.
- Any frontend framework, build pipeline, or new charting code.
- Any non-loopback bind address, remote access, or authentication for the control page.
- S5.T13–T15 (README diagram, design-decisions doc, what-I'd-do-differently doc), which keep their numbers and stay proposed.

## Further Notes

### Open decisions

Resolved at ticket breakdown (2026-10-02, owner accepted the recommendations): 1 (a) — payload paths accept a discarded `POST` body; 2 — narrate starvation honestly, no fix ticket; 3 — Zipf s = 1.0 and mostly 200 B / 10 KiB with a small 1 MiB share (exact rate and bound fixed in the tickets); 4 — plain-JS page embedded in the binary; 5 — kill; 6 — omitted field keeps its value. Still open: 7 (scenario list, at the start of S5.T17.1) and 8 (video location, at the start of S5.T17.2).

1. **Request bodies vs the dummy backend (T16.1 / T16.2).** The dummy backend accepts only `GET` and answers every other method with 405. A mixed request-body size mix therefore needs the backend to accept a body. Options: (a) T16.1 also makes the payload paths accept `POST` with a body, read and discarded, leaving `GET` unchanged (bench sends only `GET`, so bench runtime behaviour is unchanged); (b) the generator sends response-size variety only, and request-body variety is dropped. Recommendation: (a), recorded in T16.1's acceptance.
2. **p2c-ewma starvation in the script (T17).** A backend restored to fast latency may never be chosen again by the p2c-ewma LB, because EWMA has no decay. Options: narrate it as a known limitation, with the script's recovery step restarting that LB (cold EWMA) or showing recovery on another algorithm; or propose a separate EWMA-decay/probe ticket. Recommendation: narrate it honestly; propose a fix only if the owner wants one.
3. **Generator defaults (T16.2).** Base total rate, Zipf exponent (recommendation `s = 1.0`), response-size weights (recommendation mostly 200 B and 10 KiB, a small 1 MiB share), request-body weights, and the in-flight bound — sized so the demo is comfortable on an 8–16 GB laptop with Docker Desktop defaults.
4. **Control-page implementation (T16.4).** Recommendation: one static HTML page with plain JavaScript `fetch` calls, embedded in the control service binary; no framework.
5. **Kill vs stop (T16.4).** `docker kill` (abrupt, mid-response connections drop — shows clean 502s per S4.T6) versus `docker stop` (graceful). Recommendation: kill, matching the Sprint 3/4 chaos tests.
6. **Admin field semantics (T16.1).** Whether an omitted field keeps or resets its value. Recommendation: keeps.
7. **Scenario list (T17).** The ordered scenarios — e.g. baseline round-robin; one far backend under each selector; Zipf hot key under consistent-hash-bounded; flaky backend and the circuit breaker; kill and revive; drain reload under load — fixed when T17 starts.
8. **Where the recorded video lives (T17).** In the repository (size), as a release asset, or as an external link from the README.

### Context carried from the grilling

- The demo's charts measure the LB's whole-request duration per backend (`lb_request_duration_seconds` by `backend`), which is what the acceptance thresholds read. The EWMA the selector uses measures the backend round trip only; the two differ by the LB's own overhead, which is small next to a 200 ms injection.
- The `degraded`-slice leak hypothesis (an exported `SLEEP_MS` drifting all four backends on a later `compose up`) is unverified. The demo avoids it by construction, and the T16.5 phase-1 assertion is the guard that the demo's own injection is isolated.
- The S5.T6 published run remains the only performance evidence. Nothing in the demo produces or changes published numbers.
