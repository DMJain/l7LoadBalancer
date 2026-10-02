# S5.T14.1 — Evidence dossier for `docs/design-decisions.md`

Status: **owner-approved 2026-10-02: every item approved with its stated recommendation; the five decisions below are accepted as the recommended option.** Every item below needs an answer: **approve**, **edit**, or **strike**. The answer goes in the *Owner answer* line of that item. No prose for the design document is written until this is done (ticket 02, ticket 03).

Verified against the working tree at `eaa52f7` on 2026-10-02. Nothing was run or measured; every figure was read from code, tests, ADRs or retained benchmark output. Line numbers are for this commit.

Recommendation key: **use** · **use with caveat** · **drop**.

## Findings that change an approved claim (read these first)

1. **C4 and C3: the load generator did not deliver 1750 req/s.** The failure runs ask for a target of 1750 req/s, but the retained output shows the achieved rate was lower, and it fell throughout the run.
   - Backend-kill run: 72,688 requests in 60 s, an average of **1211.45 req/s** (`bench/results/failure/roundrobin-10kb-backend-kill.txt`, the `Requests` line).
   - In its time series the cumulative average falls from 1752 req/s at 0.5 s to 1485 at 6.3 s, 1327 at 30 s and 1211 at 60 s (`…-backend-kill-timeseries.txt`). The last 30 s therefore averaged about 1098 req/s.
   - No-op reload: 1121.15 req/s average. Drain reload: 1136.75 req/s average.
   - Every request that was sent succeeded (100% apart from the 3), so the figures are valid. But "at 1750 req/s" is wrong as a description of what was delivered.
   - **Recommend:** say "a target of 1750 req/s (the generator delivered an average of about 1210 req/s)" wherever C4 appears, and the same for C3 with its own averages. This is consistent with the C2 note that the generator is the limit.
2. **C4: the 3500 req/s peak is not in any retained output** (see item 2.2).
3. **C4: the ejection mechanism is not settled by the evidence** (see item 2.1).
4. **C5: "spill" is a classification, not a large shift.** In 12 of the 15 core load-balancer rows the owner backend still takes more than 70% of requests (for example 98.73% at 10 KiB and 30% load, marked "spill yes"). C5's example row (82.27% / 17.19%) is the largest spill in the table. The document must not say the bounded ring "rebalances" the hot key; it spills a minority of it.
5. **Third-party dependencies.** `go.mod` has five direct requirements: `client_golang`, `client_model`, `testify`, `x/net`, `yaml.v3`. `client_model` and `testify` are imported only from `_test.go` files. The three-dependency statement is correct for non-test code. Recommend adding "plus `testify` and `client_model` in tests" to the design document, not the README.

---

## 1. Concurrency primitives (topic 7)

Search: `grep` over `internal/` and `cmd/` for `atomic.`, `sync.`, `make(chan`, `CompareAndSwap`, `context.WithCancelCause`, `context.AfterFunc`, `WaitGroup`, non-test files. The first line of the Reason column is the project's own stated reason.

