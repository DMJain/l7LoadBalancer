"""Chapter specs for the study-book pipeline (chapters 6-25).

Each spec drives five steps (see drive.py):
  research  (Haiku, headless claude)  -> notes/chNN_research.md
  code      (DeepSeek, opencode)      -> notes/chNN_code.md
  write     (DeepSeek, opencode)      -> chapter block in docs/PROJECT_GUIDE.md
  review    (Haiku)                   -> reviews/chNN_review.md
  fix       (Haiku)                   -> chapter block in docs/PROJECT_GUIDE.md

Fields:
  title        exact title used in the book's table of contents
  notes_ready  True when research + code notes already exist (chapters 6-10)
  research     numbered topics for the research agent (general knowledge, web)
  code         the "What to find" sections for the code-search agent
  outline      what the writer must cover, in order
  review_extra extra checks for the reviewer
"""

# Appended to the outline of every architecture chapter (7-20): the book owner's explicit requirement.
IO_BLOCK = """
- **What goes in and what comes out, in order.** Two tables with columns: step | input (what it receives, from whom) | what it does | output (what it returns or changes, to whom) | `path:line`.
  (a) "When a client request arrives": only the steps where THIS block takes part, in execution order, naming the block before and after it.
  (b) "When the selection algorithm is changed (config reload with a different `algorithm`)": what THIS block does, or the honest statement that it is not involved, with `path:line`.
  Both tables must be consistent with Chapters 4 and 17 and with the code facts you were given."""

WHY_BLOCK = """
- **What it does, and why the project needs it.** Plain language first (one short paragraph and an analogy), then precisely. Say what would break or be worse without this block."""

NOT_SELECTED_BLOCK = """
- **How a backend ends up NOT being selected.** If this block is one of the reasons a backend is skipped, list the exact condition, the code that checks it (`path:line`), who sets it and who clears it, and give a small timeline example."""

COMMON_TAIL = """
- **What you learned** (6-8 bullets); **self-check quiz** (6 questions, at least two of the form "open file X and find Y", answers inside `<details>`); **Sources** (only VERIFIED URLs from the research file); **Next chapter** pointer (use the next title in the table of contents)."""

SPECS = {}

SPECS[6] = dict(
    title="The big picture: packages and glossary",
    notes_ready=True,
    review_extra="Check the dependency graph against the real import blocks, the package size table against `wc -l`, every CONTEXT.md term is present and defined faithfully, the three listeners table, and that Chapter 1's folder table is not simply copied.",
)

SPECS[7] = dict(
    title="YAML config",
    notes_ready=True,
    outline="""
- Why a config file at all and why YAML (use only VERIFIED research points). A tiny YAML primer: mappings, lists, scalars, indentation, comments, duration strings like `5s`.
- The whole example config (`configs/example.yaml`) reproduced and explained line by line, with the Go field each key maps to (`path:line`).
- Loading step by step (`Load`): read file, strict decode (state plainly what `KnownFields(true)` does at `internal/config/config.go:349`: unknown keys are rejected), apply defaults, validate. Error wrapping prefixes.
- A table of EVERY config key: YAML key | Go field | type | default (constant + `path:line`) | meaning | reloadable? (yes/no, from `NonBackendChanges`).
- Validation: every rule in order with the exact error message and `path:line`; group them; a table of "bad config -> error message you will see" with 8-10 examples the student can try.
- Defaults vs required fields, and why defaults live in named constants.
- Backends in config: name, url, weight, timeouts; identity of a backend for reload (name + url pair) and what changes in place.
- Reload-related config logic: `DiffBackends` and `NonBackendChanges` (what goes in, what comes out); why non-backend changes are rejected as a whole (cite ADR text from the code notes).
- A diagram of the config life cycle: file -> Load -> Validate -> Config struct -> Build (startup) and -> Diff/Reload (runtime). Mermaid + aligned ASCII.
- Documentation vs code mismatches found (be honest, cite both sides).
- Try it yourself: 3 small experiments (break a key, remove `listen`, use an unknown algorithm) with the expected result taken from the code, clearly stating they are expectations derived from the code, not run output.""" + IO_BLOCK + COMMON_TAIL,
    review_extra="Verify every key in the key table against the struct tags and defaults, every validation message string against the code, and that the example.yaml reproduction matches the file exactly.",
)

SPECS[8] = dict(
    title="App wiring and startup",
    notes_ready=True,
    outline="""
- What 'wiring' means (composition root; use the research file's VERIFIED/SECONDARY tags carefully) and why the project builds everything in one place (`internal/app`).
- `main()` step by step in execution order with `path:line`, flags, signals, exit codes.
- The `App` struct field by field.
- `Build` step by step in exact order; a dependency table: object | needs | gives to; a Mermaid + ASCII diagram of the object graph.
- `Run` lifecycle: what starts, in order; the three servers; error collection; shutdown trigger (overview only; Chapter 20 has details).
- Readiness: `/livez`, `/readyz`, `/startupz` gates (exact conditions with `path:line`) and the initial probe.
- Admin and metrics wiring and the honest note about no authentication.
- The `probe` subcommand and Docker health checks.
- A numbered timeline from typing the command to the first request being served.
- Kubernetes-style probe meanings from VERIFIED research points only.""" + IO_BLOCK + COMMON_TAIL,
    review_extra="Verify the order of construction in Build line by line and the readiness gates against internal/health/endpoint.go.",
)

SPECS[9] = dict(
    title="The client and the HTTP server",
    notes_ready=True,
    outline="""
- Who the client is (browser, curl, another service, load generator) with real commands from the repo.
- The client-facing `http.Server`: every field and constant, with reasons; why `WriteTimeout` is not set (quote the explanation); the other two servers' timeouts.
- Three modes: plain HTTP/1.1, TLS with HTTP/2 via ALPN, h2c: config, code path, certificate loading and when a bad path fails, the mutual-exclusion rule; the h2c dependency and the deprecation side note (hedged, VERIFIED points only; Go 1.24 added `Protocols`, the project pins Go 1.25.1).
- What Go's net/http server does per connection (words only) and the exact handoff to `Proxy.ServeHTTP`.
- The request as the balancer sees it: which fields are read and where; client IP derivation for hashing; X-Forwarded-For (not set by project code).
- Client disconnects and cancellation (how the request context flows; mapping of client cancel).
- Limits and what is NOT protected (honest, with the project's own admissions).
- TLS primer for students using VERIFIED points: certificate, key, termination, ALPN. HTTP/2 primer. Keep it short and accurate.
- Diagrams: connection handling per protocol (Mermaid + ASCII); sequence diagram of TLS+ALPN decision.""" + IO_BLOCK + COMMON_TAIL,
    review_extra="Verify every http.Server field and constant, the h2c wrapping lines, and that no standard-library line numbers are cited.",
)

