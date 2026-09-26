# S4.T5–T11 — Connection lifecycle correctness: cancellation, mid-body death, timeouts, transport tuning, pprof audit, soak

Status: ready-for-agent

Bundle spec for the second Sprint 4 phase. Every design decision recorded here
was locked in the /grill-with-docs session that preceded this file (Rounds 1–3,
questions Q1–Q8 including the Q2b/Q3/Q4/Q6/Q8 follow-ups). Kickoff prompts for
the individual tickets consume this spec directly. Vocabulary follows
`CONTEXT.md`: **Backend identity**, **Reload**, **Draining**, **Drain window**,
**Selectable**, **Probe**, **Ejection**, **Client cancellation**, **Slow-loris**.

**Ticket discipline.** Every user story and every implementation decision below
is tagged with exactly one ticket. A ticket builds only what carries its tag.
Where a later ticket changes something an earlier one built, the earlier ticket
says so as a named limitation, not as a TODO it fills itself (AGENTS.md
Step 2.5). The ticket map is the section to check first.

---

## Problem Statement

The load balancer's request path is correct on the happy path and wrong at
every edge where a client or backend misbehaves, and the edges are where
production load balancers live:

- **Client cancellation is misclassified.** When a client disconnects
  mid-request, the transport cancels the outbound request and the proxy's
  error handler fires. It is indistinguishable from a backend failure, so the
  circuit breaker, passive outlier detector, and EWMA latency all record a
  backend failure that never happened. A fleet of flapping clients looks like
  a fleet of broken backends.
- **Backend death mid-response is invisible.** When a backend dies after
  sending response headers but before completing the body, the body read error
  is swallowed. The client gets a truncated body with a 200, and nothing is
  logged — the death leaves no trace anywhere.
- **The client-facing server has no slow-loris protection.** Only a 5-second
  read-header bound exists. A client that sends headers slowly, or a body
  slowly, pins a server goroutine for as long as it likes.
- **The backend transport is untuned.** The proxy runs on
  `http.DefaultTransport`: 2 idle connections per host, no dial timeout
  beyond the stdlib default, no response-header timeout. A backend that
  accepts connections but never answers pins a request goroutine forever.
- **There is no way to audit for leaks.** No pprof endpoints exist, and no
  sustained-load test exercises the failure paths that allocate resources.
- **The absence of retries is an architectural consequence that is nowhere
  recorded.** The observer and active-connection design makes retrying
  harmful; that decision needs to be defensible, not implicit.

Sprint 4's second exit criterion is the chaos test: `docker kill` a backend
mid-response — client gets a clean 502, no crash, no leak. The third is a
one-hour soak under load with no goroutine or memory growth.

## Solution

Eight tickets: one tracking amendment, six implementation tickets, one
docs-only ADR.

0. **S4.D1 — Tracking amendment** (docs-only). `PROGRESS.md` gains the
   S4.T5–T11 entries with acceptance criteria; the proposed ticket
   "client-cancellation misclassification fix" is closed here, named to S4.T5.
   Lands before any code, per the amendment-first precedent.
1. **S4.T5 — Context propagation and cancellation correctness.** The error
   handler classifies every failed round trip into exactly one of three
   buckets — drain cancellation, client-gone, genuine transport failure —
   using the client's own request context as the discriminator. Client-gone
   requests reach no observer, record no latency, release their slot, and are
   recorded as 499.
2. **S4.T6 — Backend death mid-response.** The response-body wrapper
   observes read errors. A non-EOF read error after headers is logged with
   the bytes already copied; the success recorded when headers arrived stands;
   no observer is fed. The mid-body ejection gap is documented as a known
   limitation.
3. **S4.T7 — Slow-loris client timeouts.** A new `server:` config section
   with `read_timeout` bounds the full request read including the body.
   `WriteTimeout` is deliberately omitted and the reason documented.
4. **S4.T8 — Transport tuning.** A new `transport:` config section with
   `dial_timeout`, `response_header_timeout`, `max_idle_conns_per_host`, and
   `idle_conn_timeout` replaces `http.DefaultTransport` with a configured
   `http.Transport`.
5. **S4.T9 — pprof audit.** pprof is mounted on the metrics listener, and a
   goroutine-leak audit test asserts the count returns to baseline after a
   failure-laden load burst.
