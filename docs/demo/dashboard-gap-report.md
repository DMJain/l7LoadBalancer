# Demo dashboard: gap report and frame-by-frame script

This report checks the demo dashboard specified in Addendum 3 against the repository as it stands (HEAD `632c7c2`). It lists what exists, what is missing, the tasks that would close each gap, and a frame-by-frame recording script with a dry-run checklist. Nothing here is implemented or approved. Every proposed task below needs the owner's approval before it goes into `PROGRESS.md` (`AGENTS.md`, Step 2.5).

**Status (2026-10-07):** the owner resolved every decision in §6. The approved tickets are tracked in `PROGRESS.md` (*Sprint 5 — Demo dashboard (S5.T20)*) and scoped in `.scratch/s5-t20-demo-dashboard/spec.md`. The script in §8 and the checklist in §9 use the resolved wording.

Companion: [`video-script.md`](video-script.md) is the script that can be recorded **today**, with no new work. This report's script needs the tasks in section 5.

**Input note.** Addendum 2 (the frame-table format, the gap-report phases and dry-run "Section 3b") never reached this session. Its structure is rebuilt here from the references in Addendum 3: a frame table with time, window, action, voiceover and expected state; `⚠ BLOCKED BY <task-id>` markers; and a dry-run checklist. If Addendum 2 asked for anything that Addendum 3 does not repeat, it is missing here.

---

## 1. Summary

- **Demo A (rejected config) works in the load balancer today, but the dashboard cannot show it.** Validation rejects an unknown algorithm, a duplicate backend name and a malformed URL (`internal/config/config.go:451`, `:457`, `:607–620`; tests in `internal/config/config_validate_test.go`). A rejected reload leaves the running config unchanged (`cmd/l7LoadBalancer/main.go:61–78`; `TestReloadLoopValidationFailureKeepsServing`). Two parts are missing: a way to trigger the reload from the page, and a way to display the result. The only trigger is SIGHUP and the only output is a log line. **Two REQUIRED gaps:** no admin endpoint, and no machine-readable rejection result.
- **Demo B as written cannot happen.** A reload that changes `algorithm` is rejected whole ([ADR-0015](../adr/0015-reload-architecture.md) decision 4, `internal/config/diff.go:75`), and [ADR-0023](../adr/0023-local-live-demo.md) rejects "switch algorithm by config rewrite + SIGHUP" for the demo. So frames B3–B5 ("the reload is accepted", "S2: SUCCESS", "the configuration change took effect") would be false on camera. The demo's algorithm switch moves the traffic to another load balancer process. This script narrates it as that, and S2 does not change during Demo B. The other path is to supersede ADR-0015 decision 4, which is a design change, not a dashboard task (decision 1 in section 6).
- **About half of the panels exist as metrics and five are drawn today.** The Prometheus series behind B2, B3, B4, B5 and B6 exist, and the current Grafana dashboard draws all five (`deployments/docker/observability/grafana/dashboards/l7loadbalancer.json`). B1 exists as a line chart without stacking or markers. Missing: a client-side view (C1–C3 have only load-balancer-side approximations), a "dropped" signal separate from backend 5xx, algorithm-change markers, an EWMA gauge (already proposed as S5.T18), a draining state, and both state-header lines as dashboard values.
- **Operator controls mostly exist on the control page** (`demo/control-service/main.go:384–390`). Algorithm switch, per-backend latency and stop/start are present. The invalid-reload button is entirely missing.
- **The benchmark close cannot fill its table yet.** Round-robin at 10 KiB through the load balancer was skipped in the published run, so that row would read "—". The title's "60-second runs" is wrong: the core table's figures come from 10-second attacks at the peak rate (`bench/results/core/*-throughput.txt`, `Duration … 9.99s`). There is no single "Nginx" row: Nginx ran a different algorithm against each of ours.
- **Recommended technology:** keep Grafana for every chart (ADR-0023 decision 9: no new charting code) and make the existing control page the one screen. It already embeds Grafana panels. It would gain the state header (S1, S2) and the missing controls.

---

## 2. Operator controls (zone D), gap by gap

| ID | Control | Exists today? (cite) | Gap and minimum fix |
|---|---|---|---|
| D1 | Algorithm selector (four buttons) | **Yes, as a dropdown.** "Switch active LB" re-points all eight generators: `POST /api/lb`, `demo/control-service/main.go:386`; page `demo/control-service/page.html`. | Cosmetic only: replace the dropdown with four buttons labelled with algorithm names. It is a traffic switch, not a reload (ADR-0023 decision 4), so the button labels must not say "reload". |
| D2 | Attempt reload with INVALID config | **No.** The load balancer's only listeners are the proxy, `/metrics` + pprof (`internal/app/app.go:410–415`) and `/livez` `/readyz` `/startupz` (`internal/health/endpoint.go:70–72`). Reload is SIGHUP-only (`cmd/l7LoadBalancer/main.go:52`). | **REQUIRED.** An admin endpoint on the load balancer that accepts a config and runs the existing Load → Validate → Reload path and returns the result (S5.T20.1), plus a control-page button that sends a fixed invalid body to the active load balancer (S5.T20.2). |
| D3 | Per-backend latency injection, Apply / Clear | **Yes.** Each backend row sets `sleep_ms` / `jitter_ms` / `fail_rate` → Apply: `POST /api/backend`, `main.go:388`, forwarded to the backend admin listener (ADR-0023 decision 7). | "Clear" is Apply with `0 / 0 / 0`. Optional: a one-click Clear button (S5.T20.2). |
| D4 | Per-backend stop / start | **Yes, as Kill / Revive.** `POST /api/backend/kill` and `/revive`, `main.go:389–390`. Kill is the Engine's abrupt kill (ADR-0025 amendment decision 9). | None needed. Label the buttons "Stop" / "Start" only if they keep kill semantics and the narration says "stop" (decision 6). |