SPECS[10] = dict(
    title="The proxy handler",
    notes_ready=True,
    outline="""
- `http.Handler` and `ServeHTTP` for beginners (VERIFIED research), then the `Proxy` struct field by field and its constructor.
- `ServeHTTP` line by line in execution order (the core of the chapter) with a Mermaid sequence diagram and an aligned ASCII flow; for each step: `path:line`, input, output, state changed.
- The director (what it changes and what it does not), and what the standard-library ReverseProxy adds by itself (words only). Side note: Go 1.20 added `Rewrite`; this project still uses the director (present as a note, not a bug).
- The transport and the round-trip wrapper: what is measured, who is told (observers), what counts as failure (transport error vs 5xx).
- The response path: `ModifyResponse`, the body wrapper, why the active counter is decremented on body close.
- The error handler: every branch with status, log line, metric label.
- Metrics and logs touched on the request path.
- A final numbered table of the whole request path.
- The observer pattern in plain words (hedge what research marks unverified).""" + IO_BLOCK + NOT_SELECTED_BLOCK + COMMON_TAIL,
    review_extra="Verify the execution order of ServeHTTP step by step against the real code and every status code.",
)

SPECS[11] = dict(
    title="Backend servers and the registry",
    research=[
        "What a 'backend' / 'upstream' server is in load balancing; server pools; service discovery vs static lists (nginx upstream docs, HAProxy docs, Envoy cluster docs). One line each, sourced.",
        "Backend weights in load balancing (what weight means in nginx, HAProxy, Envoy) and slow start; draining a server (connection draining in AWS ELB / nginx docs).",
        "The registry / pool pattern: a read-mostly collection with copy-on-write snapshots vs locking; Go `atomic.Pointer` / `atomic.Value` for snapshots (official docs). Mark OWN WORDING where unsourced.",
        "Server health states commonly used (healthy, unhealthy, draining, removed) in nginx/HAProxy/Envoy docs.",
    ],
    code="""
Read internal/backend/*.go completely (list files first), internal/config/diff.go, the dummy backend program under deployments/docker/dummy-backend/, demo/ backends if any, and the ADRs about the registry, reload and drain (find via docs/adr/INDEX.md).

### A. The `Backend` struct
Every field (type, purpose, who writes it, who reads it, concurrency protection: atomics/mutex), `NewBackend`/constructor, active-request counter methods (`IncActive`/`DecActive` or similar) and where they are called, EWMA latency fields and update method (`ewmaAlpha`), health flag methods (`MarkHealthy`, `MarkUnhealthy`, ...), circuit state snapshot field (CAS-guarded), `Retire` and `ErrDrainWindowExpired` (`path:line`), the context used for draining. Quote the concurrency-model comment at the top of the file.

### B. The `Registry`
Struct fields, how the backend list is stored (snapshot/slice/atomic pointer), `NewRegistry` (initial health: all HEALTHY at startup, with the comment and ADR-0015 decision 10), `Selectable()` (EXACT conditions that make a backend selectable: healthy, circuit gate allows, not draining/removed: cite each line), `All()`/iteration, `Apply` (reload: how added, removed and updated-in-place backends are handled; reload-added backends start unhealthy until a probe passes), `SetCircuitGate`, the `CircuitGate` interface (who defines it and why there), observers of registry change if any. For every method: signature, input, output, callers (`path:line`).

### C. States of a backend
Build a complete state table: healthy/unhealthy x circuit closed/open/half-open x draining/retired/removed. For each combination say whether `Selectable()` returns it and why (cite code). List every code path that changes each flag (who sets/clears, with `path:line`): active checker, outlier detector, circuit breaker, Apply/reload, drain.

### D. Identity and in-place updates
How a backend is identified across reloads (name + url), which fields update in place (weight, timeout) and what happens to its counters and state (cite `internal/config/diff.go` and registry Apply).

### E. The dummy backend (the 'real server' used in demos)
Read the dummy backend program: what it serves, flags (`-name`, `-addr`), endpoints (health endpoint? latency/failure injection?), how Docker compose and the demo start it (`path:line`). This is what students will run as "backend server".

### F. Where backends are used by other packages
Which packages call Registry/Backend methods (grep) with `path:line`: balancer, health, circuit, proxy, app.
""",
    outline="""
- What a backend is and what the "backend server" in this repo is (the dummy backend: how to run one, what it answers), with a diagram of the pool.
- The `Backend` struct field by field (plain meaning, who writes, who reads, how it is protected).
- The `Registry`: what it stores, how readers get a consistent view, `NewRegistry`, `Selectable()` in detail, `Apply` for reload.
- The complete backend state table and a Mermaid state diagram plus ASCII: healthy / unhealthy / circuit states / draining / retired / removed, with who triggers each transition.""" + NOT_SELECTED_BLOCK + """
- Weights and slow start in general (VERIFIED research) versus what this project does with `weight` (check the code notes; say honestly if it is unused by some selectors).
- Identity and in-place updates across reloads.""" + WHY_BLOCK + IO_BLOCK + COMMON_TAIL,
    review_extra="Verify every Selectable() condition and every flag-writer claim against the code; verify the state table row by row.",
)

SPECS[12] = dict(
    title="The selector interface",
    research=[
        "The strategy pattern in plain words (one trusted source) and why load balancers make the selection policy pluggable.",
        "Go interfaces for pluggable behaviour; why small interfaces; the 'accept interfaces' guidance with only VERIFIED wording (Go wiki Code Review Comments).",
        "Atomic hot swap of a configuration object in Go: `sync/atomic.Pointer[T]` docs (official) and the 'copy-on-write / atomic pointer swap' idea in one paragraph.",
        "Why a selection step must be fast and lock-free on the hot path of a proxy (own reasoning is fine but mark OWN WORDING).",
    ],
    code="""
Read internal/balancer/selector.go, internal/balancer/hashkey.go, how the Proxy holds and loads the selector (internal/proxy/proxy.go: the atomic pointer holder and `Load()` use), internal/app/app.go (where the selector is built and swapped on reload), ADR-0002 (Selector interface), ADRs about reload of the algorithm and hot swap (find via docs/adr/INDEX.md), and the tests that prove the contract (test names in internal/balancer/*_test.go about unhealthy/empty/read-only behaviour).

### A. The interface
`Selector` interface and `ErrNoHealthyBackends` with doc comments quoted, `NewFromConfig` factory (each case, error for unknown algorithm), where the interface lives and why (ADR-0002 quoted with lines). The "Select is read-only by contract" statement: quote it and then FIND WHERE IT IS NOT TRUE (selectors that mutate state: round-robin counter, p2c random, least-conn increments?). Report exactly what each implementation mutates with `path:line`, and whether the proxy or the selector increments the backend's active counter.

### B. What every selector must do
The shared steps every implementation performs: call `Registry.Selectable()`, return `ErrNoHealthyBackends` on empty, how the half-open circuit trial is handled (CircuitGate), anything common (helper functions) with `path:line`.

### C. How the Proxy uses it
How the selector is stored (atomic pointer), loaded per request, how `Select` errors map to 503/502 (`path:line`), the context and request passed in.

### D. Swapping the selector
The exact reload code that builds the new selector and stores it; ordering (build before swap; failure leaves the old one), what happens to in-flight requests that already loaded the old selector, and what happens to selector-internal state (counters, rings, EWMA) after a swap. `path:line` for each.

### E. Request key for hashing
`requestHashKey` (what it reads, what it returns) in hashkey.go.

### F. Tests
List tests that document the contract (name + `path:line`).
""",
    outline="""
- What a selector is (strategy pattern analogy: GPS route choice), the `Selector` interface, `ErrNoHealthyBackends`, the factory.
- Why the interface is defined where it is (ADR-0002) and the import-cycle reason (link back to Chapter 6).
- What every selector does (skip unavailable backends, return an error when none), and the honest finding that `Select` is described as read-only but implementations mutate internal state (list who mutates what).""" + NOT_SELECTED_BLOCK + """
- How the Proxy holds the selector (atomic pointer) and how a swap works at the code level, with a before/after diagram of a swap while requests are in flight (Mermaid sequence + ASCII).
- A comparison table of the four implementations at interface level (inputs read, state kept, cost per call): details are in Chapter 21.""" + WHY_BLOCK + IO_BLOCK + COMMON_TAIL,
    review_extra="Verify the claim about which implementations mutate state, the swap ordering, and the proxy's handling of Select errors.",
)