6. **S4.T10 — Soak test.** A flag-gated one-hour test cycling steady load,
   client cancellations, backend deaths, and SIGHUP reloads, asserting
   goroutine and heap flatness with committed numeric tolerances.
7. **S4.T11 — Retry-policy ADR** (docs-only). Records "no retry, ever" as a
   consequence of the observer and active-connection design.

From the operator's side: clients that hang up stop polluting every failure
signal; backends that die mid-response leave an actionable log line; slow
clients are bounded; backends that stop answering fail fast; and a one-hour
run under realistic failure load proves nothing leaks.

## User Stories

Each story is tagged with the one ticket that delivers it.

### Operator — failure signals

1. [T5] As an operator, I want a client cancellation to never count as a backend failure, so that my circuit breaker and outlier detector reflect only backend behavior.
2. [T5] As an operator, I want client-gone requests recorded as 499 in metrics and logs, so that I can distinguish client churn from backend breakage on a dashboard.
3. [T5] As an operator, I want the 5xx status class reserved for backend-caused failures, so that a 5xx alert always means something we broke, not something the client did.
4. [T6] As an operator, I want a backend that dies mid-response to be logged at WARN, so that the death is visible instead of silent.
5. [T6] As an operator, I want the mid-body log line to carry the number of bytes already copied, so that I can tell a crash after 12 bytes of a 10MB response from a blip after 9.99MB.
6. [T9] As an operator, I want pprof endpoints on the operational listener, so that I can profile goroutines and heap without opening a separate port.

### Operator — timeouts and pool

7. [T7] As an operator, I want a configured bound on how long the client-facing server takes to read a full request, so that a slow-loris client cannot pin a server goroutine.
8. [T7] As an operator, I want idle keep-alive connections to remain bounded, so that dead clients do not accumulate connections.
9. [T8] As an operator, I want a dial timeout, so that a backend that never accepts connections fails fast instead of pinning requests.
10. [T8] As an operator, I want a response-header timeout, so that a backend that accepts a connection but never answers is recorded as a failure rather than hanging the request.
11. [T8] As an operator, I want to tune idle connections per host and the idle connection timeout, so that connection reuse matches my traffic shape.
12. [T8] As an operator, I want the total idle connection pool sized consistently with the per-host setting, so that the per-host knob is not silently capped by the stdlib default.
13. [T7] As an operator, I want a reload that changes any server or transport setting rejected whole, so that a timeout change cannot silently do nothing.

### Operator — confidence

14. [T10] As an operator, I want a one-hour soak test under realistic failure load, so that I can trust there are no goroutine or memory leaks before deploying.
15. [T10] As an operator, I want the soak test to cycle cancellations, backend deaths, and reloads, so that it exercises the failure paths that allocate resources, not just the happy path.

### Client

16. [T5] As a client, I want my cancellation to stop the outbound request promptly, so that my disconnect frees resources immediately.
17. [T6] As a client, I want a clean 502 rather than a hang when the backend dies before responding, so that I can fail fast and retry elsewhere.
18. [T7] As a client with a slow upload, I want a generous request read timeout, so that my slow-but-legitimate body is not cut off.

### Maintainer / contributor

19. [D1] As a maintainer, I want the S4.T5–T11 tickets tracked with acceptance criteria before any code, so that scope is auditable before it is built.
20. [D1] As a maintainer, I want the client-cancellation proposed ticket closed as absorbed by S4.T5, so that the backlog has no dangling duplicates.
21. [T5] As a maintainer, I want the cancellation predicate to key off the client's own request context, so that a backend timeout is never misclassified as client-gone.
22. [T5] As a maintainer, I want the predicate's three-tier order (drain, then client-gone, then transport failure) written down, so that future error paths reuse it instead of reinventing it.
23. [T6] As a maintainer, I want mid-body death detected at the existing body wrapper, so that no new hook, goroutine, or interface is needed.
24. [T6] As a maintainer, I want the mid-body ejection gap documented in this spec, so that the limitation is stated, not buried.
25. [T7] As a maintainer, I want `WriteTimeout` omitted and the reason documented, so that no future contributor "fixes" the schema by adding it.
26. [T8] As a maintainer, I want the total-pool sizing note in this spec, so that the transport construction stays consistent with the per-host knob.
27. [T9] As a maintainer, I want the goroutine-leak audit as an automated test, so that leaks are caught by assertion, not by someone reading a profile by hand.
28. [T10] As a maintainer, I want the soak test flag-gated with a short-duration option, so that I can iterate without waiting an hour.
29. [T10] As a maintainer, I want the soak's race variant to drop the heap assertion, so that race-detector memory overhead does not produce false failures.
30. [T11] As a maintainer, I want the no-retry decision recorded as an ADR, so that the absence of retries is defensible, not an oversight.
31. [T7] As a maintainer, I want the new config sections to follow the existing nil-means-omitted convention, so that an omitted field and an explicit default compare equal after validation.