### D2 end-to-end verification (the four required checks)

| Check | Result | Evidence | Status |
|---|---|---|---|
| An admin endpoint accepts a config body or path and attempts to apply it | **Missing.** No HTTP route reaches `config.Load` or `App.Reload`. | `grep HandleFunc` over `internal/` and `cmd/`: only `/metrics`, pprof, and the three health paths. | **REQUIRED gap** → S5.T20.1 |
| Validation rejects unknown algorithms, duplicate backend names and malformed URLs | **Holds.** `config: unsupported algorithm %q` (`config.go:457`), `config: duplicate backend name %q` (`config.go:451`), and URL scheme/host/query/fragment errors (`config.go:607–620`). | Table cases "unknown algorithm", "algorithm is case-sensitive", "duplicate backend names", "backend URL without scheme / host / with query / with fragment" in `internal/config/config_validate_test.go` (lines 123–205). | OK |
| The rejection returns a readable message the dashboard can display | **Missing.** The message exists (the `err` value above) but goes only to a `WARN` log line with `event=config_reload_failed`, `reason=validation_error`, `err=…` (`main.go:71–75`). Nothing returns it to a caller or exposes it as a value. | `cmd/l7LoadBalancer/main.go:61–78`. | **REQUIRED gap** → S5.T20.1 |
| The rejection leaves the running config unchanged, with no partial apply | **Holds.** A parse or validation failure `continue`s before `Reload` is called. In `Reload`, the non-backend check runs before `reg.Apply` (`internal/app/app.go:208–216`), and `loadedCfg` is replaced only after a successful apply. | `TestReloadLoopParseFailureKeepsServing`, `TestReloadLoopValidationFailureKeepsServing` (asserts the registry is still `backend-a, backend-b` and still serving), `TestReloadLoopNonBackendChangeNamingField`, `TestReloadRejectsNonBackendChange`. | OK, and the new endpoint must reuse this path, not copy it |

**Clearest invalid config for the button:** an unknown algorithm, `algorithm: "p3c_ewma"`. It produces `config: unsupported algorithm "p3c_ewma"`, which a viewer reads in one glance. A duplicate backend name is the second choice. A malformed URL is the weakest, because its message names the backend but deliberately not the URL (`config.go:600–604`).

---

## 3. Observation panels, gap by gap

"Rendered" means the panel exists in `deployments/docker/observability/grafana/dashboards/l7loadbalancer.json`, whose panels are: *Request rate by backend / status_class*, *Request latency p50 / p99 by backend*, *Circuit state*, *Backend healthy*, *Active connections by backend*, *Request share by backend (selected LB)*. All load-balancer metrics are defined in `internal/metrics/metrics.go:89–112`.

### Zone A: client-side view

| ID | Panel | Metric or endpoint exists? | Rendered today? | Gap and minimum fix |
|---|---|---|---|---|
| C1 | Total request rate (big number) | **LB side, yes:** `sum(rate(lb_requests_total[…]))`. **Client side, no:** the generators keep `sent` / `completed` counters only in a JSON status endpoint, not in Prometheus (`demo/traffic-generator/main.go:139–150, 431–446`). | No stat tile. The rate panel is per backend. | Add a stat panel on `sum(rate(lb_requests_total{job=~"lb-.*"}[$window]))` (S5.T20.4). For a true client-side number, export the generator counters (S5.T20.3). |
| C2 | Success / failed / dropped (totals + rates) | **Partly.** `lb_requests_total{status_class}` gives 2xx / 3xx / 4xx / 5xx. **"Dropped" cannot be separated:** a no-healthy-backend 503 is recorded as `5xx` with `backend=""`. A circuit-denied 503 and a transport-error 502 are recorded as `5xx` *with* the backend's name (`internal/proxy/proxy.go:273–290, 541, 548–552`), so they look the same as a backend's own 5xx. Client-side transport errors exist only as the generator's JSON `errors` counter. The generator's `dropped` counts arrivals refused by its own in-flight cap (`main.go:305–315`), which is not an LB drop. | No. | Minimum: count client-observed outcomes in the generators: 2xx+3xx, 5xx, transport error (S5.T20.3). That is the client's view and needs no load-balancer change. Totals since start come from the counters, and rates come from `rate()`. Optional reset: Grafana cannot reset a counter. Use "since the time-range start" (`increase(...[$__range])`) instead (decision 5). |
| C3 | Client-observed latency p50 / p99, last 2 min | **LB side, yes:** `lb_request_duration_seconds` histogram (buckets `.005 … 10` s, `metrics.go:55`), aggregated over backends. **Client side, no:** the generators record no latency. | Per-backend p50 / p99 only. | Add an aggregate panel, `histogram_quantile(0.99, sum by (le)(rate(lb_request_duration_seconds_bucket[$window])))`, time range 2 min, titled "as measured at the load balancer" (S5.T20.4). A client-measured histogram in the generators is S5.T20.3. |