SPECS[13] = dict(
    title="Active health checker",
    research=[
        "Active health checks in load balancers: how nginx (max_fails/fail_timeout and the commercial health_check), HAProxy (check, inter, rise, fall) and Envoy (health checking) describe them. Cite official docs.",
        "Rise/fall thresholds and why a single failed probe should not eject a server (flapping); probe interval and timeout trade-offs (own reasoning marked).",
        "Liveness vs readiness for backends (Kubernetes docs, VERIFIED).",
        "Go `time.Ticker` and goroutine-per-task patterns; stopping goroutines with context (official docs).",
    ],
    code="""
Read internal/health/checker.go COMPLETELY and every other file in internal/health/ except endpoint.go (list first), the config keys for health (`probe_interval`, `probe_timeout`, defaults) in internal/config/config.go, how the checker is created and started (internal/app/app.go), how reload adds/removes checker goroutines, ADRs about health checking and the initial probe (docs/adr via INDEX.md), CONTEXT.md terms (probe, active check, admission).

### A. Types and constructor
`Checker` struct fields, `New` signature and arguments, what interfaces it consumes (define at consumer), constants `probeFailuresBeforeUnhealthy`, `probeSuccessesBeforeHealthy`, `probeInitialSuccessesBeforeHealthy` (`path:line`, values, comments quoted).

### B. Goroutine model
`Start`, `run`, one goroutine per backend, the ticker (interval), how goroutines are stopped (context/cancel), how a reload adds new backends and stops removed ones (`path:line`), the concurrency-model comment at the top of the file (quote).

### C. One probe
The probe request step by step: method, URL (which path? from config?), timeout, redirect policy (not followed: where?), success rule (2xx only), body handling, errors. Cite lines.

### D. State machine
Consecutive success/failure counters, thresholds, transitions (healthy->unhealthy after 3 failures; unhealthy->healthy after 2 successes; initial admission after 1), what `MarkHealthy`/`MarkUnhealthy` do and what logs/metrics are emitted on transition (reason labels, `path:line`). Build a table: current state | event | counters | new state | side effects.

### E. Interplay
How active checks interact with the passive outlier detector and the circuit breaker (who can eject, who can re-admit: only an active probe re-admits ejected backends? verify and cite). What happens to a backend that is draining.

### F. Timing example
Compute concrete timings from the defaults (5s interval, 2s timeout): worst-case time to detect a dead backend, and time to re-admit; show the arithmetic and cite the constants.

### G. Tests
Test names and `path:line` that describe thresholds and admission.
""",
    outline="""
- What an active health check is and why the project needs it (analogy: a nurse doing rounds), what it checks and what it cannot see.
- The `Checker`: types, constants, goroutine-per-backend model, ticker, stop/reload handling, with a Mermaid + ASCII diagram of goroutines and timers.
- One probe in detail (request, success rule, timeout, redirects).
- The state machine table and a Mermaid state diagram (consecutive failures/successes), the initial admission rule for new backends.
- A worked timeline using the real defaults (detection and recovery time) clearly derived from the constants.""" + NOT_SELECTED_BLOCK + """
- How it cooperates with outlier detection and the circuit breaker (who ejects, who re-admits), as a small table.
- General practice from VERIFIED research (rise/fall, flapping) compared with this project's choices.""" + WHY_BLOCK + IO_BLOCK + COMMON_TAIL,
    review_extra="Verify thresholds, defaults, the probe rules, and the timing arithmetic.",
)

SPECS[14] = dict(
    title="Passive outlier detection",
    research=[
        "Passive health checking / outlier detection: Envoy outlier detection docs, nginx passive checks (max_fails), HAProxy observe. Official sources.",
        "Why passive detection complements active probes (real traffic sees failures between probes) and its limits (needs traffic; slow to notice idle backends).",
        "Sliding window / ring buffer of recent outcomes: the idea in plain words (own wording marked).",
        "Ejection and re-admission policies (Envoy ejection time/backoff vs this project's probe-only re-admission: do not describe the project here).",
    ],
    code="""
Read internal/health/outlier.go COMPLETELY (and its tests' names), how it is registered as a proxy observer (internal/app/app.go), the observer interface the proxy defines (internal/proxy/proxy.go), how the proxy classifies an outcome (transport error, 5xx) and calls observers, and docs/adr about passive ejection (INDEX.md), CONTEXT.md terms (outlier, ejection).

### A. Types
`OutlierDetector`, `NewOutlierDetector`, constants `outlierWindowSize = 10`, `outlierFailuresBeforeEject = 5` (`path:line`, comments quoted), the per-backend window data structure.

### B. Observation flow
`ObserveRoundTrip` (the observer method): signature, what the proxy passes in, what counts as failure vs success, how the window is updated, when ejection triggers, what `MarkUnhealthy` is called with, logs and metrics. Step by step with `path:line`.

### C. Re-admission
Who re-admits an ejected backend (only an active probe? verify) and what resets the window.

### D. Concurrency
Locks/atomics used, the concurrency comment at the top of the file (quote).

### E. Interplay with circuit breaker
Both observe the same round trips: list the differences (window vs consecutive, thresholds 5-of-10 vs 3 consecutive, what each changes on the Backend, who clears it) as a table with `path:line`.

### F. Worked examples
Compute from the constants: a sequence of 10 outcomes where ejection triggers and one where it does not; show the arithmetic.

### G. Tests
Test names with `path:line`.
""",
    outline="""
- What passive detection is and why it exists next to active probes (analogy: noticing a shop is closed because customers keep coming back annoyed), what it cannot do.
- The detector's data structure (sliding window of the last 10 outcomes) with a diagram showing the window sliding.
- How observations arrive (the proxy's observer hook) step by step.
- The ejection rule (5 failures in the last 10) with 3 worked examples; re-admission by active probe only (state it exactly as the code shows).
- Comparison table: active checker vs outlier detector vs circuit breaker (what each watches, threshold, what it changes, who re-admits).""" + NOT_SELECTED_BLOCK + WHY_BLOCK + IO_BLOCK + COMMON_TAIL,
    review_extra="Verify the window logic with the real code, the failure classification, and the worked examples' arithmetic.",
)