### Reviewer

32. [T5] As a reviewer, I want a chaos test proving a client cancellation reaches no observer and records no EWMA latency, so that the misclassification fix is demonstrated, not claimed.
33. [T5] As a reviewer, I want the same test to prove a response-header timeout still reaches the observers as a failure, so that the predicate does not over-suppress.
34. [T6] As a reviewer, I want a chaos test proving a backend killed mid-body logs the death with bytes copied and leaves the recorded success standing, so that the bell is not un-rung.
35. [T7] As a reviewer, I want a test proving a slow-loris client is disconnected at the read timeout, so that the bound is real.
36. [T8] As a reviewer, I want a test proving the transport's timeouts and pool settings are actually configured, so that the knobs are wired, not just parsed.
37. [T9] As a reviewer, I want a test proving the goroutine count returns to baseline after a load burst with cancellations and backend deaths, so that the audit is automated.
38. [T10] As a reviewer, I want the soak tolerances committed as numbers, so that "no growth" is an assertion, not a wish.
39. [T11] As a reviewer, I want the retry ADR to cite the observer and active-connection design as the reason, so that the decision follows from the architecture.

## Implementation Decisions

### Ticket map

| Ticket | Blocked by | Delivers | ADR | Explicitly NOT in this ticket |
|--------|-----------|----------|-----|-------------------------------|
| S4.D1 tracking amendment | — | stories 19–20 | none | any code; any spec change |
| S4.T5 cancellation correctness | D1 | 1–3, 16, 21–22, 32–33 | none (records the predicate for later ADRs) | mid-body detection; the 499 metric is here but the log vocabulary lands with it |
| S4.T6 mid-body death | T5 | 4–5, 17, 23–24, 34 | none | changing the recorded success; observer notification timing |
| S4.T7 slow-loris timeouts | D1 | 7–8, 13, 18, 25, 31, 35 | none (omission rationale recorded here) | WriteTimeout; per-write response timeouts |
| S4.T8 transport tuning | D1 | 9–13, 26, 31, 36 | none | MaxConnsPerHost; ForceAttemptHTTP2; backend TLS |
| S4.T9 pprof audit | T5–T8 | 6, 27, 37 | none | a separate debug listener; leak fixes beyond this bundle |
| S4.T10 soak test | T9 | 14–15, 28–29, 38 | none | trend checks; efficiency measurement |
| S4.T11 retry ADR | T10 | 30, 39 | ADR written here (docs-only) | any retry logic |

Named hand-offs between tickets (each is a documented limitation of the earlier
ticket, closed by the later one — not work the earlier ticket half-builds):

- **T5 → T6:** T5 builds the three-tier errorHandler classification and the
  clientCtx field. T6 extends the same errorHandler path and the same body
  wrapper without changing the classification; it adds read-error observation
  only.
- **T7/T8 → T10:** the soak exercises both timeout layers; neither T7 nor T8
  depends on the other, so they run in parallel after D1.
- **T9 → T10:** the audit's goroutine-baseline assertion is the soak's core
  assertion, generalized to a full hour with failure phases.

### S4.D1 — Tracking amendment

- `PROGRESS.md` gains S4.T5–T11 entries with acceptance criteria drawn from
  this spec's ticket sections, under a new "Sprint 4 — Connection Lifecycle"
  heading mirroring the reload bundle's heading.
- The proposed ticket "client-cancellation misclassification fix" is closed in
  the amendment, named to S4.T5 as its owner — not left dangling.