### Zone B: per-backend view

| ID | Panel | Metric exists? | Rendered today? | Gap and minimum fix |
|---|---|---|---|---|
| B1 | Traffic distribution, stacked 0–100 %, algorithm-change markers | **Yes:** share from `lb_requests_total` by backend. **Markers: no source.** Each LB process has a fixed algorithm, and the active-LB choice lives only in the generators' `target`. Nothing records the switch as a time series. | **Partly.** *Request share by backend (selected LB)* is an unstacked line chart filtered by `$lb`, so it shows one LB at a time and goes blank on a switch unless `$lb` changes. | (1) Change the query to sum over **all** `lb-*` jobs. Only the active LB carries traffic, so the combined share is the active selector's share and continues across switches. Stack it at 100 % (S5.T20.4). (2) Markers: the control service exports `demo_active_lb{lb="…"} 1` on a `/metrics` endpoint that the demo Prometheus scrapes. A Grafana annotation query on its changes draws a labelled line at each switch (S5.T20.2 + S5.T20.4). |
| B2 | Per-backend request rate | Yes, `lb_requests_total`. | **Yes:** *Request rate by backend / status_class*. | None. Optional: drop the status_class split for readability. |
| B3 | Per-backend p99 | Yes, `lb_request_duration_seconds`. | **Yes**, as p50 / p99 lines. | Optional: a bar gauge of the current p99 per backend (S5.T20.4). Note the bucket effect: a 200 ms delay reads about 175 ms at p50 and about 250 ms at p99, because quantiles come from fixed buckets (DEMO-SCRIPT, 2a note). |
| B4 | Per-backend active connections | Yes, `lb_active_requests` (in-flight requests, `metrics.go:106`). | **Yes:** *Active connections by backend*. | None. Label it "requests in progress": it counts requests, not TCP connections. |
| B5 | Health: green / red / yellow (draining) | Healthy and unhealthy: yes, `lb_backend_healthy`. **Draining: no metric.** A removed backend's series are deleted on reload (`TestReloadSeedsAddedAndDeletesRemovedSeries`), and drain state is internal (`Backend.IsRemoved`, `RetiredContext`; [ADR-0016](../adr/0016-drain-lifecycle.md)). | **Yes:** *Backend healthy* (stat), green / red not verified as colour-mapped. | Map 1 → green "healthy", 0 → red "unhealthy" (S5.T20.4). Yellow needs a new `lb_backend_draining` gauge. This script never removes a backend, so the gauge would never show. **Out of scope** (section 7). |
| B6 | Circuit state: closed / open / half-open | Yes, `lb_circuit_state{state}` as a one-hot enum (`metrics.go:102`). | **Yes:** *Circuit state* (state timeline). | None. |
| B7 | Per-backend "load as the selector sees it" | **Partly.** `lb_active_requests` is the signal least-connections uses and that consistent-hash's bound compares against ([ADR-0009](../adr/0009-consistent-hash-bounded-loads-capacity-and-evidence.md) decision 2). **EWMA is not exported.** `Backend.EWMALatency()` exists (`internal/backend/backend.go:259`), but no metric reads it. | Active requests: yes (B4). EWMA: no. | S5.T18 (already proposed in `PROGRESS.md`: `lb_p2c_ewma_seconds{backend}`). Correction to the spec: round-robin uses **no** load signal, so B7 should read "not used" for round-robin rather than show a connection count as if it drove the choice. |

### Zone C: state header

| ID | Item | Exists? | Rendered? | Gap and minimum fix |
|---|---|---|---|---|
| S1 | "Current algorithm: …" | **Yes, on the control page:** `active: lb-…`, read live from the generators' targets (`GET /api/state`, `main.go:385`). No Prometheus series. | On the control page, as an LB name. | Show the algorithm name, mapped from the LB name, in a header line (S5.T20.2). The page re-reads state, so it updates on the next poll, not "the instant" (decision 7). |
| S2 | "Last reload: SUCCESS / REJECTED at hh:mm:ss — <error>" | **No.** Only log lines (`config_reloaded`, `config_reload_failed`). | No. | **REQUIRED for Demo A.** The LB keeps the last reload result (result, time, error text) and serves it, and the control page shows the active LB's value (S5.T20.1 + S5.T20.2). Counters `lb_config_reload_total{result}` and a last-reload timestamp gauge are optional extras for Grafana. The error text cannot be a metric label. |

---

## 4. Recommended dashboard technology

**Recommendation: one screen made of the control page, which hosts the state header and the controls, and embedded Grafana panels for every chart. The Grafana dashboard stays the single source for charts.**

- ADR-0023 decision 9 already settles this pattern: "Charts are embedded panels of the existing Grafana dashboard; there is no new charting code." The control page already embeds three panels at `var-window=15s&refresh=5s`. Adding more embedded panels breaks no decision.
- Zones A and B are Prometheus queries. Grafana draws stacked percentages, stat tiles, colour-mapped state, and annotations from a Prometheus query natively. Re-implementing those in plain JavaScript would be new charting code and a new ADR.
- Zones C and D are about *the demo's* state: which LB is active, and the last reload attempt. The control service already owns that view (ADR-0025 decisions 1 and 10), and it has no Prometheus dependency to add.
- What it costs: two systems on one screen, and an iframe's 5 s refresh sets how fast the charts react. The header updates on the page's own poll.