SPECS[15] = dict(
    title="Circuit breaker",
    research=[
        "The circuit breaker pattern (Martin Fowler's bliki and Microsoft/AWS architecture docs): closed, open, half-open; why it exists; failure thresholds and cool-down. Cite what you can fetch.",
        "Circuit breaking in proxies (Envoy circuit breakers are connection/request limits, which differs from the Hystrix-style breaker): state this difference accurately with the Envoy docs.",
        "Retry storms and cascading failure in plain words (Google SRE book chapter on cascading failures, if fetchable).",
        "Compare-and-swap (CAS) in plain words and Go `atomic.CompareAndSwap*` docs.",
    ],
    code="""
Read internal/circuit/*.go COMPLETELY, the circuit state fields and CAS helpers on the Backend (internal/backend/backend.go), `CircuitGate` interface and `SetCircuitGate` (internal/backend/registry.go), how the proxy reports outcomes and asks permission (internal/proxy/proxy.go: circuit-gate denial -> 503 around lines 305-308), observer registration (internal/app/app.go), ADRs about the circuit breaker (INDEX.md), CONTEXT.md terms (circuit, half-open, trial).

### A. Types and constants
`Breaker`, `New(cooldown, log, collector)`, `circuitFailuresBeforeOpen = 3`, default cooldown (`DefaultCircuitCooldown` and key `circuit.cooldown`) with `path:line`.

### B. State machine
States Closed/Open/Half-Open as stored on the Backend (the CAS-guarded snapshot: describe the snapshot struct/enum), every transition: trigger, guard, code line. Table: state | event | action | next state | metric/log. How the cooldown is timed (timestamp compare, no timer goroutine? verify).

### C. `Allow` and the half-open trial
`Allow` step by step: what happens when closed, open before cooldown, open after cooldown (transition to half-open, exactly ONE trial request admitted: how is that enforced with CAS?), concurrent callers during half-open. `path:line` for each branch.

### D. Reporting outcomes
The observer method that receives round-trip outcomes, what counts as failure (transport error, 5xx), how consecutive failures are counted and reset by success, what happens on a failed or successful trial.

### E. Relationship to the selector
How `Registry.Selectable()` uses the gate (does it call Allow? or a non-mutating check?). The note about half-open handling in selectors (CircuitGate). Cite where a denied request becomes 503 in the proxy. Be precise about who consumes the trial slot.

### F. Metrics/logs
Circuit state gauge, transitions logged with reason labels: names and `path:line`.

### G. Worked timeline
From the constants: 3 failures -> open -> 30s cooldown -> half-open -> trial success/failure, with the arithmetic and code lines.

### H. Tests
Test names with `path:line`.
""",
    outline="""
- What a circuit breaker is (analogy: the electrical fuse/breaker in a house, clearly labelled), why the project needs it, the three states.
- The Mermaid state diagram plus an aligned ASCII version, with the real thresholds and cool-down.
- The `Breaker` and the CAS-guarded snapshot on the Backend (what CAS is in plain words, VERIFIED research), `Allow` step by step, the single-trial rule in half-open.
- How outcomes are reported (observer) and what counts as a failure.
- How the circuit state is used by the registry/selector and where a denied request becomes a 503 (`path:line`).
- Worked timeline with real numbers from the constants.
- Honest comparison with the Envoy circuit-breaker concept (VERIFIED research) and with the outlier detector.""" + NOT_SELECTED_BLOCK + WHY_BLOCK + IO_BLOCK + COMMON_TAIL,
    review_extra="Verify every state transition and the single-trial enforcement against the code; verify the worked timeline arithmetic.",
)

SPECS[16] = dict(
    title="Metrics, logging and admin endpoints",
    research=[
        "Prometheus basics from prometheus.io: pull model, metric types (counter, gauge, histogram), labels, and the label-cardinality warning (official best practices page).",
        "Structured logging: what `log/slog` is (official docs/blog), key-value fields, why structured logs help.",
        "Kubernetes liveness/readiness/startup endpoints (VERIFIED) and pprof in Go (net/http/pprof official docs) including the security warning.",
        "The RED and four golden signals ideas (Google SRE book / Tom Wilkie), one trusted source each if fetchable.",
    ],
    code="""
Read internal/metrics/*.go COMPLETELY, internal/logger/*.go (logger.go, doc.go, vocab.go), internal/app/admin.go, the mux registration of the metrics listener in internal/app/app.go (metrics, pprof, admin), internal/health/endpoint.go, deployments/docker observability config (Prometheus scrape config, Grafana dashboards: list files and what they show), demo/ dashboard if any, and ADRs about metrics/logging/cardinality (INDEX.md).

### A. Metrics
Every instrument: name, type (counter/gauge/histogram), labels, help text, buckets (`path:line`), the `Collector` methods that update them and WHO calls each (`path:line`). A table: metric name | type | labels | updated by | meaning. Note the deterministic label transitions (set new before deleting old) and any label that could be unbounded (e.g. a `method` label): verify and cite. The private registry and `/metrics` handler.

### B. Logging
Logger construction (JSON? level?), the field-name vocabulary in doc.go/vocab.go (list every field name constant and reason value), and 8-10 real log call sites with the fields they pass (`path:line`). What a student will see in the output for startup, request failure, ejection, circuit open, reload.

### C. Admin endpoints
`/admin/reload` (POST) and `/admin/reload/last` (GET): handler code, request/response format (JSON fields), status codes, concurrency (reload mutex), what 'last' returns; pprof mounting; authentication (none). `path:line`.

### D. Health endpoints
`/livez`, `/readyz`, `/startupz` conditions (exact) and responses, listener and port (brief; Chapter 8 covers wiring).

### E. Observability stack in the repo
What the docker observability files provide (Prometheus targets, Grafana dashboards, panel queries if present). `path` and what a student can look at.

### F. Example outputs
Only if you can derive them from code: an example of the Prometheus exposition lines for two metrics, and an example JSON log line, clearly marked as constructed from the code and not run output.
""",
    outline="""
- Why observe a load balancer (what questions metrics, logs and endpoints answer).
- Prometheus primer (VERIFIED research) and the project's metrics: the full metrics table, label design, cardinality note (be honest about any unbounded label).
- Logging: slog primer, the project's field vocabulary, real log examples at key events.
- The admin API: reload endpoints (request, response, errors), pprof, and the missing authentication (honest).
- Health endpoints: what each answers and who uses it.
- The observability stack files (Prometheus/Grafana) and what to look at when the demo runs.
- A diagram of what flows where: request path -> metrics/logs; control path -> admin; probes -> health endpoints (Mermaid + ASCII).""" + WHY_BLOCK + IO_BLOCK + COMMON_TAIL,
    review_extra="Verify every metric name, type, label and updater against the code, and every log field name against vocab.go/doc.go.",
)