- `MILESTONES.md` needs no change: the Sprint 4 deliverables already list
  connection lifecycle correctness (context propagation, client cancellation,
  backend death mid-response, slow-loris timeouts, pprof audit), transport
  tuning via `http.Transport`, and the retry-policy ADR.
- Docs-only, TDD-exempt; lands alone as one commit touching only
  `PROGRESS.md`.

### S4.T5 — Context propagation and cancellation correctness

- The per-request bookkeeping (`reqState`, ADR-0007) gains a `clientCtx`
  field: the client request's context, captured in `ServeHTTP` before the
  existing cancel-with-cause derivation. Concurrency is fine under ADR-0007's
  documented model — `reqState` is created on and only touched by the request
  goroutine, and the error handler runs on that same goroutine.
- The error handler classifies every failed round trip into exactly one of
  three buckets, in this order:
  1. **Drain cancellation** — the existing `ErrDrainWindowExpired` cause check
     (ADR-0016), unchanged.
  2. **Client-gone** — `clientCtx.Err() != nil`: the cancellation originated
     client-side. The transport can only cancel the outbound context via
     parent propagation (client context done), the drain after-func, or its
     own timers; the drain case is caught by check 1, and the transport's own
     timers leave the client context alive — so a real backend timeout still
     reaches check 3. Context cancellation is sticky, so checking at
     error-handler time is race-free.
  3. **Genuine transport failure** — dial timeout, response-header timeout,
     connection refused: observers get the failure with the fixed 2s penalty,
     exactly as today.
- Client-gone handling: suppress the observer fan-out entirely (same
  suppression discipline as a removed backend, ADR-0015 decision 8); record no
  EWMA latency (no round trip completed); release the active-connection slot
  (the existing once-guard); write **499** to the response recorder instead
  of 502; log at INFO with the new `client_canceled` reason instead of the
  WARN failure line. The 499 write to a dead client is a no-op on the wire —
  the status exists for the recorder, metrics, and logs, matching nginx's
  client-closed-request convention.
- Metrics: the whole-request counter still records the request, with status
  499 → `status_class="4xx"`. The 5xx class is reserved for backend-caused
  failures; a client that left before we answered is a client error. This is
  what makes the fix observable: client-gone volume shows up as 4xx, backend
  failures stay 5xx, and the two can never be conflated on a dashboard.
- Accepted conflation: on server shutdown, the HTTP server cancels in-flight
  request contexts, so shutdown-time cancellations also classify as
  client-gone. That is desirable — the process is exiting, and suppressing
  observer writes during shutdown is correct, not harmful.
- The log vocabulary gains the `client_canceled` reason, defined here with its
  first emitter (frozen-vocabulary discipline).

### S4.T6 — Backend death mid-response

- The response-body wrapper (`releaseBody`, ADR-0007) observes read errors:
  any error that is not `io.EOF` is mid-body death (connection reset,
  unexpected end of body on a content-length response, etc.). `io.EOF` is a
  clean end and logs nothing.
- On mid-body death: log at WARN with the new `backend_died_mid_response`
  reason, carrying the backend name, the path, and **bytes already copied**
  (tracked trivially in the wrapper's `Read`). Feed no observer; change nothing
  about the success recorded when headers arrived.
- Justification — *you can't un-ring the bell*: the observer already recorded
  a success when the headers arrived, and a second failure event for the same
  request would corrupt the outlier window's counts. (This is distinct from
  ADR-0016's drain precedent, which is a voluntary, proxy-initiated stop; a
  backend dying mid-body is involuntary, and the bell argument is the honest
  one.)
- The slot release stays once-guarded via the existing `sync.Once`; `Close`
  still runs during panic unwinding, and the once-guard makes the second call
  a no-op.
- The pre-headers case is unchanged and already correct: transport error →
  error handler → 502 → observers see a failure (that *is* a backend
  failure). The exit-criterion chaos test proves it.
- **Known gap, stated in this spec:** a backend that consistently dies after
  sending headers (e.g. OOM-killed mid-response) is never ejected by passive
  detection — every request looks like a success at headers time. Fixing it
  means deferring observer notification until body completion, which
  restructures the request path; out of scope for this bundle, documented
  here, not buried.
- The log vocabulary gains the `backend_died_mid_response` reason, defined
  here with its first emitter.