**Rejected:**
- A Grafana-only dashboard. The buttons would need a Grafana plugin or links out to the control service, and Grafana cannot show a free-text error message from the LB without a JSON datasource plugin.
- A custom single page that draws its own charts from the Prometheus HTTP API. That is new charting code, which contradicts ADR-0023 decision 9, for no gain the viewer would see.

**Open question:** the dashboard JSON is shared with the root stack and mounted, not copied (ADR-0023 decision 9). New panels either go into that shared file (the root stack gains them too) or into a demo-only dashboard file (decision 4).

---

## 5. Proposed new sprint tasks

All proposed, none approved. IDs are suggestions that continue the Sprint 5 numbering. `S5.T18` is the existing proposal in `PROGRESS.md`, reused rather than duplicated.

| ID | Task | Touches | Needs ADR | Blocks frames | Depends on |
|---|---|---|---|---|---|
| **S5.T20.1** (REQUIRED) | **LB reload admin endpoint and last-reload status.** `POST /admin/reload` on the LB's internal metrics listener (never published, ADR-0023 decision 2). It takes a YAML body and runs the *same* parse → Validate → `App.Reload` path as SIGHUP, through one shared function rather than a copy. It returns `200` with the added/removed/unchanged counts, or `422` with the error text. `GET /admin/reload/last` returns `{result, time, error}`. Tests first: unknown algorithm, duplicate name, malformed URL and non-backend change each return `422` with the message and leave the registry unchanged; a valid body returns `200`; SIGHUP and the endpoint compose. | `cmd/l7LoadBalancer`, `internal/app`, possibly `internal/metrics` | **Yes.** It adds a second reload trigger and must say whether a body-applied config may diverge from the file on disk (decision 2). Amends ADR-0015 and ADR-0013. | R1–R6 (Demo A), S2 in every frame | — |
| **S5.T20.2** | **Control page: state header and controls.** S1 algorithm line; S2 last-reload line read from the active LB's `GET /admin/reload/last`; an "Attempt reload with invalid config" button that posts a fixed `algorithm: "p3c_ewma"` body to the active LB; four algorithm buttons; one-click Clear per backend. The control service also serves `/metrics` with `demo_active_lb{lb}` (stdlib text format, no new dependency), scraped by the demo Prometheus. | `demo/control-service`, `demo/prometheus` | Amends ADR-0025: new peer set (the four LB admin listeners), a fixed invalid body (no caller-supplied config, the same allowlist idea as decision 8), and `/metrics`. | T1–T3, R1–R6, B-frames' header checks | S5.T20.1 |
| **S5.T20.3** (deferred 2026-10-07) | **Client-side metrics in the generators.** `/metrics` on each generator: requests by outcome (2xx+3xx, 4xx, 5xx, transport error), the cap-drop counter, and a client-measured latency histogram, scraped by the demo Prometheus. | `demo/traffic-generator`, `demo/prometheus` | Amends ADR-0024. | C1–C3 as true client-side views (the frames fall back to LB-side values without it) | — |
| **S5.T20.4** | **Dashboard panels for zones A and B.** C1 stat; C2 three stats with rates; C3 aggregate p50/p99 over 2 min; B1 stacked 100 % share summed over all `lb-*` jobs, with an annotation on `demo_active_lb` changes; B3 p99 bar gauge; B5 colour mapping; B7 row (active requests, plus EWMA once S5.T18 lands). The control page embeds the new panels. | Dashboard JSON (shared or demo-only, decision 4), `demo/control-service/page.html` | Amends ADR-0023 decision 9 only if a demo-only dashboard file is chosen. | O1, T1–T3, B1–B6, F1–F4 | S5.T20.2 (markers), S5.T20.3 (client-side C1–C3, optional) |
| **S5.T18** (existing proposal) | `lb_p2c_ewma_seconds{backend}` gauge. | `internal/metrics`, latency path, ADR-0013 | Yes (as already noted in `PROGRESS.md`) | B7 under p2c-ewma (B4 frame line) | — |
| **S5.T20.5** | **Benchmark summary page.** One static HTML file, opened from disk in a browser tab: a title and one table, 4 algorithm rows plus the Nginx row chosen in decision 3, columns peak req/s, p50, p99 and p99.9 in ms, best cell per column highlighted, text at least 32 px. Numbers copied from `RESULTS.md`'s core table, with a check that each one matches a generated cell (the `bench/generate-results.sh` narrative check could be pointed at it). | `docs/demo/` | No | Z1–Z2 | Decision 3; S5.T20.6 if the round-robin row must be filled |
| **S5.T20.6** | **Fill the round-robin 10 KiB LB row.** It was skipped as "no sustainable rate at the seed rate" (`RESULTS.md`, Limitations). It needs the existing "cold-start guard for the first peak-search step" proposal and then a re-run. A targeted re-run breaks the single-commit provenance of `bench/results/provenance.json` unless the whole matrix is re-run (decision 3). | `bench/` | No | Z2 without a "—" | Cold-start guard proposal |