SPECS[17] = dict(
    title="One request, end to end",
    research=[
        "A well-known end-to-end request walkthrough for an HTTP reverse proxy (nginx.org docs or Cloudflare learning): keep to VERIFIED statements.",
        "How to read a distributed trace / request timeline (own wording is fine; mark it).",
        "Tail latency basics: why p99 matters for load balancers (a trusted source such as 'The Tail at Scale' paper abstract, if fetchable).",
    ],
    code="""
This chapter ties everything together with ONE worked example. Using the demo configuration (configs/example.yaml or the demo config under demo/), three backends named as in the config, algorithm round_robin: trace ONE request GET /hello from curl through the system, step by step, citing `path:line` for EVERY step (use Chapter 4's walk as a skeleton: read the book's Chapter 4 block in docs/PROJECT_GUIDE.md for the 15 steps, but VERIFY each against the code yourself).

### A. Happy path
Each step: function, `path:line`, what goes in, what comes out, which counters/metrics/logs change (with metric names and log fields). Include the exact objects touched: Registry.Selectable (conditions), the Selector (round-robin counter value change), active counter increment/decrement, outlier/circuit observers, EWMA update.

### B. Variant 1: the chosen backend is down (connection refused)
What happens in the proxy, the observers, circuit counters, outlier window, active checker; the client sees 502; what the NEXT request does. Cite lines.

### C. Variant 2: backend slow and then the client cancels
Context cancellation path, counter release, status recorded.

### D. Variant 3: all backends unavailable (503)
Which conditions lead there.

### E. Variant 4: algorithm = least_conn and p2c_ewma
What changes in the Select step only (one paragraph each, cite the Select functions).

### F. State changes over time
Build a table of the visible state of the system (active counters per backend, health, circuit, outlier window count) before, during and after the happy-path request and after Variant 1, derived from the code.
""",
    outline="""
- The story: one `curl` request, three backends (use the real names/addresses from the config), told as a numbered narrative that cites `path:line` at every step and points to the chapter with the deep dive.
- A big Mermaid sequence diagram and an aligned ASCII version covering the whole happy path.
- The "state of the world" table before/during/after.
- Variant stories: backend refuses connection (502 and what the system learns), slow backend + client cancel, no backend available (503), a different algorithm (what changes in the Select step only).
- A "what each block saw" matrix: rows = blocks (server, proxy, selector, registry, backend counters, health checker, outlier detector, circuit breaker, metrics, logger), columns = happy path / refused / cancel / 503.
- A "debugging checklist" for a student: given a symptom (502, 503, slow), which logs/metrics/endpoints to look at and which chapter explains it.""" + IO_BLOCK + COMMON_TAIL,
    review_extra="Verify the narrative step by step against the code and against Chapter 4; the variant behaviours must match the code exactly.",
)

SPECS[18] = dict(
    title="Hot reload without downtime",
    research=[
        "How nginx reloads configuration without dropping connections (nginx.org control docs: old workers finish, new workers start), and how HAProxy does seamless reloads. Official sources only; mark gaps.",
        "Connection draining / deregistration delay concepts (AWS ELB docs).",
        "Atomic configuration swap, immutable snapshots and read-copy-update (RCU) idea in plain words (one trusted source; mark OWN WORDING).",
        "SIGHUP convention for reload (man7 signal(7)).",
    ],
    code="""
Read internal/app/app.go (Reload, reloadLocked, ReloadFrom if there), internal/app/admin.go (admin reload handler), internal/app/drain.go (drainBackend), cmd/l7LoadBalancer/main.go (reloadLoop, drainSignals, SIGHUP channel buffer), internal/config/diff.go, internal/backend registry Apply and Retire, the proxy's drain context join, ADR-0015 (reload trigger), ADR-0016 (drain window), other reload ADRs (INDEX.md), tests about reload and drain (names).

### A. Triggers
SIGHUP path: signal channel buffer 1, `reloadLoop`, coalescing (`drainSignals`), config re-read. Admin path: POST /admin/reload, response JSON, `GET /admin/reload/last`. Both converge on one function: name it with `path:line`. The reload mutex. ADR-0015 decision that the file watcher was rejected (quote).

### B. The reload algorithm step by step
`Reload`/`reloadLocked` in order: take lock, load + validate new config, `NonBackendChanges` (reject whole reload if anything else changed: which fields), `DiffBackends` (added/removed/updated), algorithm change handling (build new selector first, then atomic store), registry Apply, start probes for new backends, start drain for removed backends, record the last-reload result, metrics/logs. For each: `path:line`, input, output, failure behaviour (what is left unchanged if a step fails).

### C. Adding a backend
New backend created UNHEALTHY and admitted only after the first successful probe (`probeInitialSuccessesBeforeHealthy = 1`): cite; consequences for traffic.

### D. Removing a backend (drain)
The removed backend is marked draining/removed so no NEW requests are selected (cite how), in-flight requests continue; `drainBackend` polls every 100ms until active count is zero or the drain window (default 30s) expires; then `Retire` cancels the backend context with `ErrDrainWindowExpired`; what clients see for requests still in flight at expiry (502 mapping at proxy.go:430-431 area); the drain race noted in earlier reviews: verify in code and describe precisely whether a request loaded before the swap can complete after the removal flag is set.

### E. Updating in place
Weight/timeout changes: no new backend object, counters preserved.

### F. What is NOT reloadable
Exact list and the rejection error message; transport/server settings.

### G. Edge cases
Replacing ALL backends (window with no selectable backend -> 503; README admits it), reload during shutdown (context), rapid SIGHUPs, invalid config (old config keeps running).

### H. Tests
Names with `path:line` for reload, drain, coalescing.
""",
    outline="""
- The promise: change backends while traffic flows. How nginx does it (VERIFIED research, one paragraph) versus this project's approach (single process, atomic swap + drain).
- The two triggers (SIGHUP and the admin endpoint), coalescing of repeated signals, the reload mutex (diagrams).
- The reload algorithm as a numbered flow with a Mermaid flowchart (including the "reject" and "keep old config" exits) and an aligned ASCII version.
- Timeline diagrams (Mermaid sequence + ASCII) of three scenarios with a client sending requests all the time: (1) add a backend, (2) remove a backend, (3) change the algorithm. Show exactly when the registry/selector changes and what in-flight requests experience.
- The drain in detail (poll, window, retire, 502 at expiry) and the honest drain race.
- What cannot be reloaded and why (whole-reload rejection).
- Edge cases table.""" + NOT_SELECTED_BLOCK + WHY_BLOCK + IO_BLOCK + COMMON_TAIL,
    review_extra="Verify the order of steps in the reload function, the drain constants, the 502 mapping, and the claims about the drain race.",
)

SPECS[19] = dict(
    title="Switching the selector algorithm at runtime",
    research=[
        "Atomic pointer swap vs locking for hot-swapping behaviour in a running server (Go `atomic.Pointer` docs, VERIFIED) and the memory-visibility guarantee in plain words.",
        "What state is lost when a load-balancing algorithm is swapped (round-robin position, consistent-hash ring, latency estimates): general reasoning, mark OWN WORDING.",
        "Blue/green and canary deployment ideas (one trusted source) to relate to algorithm changes.",
    ],
    code="""
Focus: what happens when the YAML `algorithm` changes and a reload is triggered. Read internal/app/app.go (reloadLocked: selector rebuild and swap, lines around 250-259), internal/balancer/selector.go (NewFromConfig), the Proxy's selector holder (internal/proxy/proxy.go), each selector constructor (roundrobin.go, leastconn.go, consistent_hash.go + ring.go, p2c_ewma.go) for the state it creates, the tests for algorithm swap (names in internal/app/*_test.go, internal/proxy/*_test.go), the demo control service that switches algorithms (demo/ folder: find how the demo UI triggers a reload with a new algorithm), ADRs on algorithm hot-swap (INDEX.md).

### A. The swap code
Step by step with `path:line`: new config loaded and validated, algorithm differs -> `balancer.NewFromConfig(cfg, registry)`, error behaviour (old selector kept), the atomic store, the log/metric emitted, `last reload` record.

### B. What a request sees
A request that already called `Load()` keeps the old selector for its whole `Select` call; the next request loads the new one. Cite the Load call. Explain atomicity (what the atomic pointer guarantees).

### C. State carried or lost
For each selector: what internal state it builds in its constructor (round-robin counter; least-conn none?; consistent-hash ring of 150 vnodes per backend; p2c per-backend EWMA is stored where? on the Backend (kept across swaps) or in the selector?). Verify and cite. What is preserved (backend active counters, EWMA on Backend) and what resets.

### D. Interaction with registry changes in the same reload
If backends and algorithm change in the same reload: order of operations and which selector sees which registry snapshot (cite).

### E. The demo
How the demo stack switches algorithm live (files, endpoints, what the dashboard shows) with `path:line`.

### F. Tests
Names with `path:line`.
""",
    outline="""
- The question: how can we change the algorithm without dropping a single request?
- The answer in one picture: Proxy holds a pointer to the current selector; reload builds a new selector and swaps the pointer (Mermaid sequence with requests in flight + aligned ASCII before/during/after).
- The code step by step (`path:line`), including failure handling (bad algorithm name -> reload rejected, old selector keeps running).
- What a request sees; what state carries over and what resets, as a table per algorithm.
- What happens when backends AND algorithm change together.
- The demo: switching algorithms live and what to watch.
- A worked example: round_robin -> least_conn with three backends and a few requests, showing which backend gets each request before and after the swap (derived from the code; label as illustrative).""" + IO_BLOCK + WHY_BLOCK + COMMON_TAIL,
    review_extra="Verify the swap ordering, what state is preserved or reset per selector, and the demo wiring.",
)