### S4.T7 — Slow-loris client timeouts

- Config gains a top-level `server:` section with one field:
  `read_timeout` — a pointer duration, default **60s** when omitted, rejected
  when explicitly non-positive, following the same nil-means-omitted
  convention as the Sprint 3 sections.
- `ReadTimeout` bounds the full request read *including the body* (the
  standard library's own definition), covering the slow-body slow-loris
  vector; the existing 5-second `ReadHeaderTimeout` covers slow headers;
  idle keep-alives remain bounded by the standard library's fallback chain
  (`IdleTimeout` → `ReadTimeout` → `ReadHeaderTimeout`), which already
  resolves to the 5-second header bound today.
- **`WriteTimeout` is deliberately omitted.** The standard library's
  `WriteTimeout` spans end-of-request-headers through the entire response
  body copy, so a slow-but-healthy upstream (large response, slow client)
  trips it even though nothing is wrong — it is not symmetric with
  `ReadTimeout` and must not be silently included. The omission rationale is
  recorded here and in the config field documentation, so no future
  contributor "fixes" the schema by adding it.
- **Known gap, sharpened:** the residual exposure is a client that stalls
  reading while the proxy still has buffered data to send — that pins one
  server goroutine. A finite body bounds the pin: it lasts only as long as
  there is data to write. The worst case is a dead-but-not-RST'd client
  holding the goroutine for the OS TCP retry window (Linux `tcp_retries2`
  ≈ 13–30 minutes), but only while data remains to write. Per-write response
  timeouts (nginx's `proxy_send_timeout` granularity) would require wrapping
  the connection and are out of scope.
- `NonBackendChanges` names `server` when it differs, so a reload that
  changes it is rejected whole (ADR-0015 decision 4 extended).

### S4.T8 — Transport tuning

- Config gains a top-level `transport:` section with four fields, all
  pointer-typed with exported defaults, all following the nil-means-omitted
  convention:
  - `dial_timeout` — default **5s**. Bounds dial and connection
    establishment; LAN backends accept in milliseconds, so 5s is generous
    headroom.
  - `response_header_timeout` — default **30s**. The backend must begin
    answering headers within this; this is the knob that makes a hung
    backend a failure instead of a pinned request. (This is also the timeout
    whose cancellation the T5 predicate must not misclassify as client-gone —
    the three-tier order is what keeps them distinct.)
  - `max_idle_conns_per_host` — default **100**. The stdlib default of 2 is
    far too low for a proxy multiplexing many concurrent requests onto few
    backends; 100 matches common production practice without being unbounded.
  - `idle_conn_timeout` — default **90s** (the stdlib default). Recycles
    idle keep-alives so backend restarts are picked up; kept at the stdlib
    value deliberately — no evidence yet that shorter is better, and the
    Sprint 5 benchmarks can revisit.
- The proxy's `ReverseProxy` gains a custom `http.Transport` replacing
  `http.DefaultTransport`: `DialContext` with the dial timeout,
  `ResponseHeaderTimeout`, `MaxIdleConnsPerHost`, `IdleConnTimeout`.
- **Implementation note (not a config field):** the transport's total
  `MaxIdleConns` must be at least `max_idle_conns_per_host × backend_count`
  (or 0 = unlimited), otherwise the stdlib default of 100 silently caps the
  per-host setting and the knob is a lie. The transport construction sizes it
  from the per-host value and the configured backend count. Operators tune
  per-host, not total — YAGNI.
- YAGNI exclusions, recorded here: `MaxConnsPerHost` outbound limits,
  `ForceAttemptHTTP2` (Sprint 5's HTTP/2 work), TLS knobs for backend
  connections (no HTTPS backends exist in the demo stack).
- `NonBackendChanges` names `transport` when it differs.

### S4.T9 — pprof audit

- `net/http/pprof` is mounted on the metrics listener's handler. No new
  listener, no new config: the metrics listener is already always-on and
  unauthenticated, so pprof there adds no attack surface the project has not
  already accepted (ADR-0005 puts security hardening out of scope).
- A goroutine-leak audit test through the app seam: a load burst that includes
  client cancellations and backend deaths, a quiet period, then an assertion
  that the goroutine count has returned to baseline within a small delta.

### S4.T10 — Soak test

- A test in the chaos harness through the app seam, skipped unless a flag is
  set; default duration one hour, with a duration flag to shorten for
  iteration. New `make soak` target.
- Phases (the failure phases are the point — steady-state-only proves nothing
  about the code this bundle adds):
  1. **Warmup** — steady load until goroutine and heap counts stabilize; the
     baseline is captured here.
  2. **Steady-state proxying** (20 min).
  3. **Client-cancellation phase** (10 min) — ~5% of clients disconnect
     mid-response, exercising T5's suppression path under volume.
  4. **Backend-death phase** (10 min) — one backend is killed mid-response
     and restarted, repeatedly, exercising T6's read-error path and the
     health checker's ejection and reinstatement.
  5. **Reload phase** (10 min) — SIGHUP every 60s: remove one backend, add
     another, short drain window, exercising the drain lifecycle under
     sustained load rather than a single burst.
  6. **Quiet** (60s) — no traffic, drains finish, then assert.
- Tolerances, asserted after the quiet period, committed as numbers:
  - Goroutines: end-of-soak count ≤ warmup baseline **+ 10**. The delta
    absorbs GC finalizer queueing and timer jitter; a real leak (per-request
    goroutine, per-reload drain goroutine) is thousands of goroutines and
    cannot hide inside 10.
  - Heap: `runtime.GC()` forced at both measurement points; end `HeapAlloc`
    ≤ warmup `HeapAlloc` **+ 8 MB**. Post-GC `HeapAlloc` is the honest leak
    signal — live heap that survives two GCs is retained, not noise.
  - No trend check: the endpoint assertions are sufficient and deterministic;
    a trend check adds flake risk for no additional leak-catching power.
- Race caveat: `make soak` runs **without** `-race` — the tolerances are
  calibrated for non-race execution. `make soak-race` is a separate manual
  target that keeps the goroutine assertion and **drops the heap assertion**
  (the race detector's 5–8x memory overhead makes heap numbers meaningless).

### S4.T11 — Retry-policy ADR (docs-only)

- Records **no retry, ever**. A failed round trip is classified (T5/T6),
  logged, and surfaced as a 502 or 499. Retrying would double-count active
  connections (two slots for one client request) and corrupt the outlier
  window (one logical failure recorded as two). The decision is a consequence
  of the observer and active-connection design, written after T5/T6 land so
  the ADR cites the code that forces it.
- Docs-only, TDD-exempt.

## Testing Decisions

A good test exercises external behaviour through the highest seam that can
observe it: HTTP outcomes, gauge values through the collector's registry, and
structured log lines through the existing capture handler. Tests do not reach
into request bookkeeping or transport internals. Concurrency tests run under
`-race` with `require.Eventually` deadlines sized for race-detector overhead
(the S3.T8/T9 convention). Blocking is deterministic — backends gated on
channels, never sleeps. Each ticket's tests are Red-first and cover only that
ticket's tagged stories.

### Seams

Highest first; the first two exist and are the primary seams, the rest are
package-level:

1. **The application seam** (built in S4.T0, reload added in S4.T3): build
   (and reload with a parsed config), observed through the client handler, the
   collector, the registry, and captured logs. The chaos tests, the leak
   audit, and the soak use it.
2. **The metrics listener** (S4.T9): pprof endpoints observed over HTTP
   through the same handler as `/metrics`.
3. **Package-level seams:** config load/validate table tests; the proxy's
   error-handler classification observed through HTTP outcomes (a gated
   backend that hangs vs. a client that disconnects vs. a black-hole
   address); the transport's timeouts observed through the proxy's behavior
   (dial timeout against a non-accepting address, response-header timeout
   against a gated backend); the soak's goroutine/heap assertions through
   `runtime` after a quiet period.

### Per ticket

- **D1:** none — docs-only.
- **T5:** a client that disconnects mid-request reaches no observer (outlier
  window and circuit state unchanged), records no EWMA latency, releases its
  active-connection slot, and is recorded as 499/`4xx`; a backend that
  trips the response-header timeout still reaches the observers as a failure
  (the predicate does not over-suppress); a drain cancellation still classifies
  as `window_expired` (the three-tier order). Prior art: proxy observer
  tests, drain chaos tests.
- **T6:** a backend killed mid-body logs `backend_died_mid_response` with the
  bytes copied, leaves the recorded success standing (outlier window and
  circuit unchanged), and releases its slot; a backend killed before headers
  still produces a 502 and a failure observation. Prior art: S3.T8 eviction
  chaos test, the proxy 100-concurrent active-connection leak test.
- **T7:** a slow-loris client (headers sent slowly, then body sent slowly) is
  disconnected at the read timeout; an omitted `read_timeout` defaults to 60s
  and an explicit non-positive value is rejected naming the field; a reload
  that changes `server` is rejected naming it. Prior art: config
  load/validate table tests.
- **T8:** the transport's timeouts and pool settings are observed through
  behavior — a non-accepting address fails at the dial timeout, a gated
  backend fails at the response-header timeout; omitted fields default and
  non-positive values are rejected naming the field; a reload that changes
  `transport` is rejected naming it. Prior art: config table tests, health
  checker probe-timeout tests.
- **T9:** pprof endpoints respond on the metrics listener; the goroutine
  count returns to baseline within the delta after a failure-laden burst.
  Prior art: the proxy active-connection leak test.
- **T10:** the soak itself, flag-gated, with the phases and tolerances above;
  a short-duration run verifies the phase machinery, and the full-hour run is
  the exit criterion. Prior art: the S4.T4 exit-criterion test, S3.T8/T9
  chaos tests.

## Out of Scope

Recorded as proposed tickets or named limitations, not built in any ticket
here:

- `WriteTimeout` — omitted deliberately; the rationale and the sharpened
  known gap are recorded in T7 and in the config field documentation.
- Per-write response timeouts (nginx `proxy_send_timeout` granularity) —
  would require wrapping the connection; a Sprint 5 topic if ever.
- Retry logic of any kind — the ADR records its absence (T11).
- Fixing the mid-body ejection gap — deferring observer notification to body
  completion restructures the request path; documented as a known limitation
  in T6.
- `MaxConnsPerHost` outbound limits, `ForceAttemptHTTP2`, backend TLS —
  Sprint 5's HTTP/2 work or later.
- Trend checks in the soak — endpoint assertions only.
- A separate debug listener for pprof — the metrics listener is the
  operational surface (T9).
- Hot-reload of `server:` or `transport:` settings — rejected, not ignored.
- CI, pre-commit hooks, linters beyond `gofmt`/`goimports` — deferred per
  AGENTS.md.
- `docs/architecture.md` updates for this bundle — the Sprint 4 retro
  ticket, per the Sprint 1–3 pattern.
- Testing that the OS delivers signals to a notification channel (the
  one-line registration in `main` stays untested, as in the reload bundle).

## Further Notes

- Frozen contracts touched, each in the tagged ticket: `Config` gains the
  `server:` and `transport:` sections [T7, T8]; `NonBackendChanges` names
  both sections [T7, T8]; the log vocabulary gains `client_canceled` [T5] and
  `backend_died_mid_response` [T6]; `proxy.New(reg, sel)` is untouched; the
  `Backend` type is untouched; `reqState` gains `clientCtx` and `releaseBody`
  gains read-error observation, both internal to the proxy package and both
  under ADR-0007's request-goroutine concurrency model [T5, T6].
- The 499 convention: nginx's client-closed-request code. It is recorded in
  metrics as `status_class="4xx"` and in logs as status 499; the 5xx class is
  reserved for backend-caused failures.
- `CONTEXT.md` gains two entries, additions only: **Client cancellation** (the
  client disconnecting mid-request; not a backend failure; recorded 499;
  distinct from Draining, which is voluntary and proxy-initiated) and
  **Slow-loris** (the slow-request attack the client-facing timeouts defend
  against; bounded by the request read timeout and the idle fallback).
- The sprint-1 contracts doc's concurrency-ownership rows for the proxy's
  request path are updated from the as-built description to include the
  clientCtx field and the body wrapper's read-error observation.
- Ticket files, one per ticket under this directory's `issues/`, in
  dependency order: `01` S4.D1, `02` S4.T5, `03` S4.T6, `04` S4.T7,
  `05` S4.T8, `06` S4.T9, `07` S4.T10, `08` S4.T11. T7 and T8 are parallel
  (both blocked by D1 alone); the rest are serial as mapped above.