Lowest-effort format for S5.T20.5: a static HTML file. A rendered image needs a tool chain, and a Grafana panel needs a datasource for a saved dataset, which Grafana 11.1 lacks without a plugin.

---

## 6. Decisions you need to make

**Resolved by the owner on 2026-10-07:** 1(a) traffic switch, script says "switch to the P2C load balancer" and S2 does not change in Demo B; 2(a) YAML body, internal metrics listener only, file not authoritative until the next SIGHUP; 3 no Nginx row, round-robin row filled by S5.T20.6 followed by a full-matrix re-run, title fixed; 4 demo-only dashboard file; 5 no reset, `increase(...[$__range])`; 6 keep the abrupt kill, labelled "Stop"; 7 200 ms; 8 approved in the order S5.T20.1, S5.T20.2, S5.T20.4, S5.T18, S5.T20.5, S5.T20.6, with S5.T20.3 deferred. The options below are kept as the record.

1. **Demo B framing.** (a) *Recommended:* keep ADR-0015 decision 4. Demo B is a traffic switch between four running load balancers, S2 does not change in Demo B, and the voiceover says "switch", never "reload". (b) Make `algorithm` reloadable by superseding ADR-0015 decision 4. That needs a new ADR and a sprint of selector-state work (ring rebuild, EWMA carry-over, admission). It is not a dashboard task.
2. **What the reload endpoint accepts** (S5.T20.1). (a) *Recommended:* a YAML body, with the ADR stating that after a body-applied reload the file on disk is no longer the source of truth until the next SIGHUP. (b) No body: re-read the mounted file. Then the invalid-config button needs a second mounted "candidate" file and a way to write it, which adds more parts.
3. **Benchmark table rows.**
   - Nginx: ran a different algorithm against each of ours. Pick one: (a) *Recommended:* drop the Nginx table row and keep Nginx in the spoken HTTP/1.1 sentence. (b) Nginx least_conn at 10 KiB (matched: 2500 req/s, p50 0.655, p99 26.741, p99.9 55.744 ms). (c) Nginx round-robin at 10 KiB (2500 req/s, p99 1410.277 ms), which `RESULTS.md` calls the least trustworthy figure in the matrix.
   - Round-robin row: show "—" with a footnote, or run S5.T20.6 first.
   - Title: replace "60-second runs" with the truth: "10 KiB responses, 4 backends, HTTP/2, 10-second runs at each peak rate".
4. **Where new panels live:** in the shared dashboard JSON (the root stack gains them) or in a demo-only dashboard file (ADR-0023 decision 9 amended).
5. **C2 reset button.** Prometheus counters cannot be reset from a dashboard. Accept "since the start of the time range" (`increase`), or drop the reset.
6. **Stop vs kill.** The control page's Kill is abrupt (ADR-0025 amendment decision 9). Keep it and say "stop" in narration, or add a graceful stop endpoint, which is a further ADR-0025 amendment.
7. **Latency to inject: 200 ms, not 500 ms.** *Recommended: 200 ms.* `demo/acceptance.sh` verifies 200 ms isolation, and 200 ms lands in the 0.1–0.25 s bucket (p50 reads about 175 ms). A 500 ms delay puts every slow request just above the 0.5 s boundary, so the quantiles interpolate inside the 0.5–1 s bucket. On screen, p99 would read close to 1 s, about twice the injected value. The script below uses 200 ms.
8. **Approve, reorder or reject** S5.T20.1–T20.6 and S5.T18. The script in section 8 assumes all of them land.

---

## 7. Out of scope

- **Algorithm reload by SIGHUP or endpoint** (unless decision 1(b)).
- **Draining (yellow) state in B5.** No frame removes a backend. Showing it needs a `lb_backend_draining` gauge and a drain-reload frame, and the bench already proves zero-drop draining (`RESULTS.md`, drain reload PASS).
- **A Grafana "reset counters" action** (decision 5).
- **Public deployment** (ADR-0023 decision 1).
- **Re-running the benchmark matrix** to change any published number, beyond the optional S5.T20.6.
- **The `docs/resume/l7lb-bullets.md` file in commit `632c7c2`.** It is unrelated to the demo but should be resolved before the repository link is published.

---

## 8. Demo script, frame by frame

Target: 6:40. The take is continuous and trimmed only at the ends. Voiceover follows ASD-STE100: at most 20 words per sentence, active voice, each technical term defined on first use. "Dashboard" means the control page with its embedded panels (section 4). The Grafana tab is not used. Expected states give a number or a direction that can be checked. Where the number is only known at record time, the cell gives the rehearsal value from `demo/DEMO-SCRIPT.md` and a placeholder.

**Before the take** (dry-run checklist, section 9): rate 400 req/s, round-robin active, backend3 at `sleep_ms 0`, all four backends healthy, and one prior invalid-reload attempt **not** made, so that S2 reads "no reload yet" at the start.

### Opening (0:00–0:30)