SPECS[20] = dict(
    title="Graceful shutdown",
    research=[
        "Graceful shutdown of an HTTP server in Go: `http.Server.Shutdown` semantics (official docs), difference from `Close`.",
        "Kubernetes pod termination sequence: SIGTERM, terminationGracePeriodSeconds, preStop, endpoints removal race (kubernetes.io docs, VERIFIED).",
        "Why a load balancer should stop reporting ready before it stops accepting traffic (own reasoning marked; cite the Kubernetes readiness docs).",
        "`signal.NotifyContext` (official docs).",
    ],
    code="""
Read cmd/l7LoadBalancer/main.go (signal context: SIGINT/SIGTERM), internal/app/app.go `Run` (shutdown section, `shutdownTimeout` = 10s), how the health checker and observers stop (context), how /readyz behaves during shutdown (internal/health/endpoint.go: does readiness flip? or is there a documented gap?), the drain of in-flight requests (http.Server.Shutdown semantics in words), reload-versus-shutdown interplay (reload loop uses sigCtx), ADRs about shutdown/readiness (INDEX.md), tests about shutdown (names).

### A. Trigger
Signal context creation and which signals; what `Run` does when the context is cancelled. `path:line`.

### B. Shutdown order
The exact order in which the three servers are shut down (client, metrics, health), the shared 10s timeout (constant and whether it is one budget for all or per server), what happens on timeout, how errors are returned, exit code. Cite lines 372-383 area but verify.

### C. In-flight requests
What `Server.Shutdown` does (words), what happens to requests still running at timeout, active-request counters, the drain contexts of backends.

### D. Background workers
How the checker/outlier/circuit/reload goroutines stop (context cancellation), and whether the code waits for them (WaitGroup? none). Verify.

### E. Readiness during shutdown
Does `/readyz` report not-ready once shutdown starts? Cite code; if there is a gap (the health server is shut down last / readiness not flipped) say exactly what the code shows and what ADR/doc says.

### F. Timing under an orchestrator
Using the constants: relate the 10s timeout to a typical Kubernetes grace period of 30s (VERIFIED research) in a short table: only statements derivable from the code and the research.

### G. Tests
Names with `path:line`.
""",
    outline="""
- What 'graceful' means (analogy: a restaurant closing: stop seating, finish meals, then lock the door).
- Trigger and order: a numbered flow with Mermaid sequence + aligned ASCII, from SIGTERM to process exit, with `path:line`.
- What happens to in-flight requests, to the background workers and to reloads during shutdown.
- Readiness during shutdown: the exact behaviour and any gap (honest).
- A timing table with the 10s budget and Kubernetes-style grace periods (VERIFIED research only).
- Common mistakes in graceful shutdown in Go (VERIFIED research or marked OWN WORDING).""" + IO_BLOCK + WHY_BLOCK + COMMON_TAIL,
    review_extra="Verify shutdown order, the single timeout budget semantics, and the readiness behaviour exactly as coded.",
)

SPECS[21] = dict(
    title="The four selector algorithms",
    research=[
        "Round robin, least connections, consistent hashing, power of two choices (P2C): authoritative explanations (nginx.org upstream docs, HAProxy docs balance, Envoy load balancers docs, Mitzenmacher's 'power of two choices' abstract, Karger et al. consistent hashing abstract).",
        "Consistent hashing with bounded loads (Mirrokni, Thorup, Zadimoghaddam; Google research blog post / arXiv abstract): the capacity bound c = ceil(avg * (1 + epsilon)).",
        "EWMA (exponentially weighted moving average) in plain words with the formula (a statistics or Wikipedia reference) and how Finagle/Linkerd use peak-EWMA for load balancing (Linkerd docs, VERIFIED if fetchable).",
        "FNV-1a hash definition (the FNV site or Wikipedia) and why a finalizer/mixer such as `fmix64` (MurmurHash3 finalizer) improves distribution (marked SECONDARY if only Wikipedia).",
        "Virtual nodes in consistent hashing: why many points per server smooth the distribution.",
    ],
    code="""
Read completely: internal/balancer/roundrobin.go, leastconn.go, consistent_hash.go, ring.go, hashkey.go, p2c_ewma.go, naive_consistent_hash.go (the unwired comparison implementation), and the tests of each (names and the key cases), plus internal/backend/backend.go (EWMA fields and update method, `ewmaAlpha`, active counter), internal/proxy/proxy.go (`p2cFailurePenalty`, where EWMA and failure penalty are recorded), docs/adr entries about each algorithm (INDEX.md), docs/design-decisions.md algorithm parts, and bench/ results for algorithms if present.

For EACH algorithm give: constructor (`path:line`), `Select` step by step in execution order with `path:line` (what it reads from Registry.Selectable(), which atomics/locks it touches, what it returns), the constants and their values, per-call cost, what state it keeps, how it treats the circuit half-open trial, how it behaves with 1 backend and 0 backends, and the concurrency comment.

### Round robin
Counter type and increment, modulo arithmetic, what happens when the selectable set changes between calls.

### Least connections
How the least-loaded backend is chosen (tie-breaking rule), where the increment actually happens (the proxy, not the selector: cite), why the selector does not increment ('deferred increments' ADR note).

### Consistent hash with bounded loads
Hash key from the client address (`requestHashKey`: port stripped?), ring construction (150 virtual nodes per backend, FNV-1a + fmix64: cite functions), ring lookup (binary search? cite), capacity formula `math.Ceil(avg * (1 + capacityEpsilon))` with `capacityEpsilon = 0.25`, how 'avg' is computed (active counts? +1?), the spill-over rule to the next node on the ring, ring rebuild when backends change (when/how), key remap property.

### P2C with EWMA
Two distinct random picks (`rand.IntN`), compare latency EWMA only (not active count? verify), EWMA update formula and `ewmaAlpha = 0.1` (backend.go), the failure penalty (`p2cFailurePenalty = 2 * time.Second`: how it is applied), 'sample-count decay' instead of time decay (explain from the code), cold start (no samples) behaviour.

### naive_consistent_hash.go
Why it exists (comparison in the hot-key test, hotkey_test.go:159) and how it differs.

### Worked numeric examples
Using constants only, compute small examples the writer can reuse: (1) round robin over 3 backends for 7 requests; (2) least-conn with active counts [3,1,2] and a tie; (3) bounded-load capacity for 4 backends and 20 in-flight requests (avg, capacity, which are full); (4) EWMA after samples [100ms, 100ms, 500ms] with alpha 0.1 starting from the first sample (state your assumption about initialisation from the code); (5) P2C choosing between two backends with EWMA 120ms and 80ms. Show the arithmetic.

### Benchmarks/evidence
Any algorithm comparison data in bench/ or docs with file and lines.
""",
    outline="""
- Why algorithm choice matters (analogy: supermarket checkout line choices), what each algorithm optimises.
- One `###` section per algorithm in the order round robin, least connections, consistent hash with bounded loads, P2C with EWMA, each with: the idea in plain words and an analogy; the step-by-step algorithm as a numbered list; the code walk (`path:line`); an ASCII/Mermaid diagram (for the hash ring draw the ring with virtual nodes and the spill-over; for P2C draw the two-choice comparison); a worked numeric example derived from the code constants (clearly labelled illustrative); strengths and weaknesses; when to use it.
- A big comparison table (state kept, cost, uses request info?, handles slow backends?, handles hot keys?, sticky?, weaknesses).
- The unwired `naiveConsistentHash` and why it exists.
- How to choose, plus how to switch (YAML `algorithm:` values) and what happens when you switch (link to Chapter 19).""" + NOT_SELECTED_BLOCK + COMMON_TAIL,
    review_extra="Verify every constant, every formula and every worked example's arithmetic; verify each Select walk order against the code.",
)