| # | Primitive | Use site (file:line) | Why the project chose it (source) | Rec. |
|---|---|---|---|---|
| 1.1 | Atomic counter (increment) | `internal/balancer/roundrobin.go:23` `counter atomic.Uint64` | Round-robin needs only a rotating index; no lock on the hot path (contracts table; INDEX: "atomic uint64 counter, modulo len(healthy). No mutex.") | use |
| 1.2 | Atomic counter (in-flight gauge) | `internal/backend/backend.go:118` `active atomic.Int64`, written by `IncActive` at `internal/proxy/proxy.go:93` | Written by the proxy on dispatch, read by least-connections and bounded loads; atomics because many request goroutines write (contracts table, row `Backend.active`) | use |
| 1.3 | Atomic flags | `internal/backend/backend.go:117` `healthy atomic.Bool`, `:121` `removed atomic.Bool` | One writer class per flag, many readers; the circuit never writes `healthy` (ADR-0011 d1) | use |
| 1.4 | Atomic pointer: immutable snapshot swap (backend set) | `internal/backend/registry.go:52` `snap atomic.Pointer[registrySnapshot]` | Readers load once and never lock; reload builds a new snapshot and swaps it (ADR-0015; contracts table, row `Registry`'s backend set) | use |
| 1.5 | Atomic pointer: loaded config | `internal/app/app.go:69` `loadedCfg atomic.Pointer[config.Config]` | Holds the last applied config; the reload operation replaces it last (contracts table, row `config.Config`) | use |
| 1.6 | Compare-and-swap: lock-free EWMA update | `internal/backend/backend.go:248` inside `RecordLatency` (`:241`–`:252`) | "so the lock-free read path P2C depends on stays lock-free" (comment above `RecordLatency`; ADR-0010) | use |
| 1.7 | Compare-and-swap: circuit breaker state | `internal/backend/backend.go:286`, `:345`, `:375`, `:403`, `:436`, `:442`, `:449` (`b.circuit.CompareAndSwap`) on `circuit atomic.Pointer[circuitSnapshot]` (`:120`) | State is one immutable snapshot replaced as a unit; a separate timestamp atomic would race the transition. A mutex was rejected (ADR-0012 d3 and its Alternatives) | use |
| 1.8 | Compare-and-swap: consistent-hash ring cache | `internal/balancer/consistent_hash.go:44` (`cache atomic.Pointer[ringCache]`), CAS at `:88` | Rebuilds the ring only when the backend set changes, without a lock (ADR-0008/0015; confirm wording from the ADR when writing) | use with caveat: reason not read from an ADR yet |
| 1.9 | Mutex | `internal/health/outlier.go:67` `mu sync.Mutex` | Per-backend outcome windows; "held only for the window update and one atomic store, never across I/O" (doc comment above `OutlierDetector`) | use |
| 1.10 | Mutex | `internal/health/checker.go:83` `mu sync.Mutex` | Guards the map of running probers so a concurrent add and remove cannot corrupt it; off the request path (comment at `:78`–`:81`) | use |
| 1.11 | Exactly-once release | `internal/proxy/proxy.go:74` `once sync.Once`, used at `:102` `s.once.Do` | The active-connection decrement must happen exactly once however the response ends (ADR-0007) | use |
| 1.12 | Context-based join | `internal/proxy/proxy.go:304` `context.WithCancelCause`, `:307` `context.AfterFunc`; created at `internal/backend/registry.go:93` | A removed backend's retired context cancels its in-flight requests at the drain-window end, with no goroutine per request and no cancel registry (comment at `proxy.go:299`–`:311`; ADR-0016 d3) | use with caveat: ADR-0016's Consequences (line 184) says "one goroutine per in-flight request is the cost"; the code comment says none. Reconcile before writing (`AfterFunc` starts a goroutine only when the context fires) |
| 1.13 | Channel: signal queue | `cmd/l7LoadBalancer/main.go:206` `make(chan os.Signal, reloadSignalBuffer)`; buffer constant at `:33`–`:38` | Depth 1 so a burst of SIGHUPs collapses into one pending reload (ADR-0015 d1) | use |
| 1.14 | Channel: first-error collection | `internal/app/app.go:294` `errCh := make(chan runError, 3)` | One slot per server so no serve goroutine blocks; `Run` is the only reader and owns shutdown (comment at `:283`) | use |
| 1.15 | Channel: goroutine-exit signal | `internal/health/checker.go:174` `done: make(chan struct{})`, closed at `:182`, awaited at `:200` | `Remove` waits so a removed backend's probe cannot write a log line or gauge after its series is deleted (comment on `proberHandle`) | use |
| 1.16 | Ticker | `internal/app/drain.go:73` (100 ms poll), `internal/health/checker.go:343` | Drain polls `ActiveConns` until zero (contracts table, row `App` drain goroutines) | use only if the document mentions polling |
| — | `sync.RWMutex`, `sync.WaitGroup`, `errgroup` | **not found — do not claim** | — | — |

Test-only synchronisation (not claimed): `-race` runs in `make test-race`.

**Owner answer:** approved as recommended (2026-10-02).

## 2. C4 — backend-stop run

### 2.1 Which mechanism ejected the stopped backend

What the evidence shows:

- 3 errors, all 502, all within 9 ms (29.734 s to 29.743 s), then none (`backend-kill.txt`, `measurements:` line).
- Constants, read from code: the circuit breaker opens after **3 consecutive failures** (`internal/circuit/circuit.go:16` `circuitFailuresBeforeOpen = 3`; ADR-0012 d6). Passive outlier detection ejects after **5 failures in a window of 10** (`internal/health/outlier.go:21`, `:29`). The active check needs **3 consecutive failed probes** at a **5 s** interval (`internal/config/config.go:64`; the benchmark config sets no override), so it cannot act within 9 ms.
- So the 3 errors are exactly what the circuit breaker's threshold predicts, and they are too few for passive ejection and too fast for the active probe.

What the evidence does not show: no retained output records a circuit transition, a health transition, or a metrics scrape for that run. The load balancer's logs and `lb_circuit_state` series were not kept. The match is an inference from three constants.

**Recommendation: use with caveat.** Cite ADR-0018 (no retry, so the 3 reached the client) and the observed figures. If the mechanism is named, say "consistent with the circuit breaker opening after three consecutive failures (ADR-0011, `circuitFailuresBeforeOpen = 3`); the run did not record the transition". Do not state it as observed.

**Owner answer:** approved as recommended (2026-10-02).

### 2.2 The 3500 req/s peak

Not in any retained output. The core slice skipped the h2 round-robin 10 KiB load-balancer scenario (C1), so `bench/results/core/` has no throughput file for it. `provenance.json` and `RESULTS.md` do not print it. The failure run finds its own peak at run time and logs it only to the console (`bench/run.sh` around the `discover_peak` call in the failure slice; `FAILURE_RATE_PCT=50` at `:135`). The figure is **inferred**: target rate 1750 ÷ 0.5 = 3500. Given finding 1 (delivered ~1210 req/s), "half of a 3500 req/s peak" describes the *target*, not what ran.

**Recommendation: use with caveat.** Write "a target of 1750 req/s, set at half of the peak the run found" and either drop 3500 or label it "inferred, not retained". Preferred: drop 3500.

**Owner answer:** approved as recommended (2026-10-02).

### 2.3 The "about 0.7 s after the stop" figure

Stop at 29 s, first error at 29.734 s: 0.734 s. Read straight from the `measurements:` line, so safe. It measures when the first request hit the dead backend (round-robin reaches it within a few requests), not how long the load balancer took to notice. **Recommendation: use with caveat** — say "the first failed request came 0.73 s after the stop command was issued".

**Owner answer:** approved as recommended (2026-10-02).

## 3. N1 — bounded vs unbounded hot-key figures (topic 2)

| Quantity | Value | Source |
|---|---|---|
| Workload | 10,000 Zipf-skewed requests, 100 client IPs, 4 backends, seed 2253, skew 1.0 | `internal/balancer/hotkey_test.go:19`–`:28` (`hotKeySeed = 2253`, `hotKeyClients = 100`, `hotKeyRequests = 10000`, `hotKeySkew = 1.0`) |
| Unbounded (naive) busiest backend | **4,005** requests (40.0%) | ADR-0009 table at line 101; test window `[3800, 4200]` |
| Bounded-loads busiest backend | **3,126** requests (31.3%) | ADR-0009 table; test window `[3100, 3130]` |
| Cap | `ceil(10000 / 4 × 1.25) = 3125`; bounded lands one over because admission is `<=` | ADR-0009; `internal/balancer/consistent_hash.go:149` |
| 60-seed spread | naive busiest 29.9%–51.3% (median 40.0%); bounded 30.0%–31.3% | ADR-0009 offline reproducer; `internal/balancer/hotkey_reproducer_test.go:21` |

**3,996 vs 4,005, resolved.** 3,996 was the output of seed 17, an early choice that did not match the figure the spec had frozen. A 2,500-seed sweep found seed 2253 gives exactly 4,005 and 3,126; the test and ADR were locked to it (`docs/sessions/2026-09-19-opencode.md`, "Code-review follow-up"). The agent guidance's 3,996 was corrected in commit `28f52d9` ("correct stale bounded-loads hot-key figure to seed-2253 value"); `AGENTS.md` no longer contains 3,996 (searched). **Use 4,005 and 3,126.**

Caveat: these are one fixed seed chosen from a sweep because it reproduces the frozen figures. The 60-seed range is the fairer statement. **Recommendation: use with caveat** — quote the seed-2253 pair as "one fixed workload" next to the 60-seed range, and say the seed was chosen to match the frozen figures.

**Owner answer:** approved as recommended (2026-10-02).

## 4. N2 — P2C latency-bias figures (topic 3)

| Quantity | Value | Source |
|---|---|---|
| Conditions | 4 healthy backends: one at 10 ms, three at 500 ms; 4,000 `Select` calls | `internal/balancer/p2c_ewma_test.go:98`–`:113` (`TestPowerOfTwoChoicesEWMAShiftsLoadToFasterBackend`) |
| One recorded run | fast 2,043 (51.1%); slow 671 / 647 / 639 (16.8% / 16.2% / 16.0%) | ADR-0010 table, lines 108–111 |
| What the test asserts | fast ≈ 2000 ± 400, and more than twice the busiest slow backend | same test |
| Analytic expectation | the fast backend is one of the two sampled with probability 1 − C(3,2)/C(4,2) = 1 − 3/6 = **50%**, and wins every comparison it is in; each slow backend gets 50% ÷ 3 ≈ **16.7%** | derived here, consistent with the table |

The ADR states the exact split varies run to run (the generator uses the global `math/rand/v2` source). **Recommendation: use with caveat** — lead with the analytic 50% / 16.7%, and give the recorded run as "one run of the test". Do not quote 2,043 as a fixed result.

Related facts for topic 3, each read from code:
- EWMA update: `new = 0.1 × observed + 0.9 × old` (`internal/backend/backend.go:26`, `:242`–`:247`).
- Cold start: the first sample is stored directly; a zero estimate reads as "fastest possible" (`backend.go:254`–`:259`; ADR-0010 d2).
- Failure penalty: 2 s recorded for a round trip that failed before a response (`internal/proxy/proxy.go:30`).
- No decay: nothing lowers a stale estimate when a backend receives no traffic (ADR-0023 decision 5). A backend with a high estimate that is never sampled keeps it. Use as a stated limit.

**Owner answer:** approved as recommended (2026-10-02).

## 5. N3 — soak-test result (topic 7, retrospective)

| Quantity | Value | Source |
|---|---|---|
| Result | 59.66 M requests, 7,175 cancellations, 2,789 failures, 12 reloads; goroutines 35 → 30; post-GC heap 930 KB → 804 KB | `PROGRESS.md` S4.T10 entry (line 116), repeated in `docs/sprint-4-retro.md:179` and `docs/architecture.md:555`–`:565` |
| Test | `TestChaosSoakConnectionLifecycle`, `test/chaos/soak_test.go`, run by `make soak` | commit `7ea2673` (2026-09-27 18:09 +05:30) |
| Pass recorded | commit `98d2bf4` (2026-09-27 19:17 +05:30), about 68 minutes after the test landed, consistent with a one-hour run | git log |
| Tolerances | goroutines ≤ warmup + 10; post-GC heap ≤ warmup + 8 MB | `PROGRESS.md:117` |
| Conditions | in-process through the application seam, stub backends, discarding logger, reloads via `App.Reload` not a real SIGHUP | `docs/sessions/2026-09-27-opencode-4.md`; `docs/architecture.md:555` |

**Retained artefacts: none.** No run output, log or profile is kept in the repository. The only record is the text in `PROGRESS.md` (and copies of it). It is a single run, not repeatable from stored data.

**Recommendation: use with caveat.** State it as "one one-hour run on 2026-09-27, recorded in the tracking file; no output was kept", and never put the 59.66 M figure beside the benchmark numbers, because it is an in-process test with stub backends.

**Owner answer:** approved as recommended (2026-10-02).

## 6. C3, C5, C6 spot checks (figures re-read from the retained files)

| Claim | Check | Result | Rec. |
|---|---|---|---|
| C3: no-op reload p99 1.752 → 1.762 ms; drain reload p99 1.856 → 2.140 ms | `sighup-noop.txt` and `sighup-drain.txt` `measurements:` lines | match | use with the rate caveat (finding 1) |
| C3: backend4 count frozen at 95710 | `sighup-drain.txt` lines 9 and 13 | match (applied = end = 95710) | use |
| C3: zero non-2xx, zero transport errors | `measurements:` lines | `non_2xx=0 transport_errors=0 drops=0` both runs | use |
| C5: nginx ≥ 97.97% on the owner in every core row | `RESULTS.md` hot-key table, all 15 core Nginx rows | minimum 97.97 (1 MiB, 30%) | use |
| C5: LB 82.27% owner, 17.19% second at 10 KiB peak | row `core, 10kb, lb, throughput` | 0.03 / 17.19 / 82.27 / 0.52; matched Nginx row 99.96% owner | use |
| C5: no spill at 1 MiB, 30% load | row `core, 1mb, lb, latency@30%` | `no` (97.74% owner, 0.75% on each other backend) | use |
| C5: Little's law explanation | 60 req/s × 8 ms = 0.48 average concurrency; capacity floors at 1 (`consistent_hash.go:163`–`:165`) | arithmetic holds, but "about 60 req/s" and "about 8 ms" are not in `RESULTS.md` | use with caveat: keep as explanation, give no measured request rate or latency unless the owner supplies the source |
| C6: HTTP/1.1 peaks and gaps | `RESULTS.md` protocol table: 11500/8000, 6000/4500, 350/275 | match; gaps 3500, 1500, 75 against steps 500, 500, 25 | use |
| C6: steps 500 / 500 / 25 | not re-derived here | the step sizes come from `bench/run.sh` peak-search bounds (ADR-0022); confirm in T13 | use, verify at writing time |

**Owner answer:** approved as recommended (2026-10-02).

## 7. Worked-example inputs

Each is computed from the formula or test it comes from.

### 7.1 Bounded-loads capacity (ADR-0009, `consistent_hash.go:149`–`:167`)

Formula: `capacity = max(1, ceil(average_active × 1.25))`, averaged over the selectable (healthy, not removed, circuit not open) backends; a backend is admitted if `active <= capacity`.

Proposed example, not from a test: four backends with 8, 2, 1 and 1 requests in flight.
- Average = (8 + 2 + 1 + 1) ÷ 4 = 3. Capacity = ceil(3 × 1.25) = ceil(3.75) = **4**.
- The first backend (8 > 4) is skipped; the walk continues clockwise round the ring to the next backend within capacity.
- Idle case: average 0 gives capacity max(1, 0) = **1**.
- Real-data anchor: test cap `ceil(10000 ÷ 4 × 1.25) = 3125`, busiest backend ended at 3,126.

Note for the writer: the average is taken over in-flight counts *before* the incoming request is counted.

**Owner answer:** approved as recommended (2026-10-02).

### 7.2 P2C with one slow backend (ADR-0010, test in section 4)

- Four backends; estimates 10 ms, 500 ms, 500 ms, 500 ms.
- Two are sampled per request. The fast one is among them 3 times in 6 pairs (50%) and wins each; in the other 3 pairs two slow backends are compared and the lower estimate wins, so each slow backend takes about 16.7%.
- EWMA step: a backend at 10 ms that serves one 500 ms request moves to 0.1 × 500 + 0.9 × 10 = **59 ms**; at 59 ms it moves to 0.1 × 500 + 0.9 × 59 = **103.1 ms** (≈ ten requests to adapt, per the `ewmaAlpha` comment).
- Failure: a failed round trip folds in 2,000 ms, so one failure from 10 ms gives 0.1 × 2000 + 0.9 × 10 = **209 ms**.
- Limit example for the no-decay statement: a backend at 500 ms that recovers to 10 ms but is never sampled keeps its 500 ms estimate. It is only chosen when paired with a backend whose estimate is higher or when it wins by a tie.

**Owner answer:** approved as recommended (2026-10-02).

### 7.3 Drain with a named window (ADR-0016)

- Default drain window: **30 s** (`internal/config/config.go:83` `DefaultDrainWindow`); the benchmark config does not override it.
- Retained run: the drain reload removed backend4 at t = 29 s; its arrival count was **95,540 before**, **95,710 when the reload applied** and **95,710 at the end** (`RESULTS.md` snapshot table). So 170 arrivals came in between the snapshot and the swap, and none after.
- Proposed example for the document (not measured): a removed backend with 3 requests in flight and a 30 s window: phase one waits for `ActiveConns` to reach zero, polled every 100 ms (`internal/app/drain.go:23`; ADR-0016 d7). If all 3 finish at 2 s, the drain ends at 2 s. If one is still running at 30 s, `Retire()` cancels it and a request still waiting for response headers gets a 502 with reason `window_expired` (ADR-0016 d4).
- The benchmark run never hit the window; its drain finished well inside it. Do not claim a window expiry from the benchmark.

**Owner answer:** approved as recommended (2026-10-02).

## 8. Definitions inventory (order of first use in the planned document)

Terms the document will use, in the order the seven topics reach them. Each is defined at first use by ticket 03/04.

1. Layer 7, reverse proxy (topic 1)
2. `net/http/httputil` and `ReverseProxy`; hop-by-hop headers (only if used)
3. h2c, HTTP/2, ALPN (topic 1, topic 6)
4. Third-party dependency (topic 1)
5. Consistent hashing; ring; virtual node (topic 2)
6. Hot key (topic 2)
7. Bounded loads; capacity; ε (topic 2)
8. In-flight request / active connections (topic 2)
9. P2C (power of two choices) (topic 3)
10. EWMA; α; cold start; decay (topic 3)
11. SIGHUP; reload; immutable snapshot (topic 4)
12. Drain; drain window (topic 4)
13. Health check, active vs passive; outlier ejection (topic 5)
14. Circuit breaker; closed / open / half-open (topic 5)
15. Retry (and why none) (topic 5)
16. p50 / p99 / p99.9; req/s; latency (topic 6)
17. Peak; matched comparison; nearest-equivalent; rig-limited (topic 6)
18. Atomic operation; compare-and-swap; mutex; channel; goroutine; race detector (topic 7)
19. Context cancellation (topic 7)

Defined in `CONTEXT.md` already: Hot key, Matched, Peak, Rig-limited, Nearest-equivalent, Competitor, No-op reload. Others need a fresh one-sentence definition in the document.

**Owner answer:** approved as recommended (2026-10-02).

## 9. Not verified / out of scope

- No code-size figure was collected (the spec lists it as a candidate; nothing requires it). **Recommendation: drop** unless the owner supplies one.
- The ADR rationale for item 1.8 (ring cache CAS) was not read; only the code. Ticket 04 reads ADR-0008 before using it.
- Nothing here edits an ADR, test or code.

## Decisions needed from the owner

1. Finding 1: accept "target 1750 req/s; delivered about 1210 req/s on average" in C3 and C4?
2. Item 2.1: cite the circuit-breaker mechanism as an inference, or cite only ADR-0018 and the figures?
3. Item 2.2: drop 3500 req/s?
4. Finding 4: confirm the document says "spills a minority" and not "rebalances"?
5. Item 1.12: which statement about goroutines per in-flight request is right? (`AfterFunc` runs its callback in a new goroutine only when it fires.)