| # | Time | Window | Action | Voiceover | Expected state |
|---|---|---|---|---|---|
| O1 | 0:00–0:30 | Dashboard, full screen | Pointer still. On "4 backends", trace the four B1 bands once. | This is a Layer 7 load balancer that I built in Go. Layer 7 means that it reads each HTTP request before it chooses where to send it. Now, 8 test clients send traffic through it to 4 backends. A backend is a service that does the work for a request. I built it to show how I design, test, and measure a backend system. | S1 reads "round-robin". C1 reads `<pull from dashboard at record time>` req/s (rehearsal: about 370 of 400 configured). B1 shows 4 bands of 25 % ± 3 points each. B5: 4 green. ⚠ BLOCKED BY S5.T20.2, S5.T20.4 |

### What it is (0:30–1:00)

| # | Time | Window | Action | Voiceover | Expected state |
|---|---|---|---|---|---|
| W1 | 0:30–1:00 | GitHub README, top | Scroll slowly through the first paragraph. | A load balancer sends each request from a client to one of several backends. A selection algorithm decides which backend gets each request. This one has 4 selection algorithms. It checks the health of each backend. A circuit breaker stops traffic to a backend that keeps failing. It gives the backend time to recover. The load balancer can also reload its backend list while it runs. | README visible; no dashboard dependency. |

### Architecture (1:00–2:15)

| # | Time | Window | Action | Voiceover | Expected state |
|---|---|---|---|---|---|
| A-1 | 1:00–1:20 | Split: README "How a request flows" diagram (left), dashboard (right) | Move the pointer down the diagram: client, handler, selector, reverse proxy, backend. | Follow one request from the top of the diagram. The handler asks the selector for a backend. The selector is the part of the code that runs the selection algorithm. The Go reverse proxy then sends the request to that backend. | Diagram readable at 150 % zoom. |
| A-2 | 1:20–1:40 | Same | Point at "selector" in the diagram, then at B1. | First decision: the selector is an interface. Each algorithm implements it. So I can add an algorithm with no change to the request path. | — |
| A-3 | 1:40–2:00 | Same | Point at `active count +1` and `body closed, count -1`. | Second decision: the handler counts the requests in progress on each backend. The count goes down when the response body closes. A guard makes sure that this happens exactly once. If it happened twice, least-connections would read the wrong count. | — |
| A-4 | 2:00–2:15 | Same | Point at B5 and B6 on the dashboard. | Third decision: health checks and the circuit breaker each keep their own flag. A backend gets traffic only if both flags allow it. So the two parts never overwrite each other. | B5: 4 green. B6: 4 "closed". |

### Dashboard tour (2:15–3:00)

| # | Time | Window | Action | Voiceover | Expected state |
|---|---|---|---|---|---|
| T1 | 2:15–2:30 | Dashboard, full screen | Circle the header (S1, S2), then Zone A. | The top line shows the active algorithm and the result of the last reload. The first row shows what the clients see. It shows the request rate, the successful and failed requests, and the latency. | S1 "round-robin". S2 "no reload yet". C2 failed = 0 and its rate = 0/s. ⚠ BLOCKED BY S5.T20.2, S5.T20.4 |
| T2 | 2:30–2:50 | Same | Point at B1, then B2–B6 in turn. | The center shows what the load balancer sees for each backend. This chart shows the share of requests that each backend gets. These panels show the rate, latency, requests in progress, health, and circuit state of each backend. p99 latency is the time that 99 of every 100 requests stay under. | B1 4 bands at 25 % ± 3. B3 p99 of every backend below 25 ms (rehearsal p50 about 2.5 ms). ⚠ BLOCKED BY S5.T20.4 |
| T3 | 2:50–3:00 | Same | Point at Zone D. | The controls are at the bottom. I use them for every change in this video. | Zone D shows 4 algorithm buttons, the invalid-reload button, and 4 backend rows. ⚠ BLOCKED BY S5.T20.2 |

### Reload demo A: rejected (3:00–3:45)

| # | Time | Window | Action | Voiceover | Expected state |
|---|---|---|---|---|---|
| R1 | 3:00–3:15 | Dashboard | Point at S2. | The load balancer checks every new config before it uses it. It rejects a bad config. The running config stays active. | S2 "no reload yet". ⚠ BLOCKED BY S5.T20.1, S5.T20.2 |
| R2 | 3:15–3:25 | Dashboard | Click **Attempt reload with invalid config**. | This config names an algorithm that does not exist: p3c_ewma. | Button acknowledges within 1 s. ⚠ BLOCKED BY S5.T20.1, S5.T20.2 |
| R3 | 3:25–3:40 | Dashboard | Point at S2, then S1, then B1. | The load balancer rejected the reload. The error names the unknown algorithm. The algorithm is still round-robin. The traffic split did not change. | S2 reads `REJECTED at <hh:mm:ss> — config: unsupported algorithm "p3c_ewma"`. S1 still "round-robin". B1 still 4 bands at 25 % ± 3. C2 failed rate still 0/s. ⚠ BLOCKED BY S5.T20.1, S5.T20.2 |
| R4 | 3:40–3:45 | Dashboard | **Silent, 5 s.** Pointer rests on B1. | *(silence)* | No change in any panel. |

### Reload demo B: algorithm switch (3:45–5:00)

The algorithm changes by moving the traffic to another load balancer process, not by a reload (section 1; decision 1(a)). S2 does not change in this group.