SPECS[22] = dict(
    title="Algorithms not in this project",
    research=[
        "Weighted round robin and smooth weighted round robin (nginx's smooth WRR algorithm description / the nginx commit explanation; HAProxy weights).",
        "Random choice, least response time / fastest, IP hash (nginx ip_hash), URL/header hash, consistent hashing variants: rendezvous (highest random weight) hashing (Thaler & Ravishankar abstract), Maglev (Google paper abstract / NSDI 2016), Jump consistent hash (Lamping & Veach abstract), ring hash in Envoy.",
        "Least request (Envoy), join-shortest-queue, peak EWMA (Linkerd), 'power of k choices', resource-based/adaptive balancing and slow start.",
        "Session affinity / sticky sessions with cookies (nginx sticky, HAProxy cookie) and the trade-offs.",
        "Geo/latency-based and global balancing at DNS level (brief pointer to Chapter 3).",
        "For EACH algorithm: one-paragraph description, how it picks a server, pros, cons, a real product that uses it, source URL. Mark any gap.",
    ],
    code="""
Goal: find what THIS repository says about algorithms it does NOT implement, and whether any related code exists.

### A. Accepted algorithm names
`implementedAlgorithms` and the constants in internal/config/config.go (all four names, `path:line`), the validation error for unknown names.

### B. The weight field
Is `weight` used by any selector? grep for Weight/weight across internal/ (non-test) and report every use with `path:line`. State exactly which selectors honour weights (probably none) and what docs say about weights.

### C. ADRs and docs about rejected or deferred algorithms
Search docs/adr/*.md, docs/design-decisions.md, docs/what-id-do-differently.md, README.md for mentions of: weighted, random, ip_hash, least response time, maglev, rendezvous, jump, sticky, session affinity, cookie, power of k, EWMA alternatives. Quote each mention (short) with file:line and say whether it is 'rejected', 'deferred' or 'future work'. Quote the scope ADR (ADR-0005) and the 'ask before adding features' rule in AGENTS.md.

### D. Extension points
How would a new algorithm be added in this codebase? List the exact places to touch (interface, factory switch, config constants and validation, tests, docs): `path:line` each, derived from the code.

### E. Things that look like algorithms but are not
Hash key derivation, the unwired `naive_consistent_hash.go`: what they are (cite).
""",
    outline="""
- Why this chapter: a complete picture of the algorithm landscape and how the project's four fit in; which ones were left out and why (quote the repo's own scope decisions; do not invent reasons).
- A table of the landscape: algorithm | one-line idea | picks how | needs what state | pros | cons | real products | in this project? (VERIFIED research only for the product claims).
- One short `###` per algorithm not included (weighted round robin and smooth WRR, random, IP hash and URL/header hash, least response time, rendezvous hashing, Maglev, jump consistent hash, ring hash variants, least request/join-shortest-queue, peak EWMA, power of k, session affinity/sticky cookies, resource-based/adaptive, geo/latency-based) with a tiny diagram or worked example each where it helps.
- Honest section about the config `weight` field: what the code does with it.
- "How would you add one here?": the exact extension points in this codebase (`path:line`), as a step list a student could follow (an exercise, clearly not done in this project).
- A decision flowchart: which algorithm should you pick for which workload (Mermaid + ASCII), flagged as general guidance.""" + COMMON_TAIL,
    review_extra="Verify the weight-field claims by grep, every quote from ADRs/docs, and that products named are supported by VERIFIED research.",
)

SPECS[23] = dict(
    title="Demo, tests and benchmarks",
    research=[
        "Go testing basics: `testing` package, table-driven tests, subtests, `httptest`, the race detector `-race` (official docs/blog).",
        "Load testing and benchmarking basics: what p50/p95/p99 mean, throughput vs latency, why benchmarks are noisy (a trusted source; mark OWN WORDING).",
        "Chaos testing idea (Principles of Chaos Engineering site) in plain words.",
        "Go benchmarks (`testing.B`) and `go test -bench` (official docs).",
    ],
    code="""
Read the demo: demo/ (list everything; README or docs/demo; the control service; the docker compose; ports bound to 127.0.0.1; how to start and what the dashboard shows), deployments/docker (dummy backends, nginx comparison, chaos, observability: list files and purposes), test/ folders (list; chaos harness under test/chaos: scenarios), Makefile targets (`make help`), bench/ (scripts, RESULTS.md, README: methodology, numbers, caveats, machine), docs/demo if present, the test layout (count `_test.go` files per package, list the notable integration/chaos/soak tests by name and `path:line`), and `internal/app` Build usage in tests.

### A. The demo
Exact steps to start it (commands from README/demo docs), services and ports (`path:line`), what the control page and dashboard do, how it triggers reloads/algorithm switches, how it injects failures (kill a backend?).

### B. Test pyramid in this repo
Unit tests per package (counts of test functions via grep -c '^func Test'), integration tests using httptest, the chaos harness (scenarios and what each asserts), soak test, race-detector usage (`make test-race`). Quote `make help` lines for each target.

### C. Benchmarks
Go benchmarks (names, what they measure: `BenchmarkProxyServeHTTP` etc. with `path:line`), the bench/ load tests against Nginx: methodology, hardware, concurrency levels, the results table (copy numbers with file and lines), what the README says about p99 being higher than Nginx, which benchmarks were skipped as unreliable and why.

### D. How to run each
Exact commands.

### E. What the tests do NOT cover
From docs (e.g. docs/what-id-do-differently.md) and from grep: honest gaps (WebSocket upgrade? verify whether any test covers 101 Switching Protocols; X-Forwarded-For test? none).
""",
    outline="""
- Why demos, tests and benchmarks matter for a systems project.
- The demo: how to start it, what each service is, what to click, with a diagram of the demo stack (Mermaid + ASCII) and a guided "5-minute tour" (steps derived from the repo's docs).
- The tests: layout, how to run, table-driven and httptest patterns with 2 real examples, the race detector, the chaos harness scenarios table, the soak test.
- Benchmarks: Go benchmarks and the Nginx comparison; reproduce the repo's results table faithfully and explain the numbers; how to read p50/p99 (VERIFIED research); honest caveats the repo states.
- What is NOT covered by tests (honest list with evidence).
- Exercises: 5 things a student can do with the demo to see each chapter's behaviour (kill a backend, change algorithm, reload config, ...).""" + COMMON_TAIL,
    review_extra="Verify the demo commands, ports, test counts, benchmark numbers (copy exactly) and every claim about test coverage by grep.",
)