| # | Time | Window | Action | Voiceover | Expected state |
|---|---|---|---|---|---|
| B1 | 3:45–3:55 | Dashboard | Point at the four backend rows. | Next, I add a delay to one backend. The demo runs 4 load balancers, one for each algorithm. Then I switch the traffic to a different load balancer. The 4 backends stay the same. Only the choice of backend for each request changes. | Every backend row at `sleep_ms 0`. |
| B2 | 3:55–4:15 | Dashboard | backend3 row: `sleep_ms 200` → **Apply**. Wait 20 s. Point at B3, then B1, then C3. | Now backend3 adds a 200 millisecond (ms) delay to each response. Round-robin ignores latency. So backend3 still gets a quarter of the requests. The slow backend raises the latency that the clients see. | B3: backend3 p99 rises to 200–250 ms (bucket effect); others stay below 25 ms. B1 still 4 bands at 25 % ± 3. C3 p99 rises above 150 ms. B4: backend3 about 17 requests in progress (rehearsal 2a). ⚠ BLOCKED BY S5.T20.4 (C3) |
| B3 | 4:15–4:25 | Dashboard | Click **P2C-EWMA**. Start a stopwatch. | Now I switch to the P2C load balancer. P2C means power-of-two-choices. It picks 2 backends at random and sends the request to the faster one. EWMA is a moving average of recent latency, and P2C uses it to compare them. | S1 changes to "p2c-ewma" within one page poll. A marker labelled "p2c-ewma" appears on B1. S2 unchanged. ⚠ BLOCKED BY S5.T20.2, S5.T20.4 |
| B4 | 4:25–4:45 | Dashboard | Wait until the stopwatch reads 30 s (this frame and B5 overlap the wait). Point at B1, then B7, then C3. | The average comes only from requests that this load balancer sends. So it needs samples before it acts. Backend3's band gets smaller. The other 3 backends share its traffic. The latency that the clients see goes down. | By 30–45 s after B3: B1 backend3 band below 5 % (rehearsal 0.0 %), the other three at 33 % ± 4 (rehearsal 32.2 / 34.2 / 35.3). B7 shows backend3's EWMA above 150 ms, the others below 25 ms. C3 p99 falls below 100 ms. Read actual values: `<pull from dashboard at record time>`. ⚠ BLOCKED BY S5.T20.4, S5.T18 (B7) |
| B5 | 4:45–4:55 | Dashboard | Point at the left of the marker on B1, then the right. | Same 4 backends. Same delay. Round-robin sent 25 percent to the slow backend. Power-of-two-choices sent `<pull from dashboard>` percent. I switched load balancers with no restart. The clients saw no errors. | C2 failed and transport-error counts did not increase between B3 and B5. The generator `dropped` counter did not increase. If either increased, cut the sentence "The clients saw no errors". ⚠ BLOCKED BY S5.T20.3 (client counts) |
| B6 | 4:55–5:00 | Dashboard | **Silent, 5 s.** Pointer rests on the B1 marker. | *(silence)* | B1 shape steady. |

### Failure mode (5:00–5:40)

Uses the +10 s allowance: ejection and reinstatement each need about 10–15 s (DEMO-SCRIPT Scenario 5).

| # | Time | Window | Action | Voiceover | Expected state |
|---|---|---|---|---|---|
| F1 | 5:00–5:05 | Dashboard | Click **Round Robin**. backend3 row → **Clear**. | Now I return to round-robin and remove the delay. | S1 "round-robin". backend3 `sleep_ms 0`. |
| F2 | 5:05–5:20 | Dashboard | backend1 row → **Stop**. Point at B5, then B6, then B1. | Now I stop backend1. The health check fails, and the load balancer marks backend1 red. The other 3 backends take its traffic. | B5 backend1 red within 5 s. B1 backend1 band reaches 0 % within 15 s, the other three at 33 % ± 4. B6 backend1: read it, and say nothing about the circuit unless it shows "open" (not observed in rehearsal for a kill). C2 failed count rises by a small number (rehearsal: 5). |
| F3 | 5:20–5:25 | Dashboard | Point at C2. | A few requests were in progress on backend1. Those requests failed. The load balancer never retries a request, because a retry can run it twice. | C2 failed total = previous + `<pull from dashboard>`, and its rate returns to 0/s. ⚠ BLOCKED BY S5.T20.4 |
| F4 | 5:25–5:40 | Dashboard | backend1 row → **Start**. Point at B5, then B1. | Now I start backend1. After 2 successful health checks, it turns green. Its share goes back to 25 percent. | B5 backend1 green at about 10 s. B1 backend1 at 25 % ± 3 by about 20 s. If not back by 5:40, say the last sentence when it is, and start Z1 late. |

### Benchmark close (5:40–6:40)