SPECS[24] = dict(
    title="Known limitations",
    research=[
        "Why WebSocket and other `Upgrade` requests need special handling in proxies (RFC 6455 / RFC 9110 section 7.8 Upgrade; MDN; nginx WebSocket proxying doc) and what `httputil.ReverseProxy` does with 101 responses (Go docs).",
        "HTTP retries and idempotency (RFC 9110 section 9.2.2 idempotent methods) and retry budgets (Google SRE book).",
        "Prometheus label cardinality (official best practices).",
        "Securing admin/pprof endpoints (Go `net/http/pprof` docs warnings; generic guidance with a trusted source).",
    ],
    code="""
This chapter must be EVIDENCE-BASED. For each candidate limitation below, VERIFY it in the code/tests/docs and report one of: CONFIRMED (with `path:line` evidence), PARTLY (what is true), NOT TRUE (what the code does), or UNVERIFIABLE. Never report a limitation you did not verify.

Candidates:
1. WebSocket/`Upgrade` requests: does the proxy handle 101 Switching Protocols? Read the proxy's response handling (ModifyResponse body wrapper that may break Upgrade responses) and any test for upgrades; state what happens (502?) and why (cite lines).
2. No retries (ADR-0018): quote the decision.
3. Drain race: a request that loaded the registry before a backend removal completing after removal flags are set; read the drain code and the ADR (0016) and describe precisely what is and is not guaranteed.
4. `Select` described as read-only but mutates state (documented? tested?).
5. Admin endpoints and pprof without authentication; listener binding (all interfaces?): cite.
6. Metrics `method` label cardinality (unbounded label values?): verify how the label value is derived.
7. Outcomes recorded at response headers, not body end: verify.
8. Shutdown readiness gap (health server shut down last): verify.
9. `WriteTimeout` omitted deliberately: trade-off statement in the repo.
10. Performance vs Nginx (bench results) and HTTP/2 benchmarks skipped.
11. Replacing all backends in one reload causes a window of 503s (README admits it).
12. h2c dependency deprecated upstream (go.mod line, docs mention).
13. No CI (.github absent), no TLS to the admin/health listeners, TLS reload not supported (non-reloadable server/transport settings: verify the whole-reload rejection).
14. X-Forwarded-For not stripped/validated (client-supplied values appended by ReverseProxy).
15. Anything else the repo itself lists: read docs/what-id-do-differently.md, README.md 'limits' sections, docs/design-decisions.md, docs/adr/INDEX.md statuses: list each stated limitation with file:line.

For each CONFIRMED item also give: how a student could observe it, the impact, and what a fix would involve (one sentence, derived from the code/ADRs; label opinions as such).
""",
    outline="""
- Why an honest limitations chapter (engineering maturity; the project says it is not hardened).
- One `###` per VERIFIED limitation, each with: what happens (plain words), evidence (`path:line`), how to observe it, impact, possible fix. Group them: correctness, resilience, security, observability, performance, operations.
- A summary table: limitation | severity (your judgement, labelled as opinion) | evidence | fix effort.
- A section "limitations the repo documents itself" quoting its own docs.
- A section "things that look like bugs but are deliberate" (for example no WriteTimeout, no retries) with the repo's reasoning.
- Student exercises: pick one limitation, write a test that shows it, sketch a fix (do not implement).""" + COMMON_TAIL,
    review_extra="Every limitation must be backed by verified evidence; reject anything speculative; check the severity table is labelled as opinion.",
)

SPECS[25] = dict(
    title="Glossary and further reading",
    research=[
        "Curated further reading with VERIFIED URLs only: Go (A Tour of Go, Effective Go, Go blog on context and concurrency), networking (Beej's guide, High Performance Browser Networking), load balancing (nginx, HAProxy, Envoy docs), reliability (Google SRE book chapters on load balancing and cascading failures), consistent hashing and P2C papers. For each: title, author/site, URL, one-line why a student should read it.",
        "Verify each URL actually opens and says what you claim; drop any you cannot verify.",
    ],
    code="""
Collect the reference material for the appendices.

### A. Constants and defaults
Every named constant and default a student may ask about, with value and `path:line`: ports/addresses, timeouts, thresholds, window sizes, ring size, epsilon, EWMA alpha, penalty, drain window, shutdown timeout, probe interval/timeout, circuit cooldown, poll intervals, buffer sizes. Group by package. Verify each by viewing the line.

### B. ADR index
Read docs/adr/INDEX.md and each ADR title/status: list ADR number | title | one-line decision | status | which chapter of the book covers it (use the book's table of contents in docs/PROJECT_GUIDE.md).

### C. Endpoints and ports cheat sheet
Every HTTP endpoint the program serves (client, metrics/admin, health) with method, path, purpose, `path:line`.

### D. Metric names
All metric names exposed (names only), `path:line` of definition.

### E. Config keys
A one-line list of every YAML key (use internal/config/config.go).

### F. Make targets and commands
`make help` output summary, the common docker/demo commands.

### G. Terms
Read CONTEXT.md and every chapter's glossary-like content in docs/PROJECT_GUIDE.md (chapters 1-24 as they exist now): list every technical term that the book explains (hop-by-hop header, round trip, eject, Director, terminate, socket, upstream, backend, ALPN, TLS, CAS, EWMA, ...) with the chapter where it is first explained (search the book).
""",
    outline="""
- A complete A-Z glossary: every project term from CONTEXT.md and every networking/Go/algorithm term the book introduced, each with a plain-English definition (1-2 sentences), the chapter number where it is explained and, where it exists, `path:line`.
- Cheat sheets: constants and defaults table; endpoints and ports; metric names; YAML keys; make targets; ADR index mapped to chapters.
- A "map of the book": one table: question a student may have -> chapter.
- Further reading: only VERIFIED URLs from the research file, grouped by topic.
- A final self-assessment: 12 mixed questions across the whole book with answers inside `<details>`.
- Closing note: what the student can now do, and 5 suggested extension projects (clearly suggestions).""",
    review_extra="Verify every constant and value, every endpoint, every ADR row against the files, and that every glossary term's chapter pointer is correct by searching the book.",
)