| # | Time | Window | Action | Voiceover | Expected state |
|---|---|---|---|---|---|
| Z1 | 5:40–5:50 | Benchmark summary page, full screen | Switch tab. | These are the benchmark results for the 4 algorithms. Each ran with the same workload and the same 4 backends. | Title reads "L7 Load Balancer — 4 algorithms, 10 KiB responses, 4 backends, HTTP/2, 10-second runs at each peak rate". One table, 4 rows, no Nginx row, no scrolling. ⚠ BLOCKED BY S5.T20.5 |
| Z2 | 5:50–6:20 | Same | Point at each number as it is said. | Power-of-two-choices reached the highest peak, at 4,000 requests per second. It also had the lowest p99 latency, at 8.2 milliseconds. Consistent hashing reached only 1,500 requests per second. The test client had one address, so one backend took most of its traffic. That is the cost of sending each client to the same backend. | Rows from `RESULTS.md` core table, 10 KiB, load balancer: p2c-ewma 4000 req/s, p50 1.154, p99 8.199, p99.9 13.949 ms; least-conn 3000, 1.292, 10.735, 48.082; consistent-hash 1500, 0.484, 10.252, 24.441; round-robin "—" (skipped). Hot-key share 82.27 % at 10 KiB (`RESULTS.md`, Hot key). ⚠ BLOCKED BY S5.T20.5; round-robin row BLOCKED BY S5.T20.6 |
| Z3 | 6:20–6:40 | Same, or the README "Headline results" table | Point at the Nginx sentence or row. | I also compared it with Nginx, a widely used proxy. Over HTTP/1.1 with small responses, Nginx reached 11,500 requests per second. Mine reached 8,000. The code and the results are on GitHub, at DMJain slash l7LoadBalancer. This project shows how I work: I write down each decision and test each claim. | README 200 B row: Nginx 11500, load balancer 8000 req/s (`RESULTS.md` protocol table). Stop recording 3 s after the last word. |

**Voiceover check** (script-checked). The longest sentence has 16 words ("Layer 7 means that it reads each HTTP request before it chooses where to send it."). Terms are defined on first use: Layer 7 and backend (O1), selection algorithm and circuit breaker (W1), selector (A-1), p99 (T2), ms (B2), P2C and EWMA (B3). No banned word or phrase from the addendum appears, and every sentence is in active voice. The spoken text is 806 words, about 5:22 at 150 words per minute. The two silent frames, the 30 s convergence wait in B3–B5 and the health waits in F2–F4 bring the take to about 6:40.

**Numbers in the voiceover and their sources:** 8 clients and 4 backends (`demo/docker-compose.yml`); 200 ms (`demo/acceptance.sh`, DEMO-SCRIPT); 4,000 req/s, 8.2 ms, 1,500 req/s (`RESULTS.md` core table, 10 KiB, load balancer); 11,500 and 8,000 req/s (`RESULTS.md` protocol table, 200 B). The p2c share in B5 and the failed count in F3 are read off the screen.

---

## 9. Dry-run checklist

Run this 30 minutes before recording, after `make demo-up`. Each row is one action and one check. The rows marked ⚠ need the named task first.

| # | Step | Verify |
|---|---|---|
| 1 | `docker compose -f demo/docker-compose.yml ps --format '{{.Name}}\t{{.Status}}'` | 18 rows, all `Up`; four `lb-*` rows `(healthy)`. |
| 2 | `demo/acceptance.sh` | Ends with `All checks passed.` Do not record otherwise. |
| 3 | Open the dashboard (control page) at 125 % zoom. | S1 "round-robin". B1 4 bands at 25 % ± 3. B5 4 green. B6 4 closed. ⚠ S5.T20.2, S5.T20.4 |
| 4 | Click **Attempt reload with invalid config**. | S2 shows `REJECTED at <time> — config: unsupported algorithm "p3c_ewma"`. S1 unchanged. B1 shape unchanged (4 bands at 25 % ± 3). ⚠ S5.T20.1, S5.T20.2 |
| 5 | backend3 → `sleep_ms 200` → Apply; wait 25 s. | B3 backend3 p99 200–250 ms; others below 25 ms. B1 still 25 % ± 3. |
| 6 | Click **P2C-EWMA**; wait 45 s. | B1 marker "p2c-ewma" present. backend3 band below 5 %; others 33 % ± 4. C2 failed rate 0/s. ⚠ S5.T20.2, S5.T20.4 |
| 7 | Click **Round Robin**; backend3 → Clear. | B1 back to 25 % ± 3 within 20 s. |
| 8 | backend1 → Stop; wait 20 s; backend1 → Start; wait 30 s. | B5 backend1 red, then green. B1 backend1 0 %, then 25 % ± 3. Note whether B6 showed "open". |
| 9 | Open the benchmark summary page in a browser tab at full screen, 1080p. | All 4 algorithm rows have numbers (no "—"). There is no Nginx row (decision 3). The title matches Z1. The smallest text is readable from 1 m away. ⚠ S5.T20.5; the round-robin row needs S5.T20.6 |
| 10 | **Reset S2 for the take:** restart the four LBs (`docker compose -f demo/docker-compose.yml restart lb-roundrobin lb-leastconn lb-consistent-hash lb-p2c-ewma`) so that S2 reads "no reload yet". Then switch to round-robin and wait 60 s. | S2 "no reload yet". B1 25 % ± 3. **Side effect:** p2c-ewma's average is now cold. Its first seconds after B3 are a random split (ADR-0023 decision 5), so keep the 30 s wait in B4. |
| 11 | Restart the timer, and check rate 400 and every backend `0 / 0 / 0`. | Control page shows `now: 400 req/s`; generator `offered` ≈ `sent`, `dropped` 0. |
| 12 | Audio check: read O1 aloud and play it back. | No clipping; no hum. |

If any ⚠ task has not landed, record [`video-script.md`](video-script.md) instead. It needs none of them.
