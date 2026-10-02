# Sprint 1 + Sprint 2 (T1–T2) — Codebase Walkthrough & Spec-Drift Audit

**For**: Darshan (project owner) · **Audit date**: 2026-09-19 · **Auditor**: opencode (read-only; no files modified except this one)

**Scope**: everything `[DONE]` in `PROGRESS.md` through S2.T2 — Sprint 1 (T0–T10) and Sprint 2's
consistent-hash work (S2.T1.1, S2.T1.2, S2.T2). `p2c_ewma` remains a panic stub and is out of
scope except where noted.

---

## Summary

- Packages implemented: **6 of 9** (`config`, `backend`, `balancer`, `proxy`, `logger`, `cmd/l7LoadBalancer`); `metrics`, `health`, `circuit` are Sprint 3 stubs (doc-only files).
- Sprint 1 tasks marked DONE: **14 of 14** entries in `PROGRESS.md` (T0, T0.5, T1, T2, T2-fix, T2.6, T3, T4, T5, T6, T7, T8, T9, T10); verified against code: **14 of 14** (T9 verified statically — files present, live docker smoke documented in the session log, not re-run by this audit).
- Sprint 2 tasks marked DONE: **2 of 2** spec-level tasks (S2.T1, S2.T2, tracked as 3 fine-grained tickets); verified against code: **2 of 2**, including a fresh run of the ADR-0009 60-seed offline reproducer, which reproduced the published figures exactly.
- Spec drift findings: **9** (of which P0: **0**, P1: **1**, P2: **8**).
- Race conditions found by `go test -race ./...`: **0** (fresh run, cache cleared; `go vet` and `gofmt -l` also clean).
- Recommended action before proceeding to Sprint 2 T3: fix the one stale hot-key figure in `AGENTS.md` key-decision #17 (it says naive 3,996; your checked-in test and ADR-0009 say 4,005) — then proceed; there are no code-level blockers.

**Priority definitions**: P0 = correctness bug/race/drift that breaks future sprints. P1 = meaningful
drift that will cause interview claims to fail. P2 = minor drift, wording, or polish; can defer.

---

# Part I — Package-by-package walkthrough

## 1. `internal/config` — strict YAML loading and validation

**Files**: `config.go` (193 lines), `doc.go`, `config_load_test.go`, `config_validate_test.go`.

**Purpose**: be the single validated source of truth for listen address, algorithm, and the backend
list, and reject bad config *at load time* rather than at request time.

**Public API** (`config.go`):
- `Config` struct — `Listen`, `Algorithm`, `Backends []BackendConfig` (`config.go:47-51`)
- `BackendConfig` — `Name`, `URL` strings (`config.go:54-57`)
- `Load(path) (*Config, error)` (`config.go:70-84`)
- `(*Config).Validate() error` (`config.go:99-138`)
- Algorithm constants `AlgorithmRoundRobin`/`AlgorithmLeastConn`/`AlgorithmConsistentHash`/`AlgorithmP2CEWMA` (`config.go:22-27`)

**Key design points, each with its documented why**:
- **Strict YAML**: `yaml.NewDecoder(f).KnownFields(true)` (`config.go:78-79`), never `yaml.Unmarshal`.
  A typo like `listn` is a load error, not a silently ignored field. Required by the S1.T2
  acceptance and `docs/design/sprint-1-contracts.md:93-95`.
- **Load does not validate**: pure deserialization; `main.go` calls `Validate` separately
  (`main.go:37-45`). The split is stated in the `Load` doc comment (`config.go:59-69`).
- **`Validate` normalizes before validating**: an empty `Algorithm` becomes `round_robin`
  (`config.go:100-102`), so downstream consumers never re-check or re-default. The mutation is
  documented inline (`config.go:90-94`).
- **Vocabulary vs acceptance** (ADR-0004): the four constants are the canonical vocabulary, but
  `Validate` accepts only `implementedAlgorithms` (`config.go:35-39` — currently `round_robin`,
  `least_conn`, `consistent_hash`; `p2c_ewma` still rejected). Validation answers "will this
  work?", not "does this parse?".
- **Uppercase-scheme rejection** (S1.T2-fix, a real bug found by review): `url.Parse` lowercases
  the scheme it reports, so `HTTP://host` passed a post-parse check. The fix checks the **raw
  string** prefix before parsing (`config.go:176-178`, rationale at `config.go:166-174`).
- **Fail-fast order**: Listen → backend count → per-backend name/URL → uniqueness → algorithm
  (`config.go:96-98`), with the name-uniqueness pass deliberately second so per-backend errors
  take precedence (session log, 2026-09-18).
- Validation goes beyond the ticket's minimum: host:port syntax with uint16 port range
  (`config.go:145-154`), backend-name charset `^[a-zA-Z0-9_-]+$` justified as safe for
  Prometheus labels / log fields / grep (`config.go:41-44`), no query/fragment
  (`config.go:186-191`).

**Concurrency**: none — `Config` is immutable after init in Sprint 1 (contracts ownership table,
`docs/design/sprint-1-contracts.md:169`).

**Dependencies**: stdlib + `gopkg.in/yaml.v3` only. Leaf.

**Tests**: 6 `Load` cases including unknown-field rejection and an `errors.Is(os.ErrNotExist)`
check (`config_load_test.go:31-112`); 21 `Validate` cases (`config_validate_test.go:41-218`)
including the two uppercase-scheme rejections and `p2c_ewma`-rejected; plus `TestExampleConfig`
(`config_load_test.go:118-122`) which round-trips the shipped `configs/example.yaml` so the
example can never drift from the schema. Coverage 98.2% (measured this audit).

**Not covered that maybe should be**: `Load` on a directory path (OS error path is covered only
for missing file); a YAML doc with duplicate `listen:` keys; a config with a backend URL whose
scheme is fine but which is a valid URL with a userinfo section (`http://u:p@host/`) — accepted
today, and nothing downstream strips it. Minor.

## 2. `internal/backend` — stateful Backend + immutable Registry

**Files**: `backend.go` (60 lines), `registry.go` (64 lines), `doc.go`, two test files.

**Purpose**: own backend identity and the two pieces of mutable per-backend state (health,
active-connection count) behind method-only access, so the internal representation can change
in Sprint 3 without touching any caller.

**Public API**:
- `Backend{Name string; URL *url.URL}` with unexported `healthy atomic.Bool` and
  `active atomic.Int64` (`backend.go:20-26`) and methods `IsHealthy`/`SetHealthy`/`IncActive`/
  `DecActive`/`ActiveConns` (`backend.go:30-59`).
- `Registry` with `NewRegistry(cfgs) (*Registry, error)` (`registry.go:31-43`), `All()`
  (`registry.go:48-50`), `Healthy()` (`registry.go:56-64`).

**Key design points**:
- **Unexported atomic fields** (ADR-0002 decision 5): callers can't touch `healthy`/`active`
  even accidentally — compile-time encapsulation. Sprint 3 can swap `atomic.Bool` for a state
  enum without touching `balancer` or `proxy`.
- **`SetHealthy` is an ADR-0006 amendment**: the freeze originally had a getter only; the
  health-transition tests (S1.T8) needed a mutator, and ADR-0006 made it the *permanent*
  contract Sprint 3's health-checker goroutines will call. It lives on `Backend`, not
  `Registry`, to avoid a per-tick name lookup (ADR-0006 decision 3).
- **Registry is an ordered slice, not a map** — order is what `LeastConnections`' deterministic
  tie-break depends on; nothing through Sprint 3 needs O(1) name lookup (`registry.go:27-30`).
- **Fresh snapshots**: `All()` returns `slices.Clone`, `Healthy()` builds a new slice
  (`registry.go:49, 56-64`) — callers can mutate the returned slice freely.
- **All backends start healthy** (`registry.go:39`) — documented: no health checker exists yet,
  and Sprint 1's exit criteria require all backends reachable from the start.
- `NewRegistry` re-parses URLs instead of trusting config (`registry.go:34-37`) because the
  `(Registry, error)` contract must be honored even if a caller skips `Validate`
  (session log, 2026-09-18).

**Concurrency model**: exactly the frozen ownership table — `healthy` written by (future) health
checker / read by selectors+proxy via `atomic.Bool`; `active` inc/dec by proxy / read by
`LeastConnections` via `atomic.Int64`; the registry slice itself is immutable after construction.
All documented at `backend.go:8-19`.

**Dependencies**: imports `internal/config` (for `BackendConfig`) — exactly the frozen direction.
Does NOT import `balancer` (the frozen anti-cycle rule, contracts:38-41).

**Tests**: zero-value-not-healthy, inc/dec arithmetic, 100-goroutine concurrent inc then
interleaved inc/dec netting out, concurrent health toggling (`backend_test.go:11-81`);
construction order/URL preservation, starts-healthy, invalid URL, `All` includes unhealthy
(table-driven), `Healthy` filtering (table-driven), snapshot freshness (mutate returned slice,
registry unaffected — `registry_test.go:157-172`), and a concurrent
`IncActive/IsHealthy/SetHealthy/DecActive` sweep asserting final state (`registry_test.go:174-198`).
Coverage: **100.0%** (measured this audit).

**Not covered**: nothing material for Sprint 1 scope. The 100%-coverage claim held up.

## 3. `internal/balancer` — the Selector seam and five implementations

**Files**: `selector.go`, `roundrobin.go`, `leastconn.go`, `ring.go`, `hashkey.go`,
`naive_consistent_hash.go`, `consistent_hash.go`, `p2c_ewma.go` (stub), `doc.go`, and nine
test files.

**Purpose**: own "which backend for this request" behind one interface, so algorithms are
pluggable and the proxy never changes when one is added.

**Public API**:
- `Selector` interface: `Select(ctx context.Context, r *http.Request) (*backend.Backend, error)`
  (`selector.go:19-21`) — byte-identical to ADR-0002's frozen signature.
- `ErrNoHealthyBackends` (`selector.go:27`) — the project's **only** exported sentinel; the
  proxy branches on it for 503-vs-502 (contracts:110-117).
- Factory `NewFromConfig(cfg, reg) (Selector, error)` (`selector.go:42-52`) — maps config string
  → type; rejects unknown/empty values rather than defaulting, because an empty algorithm here
  means validation was skipped (`selector.go:36-41`).
- Exported selectors: `RoundRobin`, `LeastConnections`, `ConsistentHashBoundedLoads`
  (plus `NewPowerOfTwoChoicesEWMA`, currently a panic stub, `p2c_ewma.go:18-25`).

**Why the interface lives here, not in `proxy`** (ADR-0002 decision 2): four implementations live
in `balancer` and need the contract where they're defined; `proxy` only stores and calls it and
has no methods of its own to hide behind an interface — consumer-side placement would just
invert an import for zero isolation.

### RoundRobin (`roundrobin.go`)
Snapshot `Healthy()`, `ErrNoHealthyBackends` if empty, else `healthy[(counter.Add(1)-1) % len]`
(`roundrobin.go:36-43`). **Atomic counter, no mutex** — a single increment needs no mutual
exclusion; racing increments each get a distinct turn; no deadlock possible
(`roundrobin.go:14-19`, contracts:168). Because `Healthy()` is a fresh snapshot per call, modulo
the *current* length is correct even as the healthy set changes size between calls
(`roundrobin.go:30-35`).

### LeastConnections (`leastconn.go`)
Linear scan of `Healthy()` for the lowest `ActiveConns()` (`leastconn.go:29-44`). Two frozen
properties: **reads never mutate** (bookkeeping is the proxy's job — separation of concerns),
and **ties break to first-in-registry-order** via strictly-lower replacement
(`leastconn.go:25-27`) — deterministic, so tests are reproducible (AGENTS.md key decision #10).

### The ring primitive (`ring.go`, ADR-0008)
Unexported, placement-only: no `Select`, no health, no capacity awareness (`ring.go:27-37`).
- **Hash pipeline**: stdlib FNV-1a-64 → `fmix64` Murmur3 finalizer (`ring.go:105-121`). The
  finalizer is load-bearing, not decoration: measured, raw FNV maps a /24 subnet's 256 addresses
  onto 3 of 4 backends (90/0/100/66); with `fmix64` all four are hit (82/59/54/61)
  (ADR-0008 evidence tables).
- **Vnode keys `<index>:<name>`**, 150 per backend (`ring.go:18, 46-61`). Index-first is the
  design-record choice — measured pre-finalizer at 23.0–26.8% spread vs `name:index`'s
  6.1–36.2% — with the honest caveat recorded that post-finalizer both orders are near-uniform
  and the ordering is defense-in-depth, not load-bearing (ADR-0008 "Post-finalizer caveat").
- **Candidate walk as `iter.Seq[*backend.Backend]`** (`ring.go:71-98`): binary-search the key's
  position, wrap around, yield each distinct backend once. An iterator, not a predicate
  callback, so each selector's skip condition stays inline and visible in its own `Select`
  (ADR-0008 decision 5).
- Immutable after construction; safe to share across goroutines — substantiated by
  `TestRingConcurrentCandidates` (`ring_test.go:219-239`), not just asserted in a comment.

### Hash key (`hashkey.go`)
`requestHashKey(r)` = `RemoteAddr` with the port stripped (`hashkey.go:26-32`), so one client
sticks to one backend across ephemeral source ports. Unparseable/empty addresses pass through
unchanged — documented as intentional (concentrates upstream bugs on one backend so they're
visible in one place's logs) with the known limitation that behind another proxy this is the
proxy's IP (ADR-0009 decision 7, `hashkey.go:14-25`).

### `naiveConsistentHash` (`naive_consistent_hash.go`) — the unwired comparator
Health-aware, load-blind: first healthy candidate from the walk, else `ErrNoHealthyBackends`
(`naive_consistent_hash.go:48-55`). Deliberately unreachable from config — not in the factory
switch, not in `implementedAlgorithms`, unexported — because its only job is to be the real
comparator the bounded-loads decision is measured against, "so that decision is measured against
a working naive implementation rather than argued from a citation"
(`naive_consistent_hash.go:17-29`). Its dead-code elimination from the shipped binary is a
**checked invariant**, not a remembered fact: `cmd/l7LoadBalancer/dce_test.go:25-49` builds a
pinned linux/amd64 binary, asserts `RoundRobin` is present (positive control) and
`naiveConsistentHash` absent.

### `ConsistentHashBoundedLoads` (`consistent_hash.go`, ADR-0009) — the operator-facing selector
Per `Select`: snapshot `Healthy()` (error if empty), compute
`capacity = max(1, ceil(avg_active_over_healthy × 1.25))` (`consistent_hash.go:109-120`),
walk the ring, admit the first candidate where `IsHealthy() && ActiveConns() <= capacity`
(`consistent_hash.go:78-84`, predicate shared with the reproducer at `consistent_hash.go:101-103`).
- **ε = 0.25 is a Go constant** (`consistent_hash.go:16`), documented as the paper's value and
  deliberately not config surface (ADR-0009 decision 1).
- **The `max(1, ...)` floor** exists because an idle system would compute cap 0 and reject
  every first request (`consistent_hash.go:106-108`); proven by
  `TestConsistentHashBoundedLoadsIdleSystemAdmits`.
- **`<=` admission** means a backend can land exactly one over the computed cap — pinned by
  `TestConsistentHashBoundedLoadsCapacityBoundary` (2 admitted at cap 2, 3 skipped).
- **Exhaustion fallback** returns the least-loaded healthy candidate seen — proven unreachable
  with a non-empty healthy set (the mean is itself within cap, so the least-loaded backend is
  always admissible), kept as a defensive branch (`consistent_hash.go:87-94`).
- **`ErrNoHealthyBackends` is the only error** — no distinct "over capacity" error, keeping
  the one-sentinel contract intact (ADR-0009 decision 6).

**Concurrency**: `RoundRobin` owns one `atomic.Uint64`; `LeastConnections` and both CH
selectors hold no mutable state at all (only immutable registry pointer/ring; all `Backend`
reads go through atomics).

**Dependencies**: `backend` + `config` — matches the frozen graph exactly.

**Tests** (the strongest package): per-selector cyclic/min/tie/empty/health-transition/
concurrency tests; ring-level stable-mapping/minimal-disruption (~1/n remap: 20.75% adding a
5th, 27.05% removing one)/±10%-uniformity/distinct-once-walk/empty-ring tests
(`ring_test.go:64-239`); hash-key table test incl. IPv6 brackets (`hashkey_test.go:16-36`);
naive-comparator affinity/port-strip/health/teeth/concurrency tests; bounded-loads over-capacity
skip, boundary, floor, affinity, health, no-mutation, concurrent-with-load-churn, and a
key-matters teeth check; factory mapping + rejection tests; the fixed-seed hot-key comparative
test (`hotkey_test.go:156-183`, seed 2253: naive busiest 4,005 vs bounded 3,126 of 10,000,
asserted in [3800,4200]/[3100,3130]) and the `offline`-tagged 60-seed reproducer
(`hotkey_reproducer_test.go`). Coverage: **94.3%** (measured this audit; the residue is the
`p2c_ewma` panic stub).

**Evidence verification (this audit)**: I ran
`go test -tags offline -run TestHotKeySeedDistribution -v ./internal/balancer/` fresh. Output
matches ADR-0009's published table **exactly**: naive min 29.9% / p10 34.0% / median 40.0% /
p90 47.8% / max 51.3% / mean 40.4%; bounded 30.0–31.3%; **fallback needed 0 of 600,000
selections**. The ADR's evidence is real and reproducible.

**Not covered**: no proxy-level (end-to-end HTTP) test of consistent-hash affinity — all CH
testing is at the selector seam. Acceptable given the proxy is algorithm-agnostic, but see
findings. Also nothing tests `Select` honoring `ctx` cancellation — no implementation consults
it (fine today; will matter in Sprint 4).

## 4. `internal/proxy` — the request lifecycle

**Files**: `proxy.go` (189 lines), `doc.go`, `proxy_test.go`.

**Purpose**: wrap `httputil.ReverseProxy`, own selection-time short-circuits, and own
active-connection accounting so `LeastConnections`' view of load is never corrupted.

**Public API**: `New(reg *backend.Registry, sel balancer.Selector) *Proxy` (`proxy.go:67-79`)
— returns the concrete type from the frozen stub, which satisfies `http.Handler` via a
compile-time assertion (`proxy.go:189`). `ServeHTTP` (`proxy.go:89-110`).

**The lifecycle in one paragraph** (full trace in Part II): `ServeHTTP` selects, short-circuits
503/502 *before* touching `ReverseProxy`, `IncActive`s, attaches a per-request `reqState` to the
context; `Director` rewrites only scheme/host; `ModifyResponse` records the status and wraps the
body; the wrapper's `Close()` — or `ErrorHandler` on transport failure — calls
`reqState.release()`, `sync.Once`-guarded, so `DecActive` fires **exactly once**; a deferred
`logRequest` emits one canonical "request complete" line on every path.

**Why it is built this way** (all ADR-0007):
- **Select in `ServeHTTP`, not `Director`** — `Director`'s signature has no `ResponseWriter`,
  so the 503 short-circuit is impossible there (`proxy.go:81-88`).
- **`sync.Once` release** — both the body wrapper and `ErrorHandler` are mandated triggers;
  unconditional decrements from both would double-decrement and drive `ActiveConns` negative,
  corrupting `LeastConnections` (ADR-0007 hazard 1; `proxy.go:22-25`).
- **Decrement on body `Close()`, not in `ModifyResponse`** — a streamed body means "request
  done" must be "client finished consuming or abandoned the body" (`proxy.go:123-127`).
- **No `ResponseWriter` wrapper** — status comes from `resp.StatusCode`, preserving
  `ReverseProxy`'s `ResponseController` flush/hijack unwrapping (ADR-0007 decision 4).
- **Logger is `slog.Default()` captured in `New`** — the frozen signature takes no logger;
  `main` sets the default (`proxy.go:62-66`).

**Concurrency**: per-request state only; `rp` built once and safe for concurrent use
(`proxy.go:52-54`). The `reqState.status` field is written by `ModifyResponse`/`ErrorHandler`
and read by the deferred `logRequest` — all on the one request goroutine, an implicit safety
argument ADR-0007 documents rather than locks (ADR-0007 consequences, "Negative" #2).

**Dependencies**: `backend` + `balancer` — matches the frozen graph.

**Tests** (`proxy_test.go`): distribution matches RoundRobin's exact a,b,c cycle through a real
front server; 503 for empty-registry and all-unhealthy (table); 502 for a non-sentinel selector
error (stub selector); dead backend → 502 **and** `ActiveConns` back to 0; **100 concurrent
in-flight requests** asserting `ActiveConns == 100` while blocked and draining to 0 after
release (`proxy_test.go:182-227` — the S1.T6 acceptance test, for real); and log-shape tests
asserting exactly one "request complete" line with all six canonical fields on success (INFO),
503 (WARN), and 502 (WARN + a separate WARN "backend round-trip failed" cause line). Coverage:
**96.1%** (measured this audit).

**Not covered**: client disconnect mid-stream / the `http.ErrAbortHandler` path (deferred to
Sprint 4's connection-lifecycle work); streaming/flush/hijack passthrough; consistent-hash
end-to-end. See findings #6/#8.

## 5. `internal/logger` — one function, one frozen vocabulary

`New(level) *slog.Logger` — JSON to stdout (`logger.go:9-11`). The real deliverable is the
frozen canonical field vocabulary in `doc.go:5-11`: `backend`, `method`, `status`,
`latency_ms`, `remote_addr`, `path` — frozen before request-path code existed so it never had
to be grep-renamed (ADR-0002 decision 4). No tests (0% coverage; it is a one-line constructor).
Leaf, no internal deps.

## 6. `cmd/l7LoadBalancer` — thin wiring + a DCE guard

**`main.go`** (92 lines): load → validate → registry → factory → proxy → `http.Server`, with
`log.Error` + `os.Exit(1)` on every failure and no silent fallback (`main.go:37-57`). Listen
address comes from `cfg.Listen`, not a flag — the frozen YAML schema owns it and `Validate`
checks it (`main.go:27-29`). Graceful shutdown on SIGINT/SIGTERM with a 10s-bounded
`srv.Shutdown` (`main.go:72-91`). `ReadHeaderTimeout: 5s` is set (`main.go:69`) — a small piece
of slow-loris hardening arriving early.

**`dce_test.go`**: the binary-level guard proving `naiveConsistentHash` never ships
(`dce_test.go:25-49`), with a `RoundRobin` positive control so the negative assertion can't be
vacuous, and a toolchain-brittleness caveat documented inline.

**Imports**: `config`, `backend`, `balancer`, `proxy`, `logger` — exactly the frozen list
(contracts:35-36). `main` imports nothing above its station.

## 7. Stubs: `internal/metrics`, `internal/health`, `internal/circuit`

Doc-only files. `metrics/doc.go:5-19` freezes the Sprint 3 reservations (`lb_requests_total`,
`lb_request_duration_seconds`, `lb_backend_healthy`, `lb_circuit_state`; labels
`backend`/`method`/`status_class` — explicitly NOT `status_code`, for cardinality). `health.go`,
`circuit.go`, and `p2c_ewma.go` carry one-line "Sprint 3/Sprint 2" notes or panic stubs.
Nothing to audit beyond the reservations, which match the contracts doc verbatim.

---

# Part II — Request lifecycle trace (one GET, round_robin, 3 healthy backends)

Every step cited. This trace was verified against the code line by line; nothing is inferred
beyond documented stdlib behavior (which is itself cited).

1. **Process start (once)**: `config.Load` + `Validate` (`main.go:37-45`) →
   `backend.NewRegistry` stamps all backends healthy (`registry.go:39`) →
   `balancer.NewFromConfig` returns a `RoundRobin` (`selector.go:44-45`) → `proxy.New` builds
   one `httputil.ReverseProxy` with bound `Director`/`ModifyResponse`/`ErrorHandler`
   (`proxy.go:67-79`) → `http.Server{Handler: proxy}` (`main.go:66-70`).
2. **Accept & parse**: `srv.ListenAndServe` runs in a goroutine (`main.go:75-80`); the stdlib
   `http.Server` accepts, and `net/http` parses the request into `*http.Request` — no custom
   code of ours is involved until the handler fires. (`http.Server` internals are stdlib and
   out of the repo's scope; this is the only step I cannot cite to a repo file, and I'm saying
   so explicitly.)
3. **`Proxy.ServeHTTP`** (`proxy.go:89`): `start := time.Now()`, `state := &reqState{}`, and
   `defer p.logRequest(r, state, start)` (`proxy.go:90-92`) — the deferred call is what
   guarantees one log line even on the abort-panic path.
4. **Selection** (`proxy.go:94`): `p.sel.Select(r.Context(), r)` → `RoundRobin.Select`
   (`roundrobin.go:36`) → `reg.Healthy()` fresh snapshot (`roundrobin.go:37` →
   `registry.go:56-64`) → empty would return `ErrNoHealthyBackends`; otherwise
   `healthy[(counter.Add(1)-1) % len]` (`roundrobin.go:41-42`). *(For `consistent_hash`, this
   step is `ConsistentHashBoundedLoads.Select` at `consistent_hash.go:67` instead; everything
   downstream is identical.)*
5. **Error short-circuit** (`proxy.go:95-103`): `errors.Is(err, ErrNoHealthyBackends)` → write
   503 and return; any other select error → 502. `ReverseProxy` never runs on these paths.
   (Proxy test: `proxy_test.go:128-168`.)
6. **Increment** (`proxy.go:105-106`): `state.backend = b; b.IncActive()` →
   `active.Add(1)` (`backend.go:46`). This is the only increment, and it happens exactly once
   per admitted request.
7. **Context attach** (`proxy.go:108-109`): `reqState{backend, status, once}` goes into the
   request context under the unexported `reqStateKey` (`proxy.go:41-46`), and the request goes
   to `p.rp.ServeHTTP`.
8. **Rewrite** — `Director` (`proxy.go:114-121`): reads the state back from context and sets
   only `r.URL.Scheme` / `r.URL.Host` from `state.backend.URL`. It never selects and never
   decrements. (ReverseProxy also handles hop-by-hop headers and `X-Forwarded-For` — stdlib,
   the reason the project wraps instead of hand-rolling.)
9. **Dispatch**: ReverseProxy's default `http.Transport` round-trips to the backend. No custom
   `Transport`/`RoundTripper` exists in Sprint 1 — pool tuning is Sprint 4 per
   `docs/architecture.md`'s deviations note.
10. **Backend responds** → **`ModifyResponse`** (`proxy.go:128-136`): records
    `state.status = resp.StatusCode` (`:133`) and wraps `resp.Body` in `releaseBody{...,
    release: state.release}` (`:134`). It does **not** decrement here — the frozen contract and
    ADR-0007 decision 3.
11. **Stream to client**: ReverseProxy copies the (wrapped) body to the client. While the body
    streams, the backend's `ActiveConns` still includes this request — by design, so
    `LeastConnections` sees real in-flight pressure.
12. **Decrement** — `releaseBody.Close()` (`proxy.go:160-163`) calls `b.release()` →
    `s.once.Do(func() { s.backend.DecActive() })` (`proxy.go:33-37`) → `active.Add(-1)`
    (`backend.go:53`). Triggered by the client's transport finishing the body read
    (`proxy_test.go:209-210` exercises exactly this). **Exactly-once is enforced by
    `sync.Once`**, not by trigger ordering.
13. **Backend failure instead** — **`ErrorHandler`** (`proxy.go:142-151`): records status 502,
    calls `state.release()` (`:146`) — the second mandated trigger, safe because of the
    `Once` — logs the transport error at WARN with its cause (`:149`, the ADR-0007-authorized
    `err` field), and writes 502 (`:150`). Verified by `proxy_test.go:170-180` (502 +
    `ActiveConns` back to 0) and `:330-351` (log shape).
14. **Abort path (client disconnect mid-stream)**: stdlib ReverseProxy panics with
    `http.ErrAbortHandler` after `defer res.Body.Close()` — Go 1.25 source,
    `GOROOT/src/net/http/httputil/reverseproxy.go:535` — and `res.Body` is **our wrapper** at
    that point, so the deferred close runs `release()` as the panic unwinds, before our
    deferred `logRequest`. **No leak on this path — but this rests on stdlib behavior that no
    test here covers** (finding #6). Sprint 4 owns client-cancellation hardening explicitly.
15. **Log**: the deferred `logRequest` (`proxy.go:168-187`) emits one line,
    `"request complete"`, with exactly the six canonical fields; WARN if `status >= 500`,
    else INFO.

**Verdict**: the frozen lifecycle contract (select in `ServeHTTP`, 503/502 short-circuits,
increment-before-dispatch, decrement-in-body-Close, exactly-once, one log line) is implemented
exactly as ADR-0007 specifies. No step was unclear from the code; the only untestable-from-repo
step is stdlib accept/parse (step 2), and the only stdlib-dependent correctness point is the
abort-path body close (step 14), both flagged honestly.

---

# Part III — Concurrency correctness review

## Mutable-state inventory

| Field | Location (file:line) | Writer(s) | Reader(s) | Sync mechanism | Correct? |
|---|---|---|---|---|---|
| `Backend.healthy` | `backend/backend.go:24` | `NewRegistry` (`registry.go:39`); tests; Sprint 3 health checker (ADR-0006 owner) | Selectors via `IsHealthy()` (through `Healthy()`), proxy, `capacityFor` | `atomic.Bool`, method-only (`backend.go:30,39`) | **Yes** |
| `Backend.active` | `backend/backend.go:25` | Proxy: `IncActive` (`proxy.go:106`), `DecActive` via `release` (`proxy.go:34-36`) | `LeastConnections` (`leastconn.go:36-41`), `capacityFor` (`consistent_hash.go:111`), tests, future metrics | `atomic.Int64`, method-only (`backend.go:45,52,58`) | **Yes** |
| `Registry.backends` | `backend/registry.go:18` | `NewRegistry` only | `All()` / `Healthy()` and all selectors | Immutable after init; fresh slices on every read (`registry.go:49,56-64`) | **Yes** |
| `RoundRobin.counter` | `balancer/roundrobin.go:22` | `Select` (`roundrobin.go:41`) | none external | `atomic.Uint64` (`Add` only) | **Yes** |
| `reqState.backend/status/once` | `proxy/proxy.go:26-30` | Request goroutine only (`ServeHTTP`, `modifyResponse`, `errorHandler`) | Same goroutine (deferred `logRequest`) | `sync.Once` for release; single-goroutine discipline for `status` (ADR-0007 documents the implicit argument) | **Yes — documented caveat** |
| `ring.vnodes` | `balancer/ring.go:39` | `newRing` only | `candidates` (`ring.go:71-98`) | Immutable after construction | **Yes** |
| `Proxy.reg` | `proxy/proxy.go:56` | `New` (`proxy.go:69`) | **nothing** | n/a | **Unused field — see finding #2** |
| `config.Config` (loaded) | `cmd/.../main.go:37` | `main` at startup | everything, read-only | Immutable after init (Sprint 4 adds `atomic.Pointer` swap) | **Yes** |

## Checklist answers

- **All atomic access through methods?** Yes. `healthy`/`active` are unexported, so raw-field
  access outside `internal/backend` is a compile error; inside the package every access is via
  `Load/Store/Add` (verified by reading every method body, `backend.go:30-59`, and grepping —
  no `b.healthy`/`b.active` reads anywhere else).
- **Do `All()`/`Healthy()` return fresh slices?** Yes — `slices.Clone` and a fresh filtered
  slice (`registry.go:49, 56-64`), and `TestRegistrySnapshotsAreFresh`
  (`registry_test.go:157-172`) proves mutating the returned slice can't corrupt the registry.
- **Goroutines without shutdown paths?** None. The only production goroutine is
  `srv.ListenAndServe` (`main.go:75-80`), which exits on `srv.Shutdown`/`ErrServerClosed`;
  `signal.NotifyContext` is stopped via `defer stop()` (`main.go:72-73`). All test goroutines
  are `wg.Wait`-joined. (The `deadBackendURL` test listener goroutine ends with its
  `t.Cleanup` listener close.)
- **Data races `-race` would catch?** `go clean -testcache && go test -race ./...` run fresh
  for this audit: **all packages ok, zero races**. Concurrent-exercise tests exist at every
  layer: `backend_test.go:35-81`, `registry_test.go:174-198`, `roundrobin_test.go:107-148`,
  `ring_test.go:219-239`, `naive_consistent_hash_test.go:180-197`,
  `consistent_hash_test.go:232-262`, `proxy_test.go:182-227`.
- One benign, documented TOCTOU: `capacityFor` computes from a snapshot while the proxy
  mutates `ActiveConns` concurrently — inherent to the design, O(backends) with atomic reads,
  accepted in ADR-0009's consequences. Not a defect.

---

# Part IV — Spec-drift audit

Legend: ✅ conforms · ⚠️ drift (see findings) · 📄 documented deviation (recorded, owner-aware)

| Decision | Source | Implementation status | Drift? |
|---|---|---|---|
| `Selector` interface in `balancer`, exact frozen signature | ADR-0002 d2; contracts:43-62 | `selector.go:19-21` byte-identical | ✅ |
| Factory `NewFromConfig` in `balancer`, not `main.go` | ADR-0002 d2; contracts:57-62 | `selector.go:42-52`; `main.go:53` only calls | ✅ |
| snake_case algorithm constants, exactly `round_robin`/`least_conn`/`consistent_hash`/`p2c_ewma` | ADR-0002 d3; contracts:97-104 | `config.go:23-27`, no other spellings anywhere | ✅ |
| `Backend.healthy`/`active` unexported, methods-only (+ `SetHealthy` per ADR-0006) | ADR-0002 d5; ADR-0006 | `backend.go:20-26,30-59` | ✅ |
| `Load` uses `KnownFields(true)`, never `yaml.Unmarshal` | contracts:93-95; S1.T2 | `config.go:78-79` | ✅ |
| `Validate`: empty listen / zero backends / bad URL / hostless / dup names / unknown algorithm; empty algorithm → `round_robin` | S1.T2 acceptance; contracts | `config.go:99-138`, all covered by tests | ✅ (strictly beyond spec: port range, name charset, query/fragment, uppercase scheme — documented in session log) |
| Vocabulary ≠ acceptance; `implementedAlgorithms` gate | ADR-0004 | `config.go:35-39` (3 accepted; `p2c_ewma` rejected) | ✅ |
| `ErrNoHealthyBackends` is the only exported sentinel; proxy branches via `errors.Is` | contracts:106-117 | `selector.go:27`; `proxy.go:97`; all selectors return it or nothing | ✅ |
| `ActiveConns` decrement = body wrapper in `ModifyResponse`, `Close()` decrements exactly once; never in `Director`/`ModifyResponse` directly | contracts:166; S1.T6; ADR-0007 d3 | `proxy.go:128-136, 160-163`, once-guarded `proxy.go:33-37`; `Director` never decrements (`proxy.go:114-121`) | ✅ |
| `ErrorHandler` also releases | ADR-0007 d2 | `proxy.go:146` | ✅ |
| One "request complete" line per request, canonical six fields, every path | S1.T6; ADR-0007 d4 | `proxy.go:92, 168-187`; log-shape tests assert it on success/503/502 | ✅ |
| `err` field on the 502 cause line | canonical vocabulary (contracts:119-122) has no `err` | used at `proxy.go:143` | 📄 ADR-0007 d5 explicitly authorizes it; flagged for owner acceptance in the S1.T6 review — not silent drift |
| Registry returns fresh slices, ordered, immutable Sprint 1 | contracts:167; S1.T3 | `registry.go:48-64`; snapshot-freshness test | ✅ |
| Ring: FNV-1a-64→`fmix64`, `<index>:<name>` vnodes, 150, `iter.Seq` walk, immutable, built from `All()` | ADR-0008 | `ring.go:18, 46-61, 71-98, 105-121`; `newRing(reg.All())` at `consistent_hash.go:50`, `naive_consistent_hash.go:41` | ✅ |
| Hash key = `RemoteAddr` port-stripped; malformed passes through | ADR-0009 d7; ADR-0008 | `hashkey.go:26-32` | ✅ |
| ε = 0.25, constant with documented default (deliberately not config) | ADR-0009 d1 | `consistent_hash.go:16` | ✅ documented default — the task checklist's "configurable or documented default" is met by the documented-default route |
| Capacity `max(1, ceil(avg_over_healthy × 1.25))`, `<=` admission, skip to next on ring | ADR-0009 d3-4; MILESTONES:28 | `consistent_hash.go:67-103, 109-120` | ✅ |
| No "over capacity" error; `ErrNoHealthyBackends` sole error | ADR-0009 d6 | `consistent_hash.go:69-70, 94` | ✅ |
| `naiveConsistentHash` unwired comparator, not named `ConsistentHash` | ADR-0008; S2.T1.2 | absent from factory (`selector.go:43-52`) and `implementedAlgorithms`; DCE-tested (`dce_test.go`) | ✅ |
| `consistent_hash` wired in both `implementedAlgorithms` and factory | ADR-0009 d8 | `config.go:38`; `selector.go:48-49` | ✅ |
| Sprint 1 deliverable "`Director` + custom `Transport` pattern" | MILESTONES.md:10 | not delivered in Sprint 1; Transport reassigned to Sprint 4 *before* S1.T6 | 📄 pre-implementation scope clarification, fully audited in `docs/architecture.md` — but MILESTONES.md:10 itself was never edited (finding #3) |
| Hot-key evidence figures | AGENTS.md key decision #17 vs ADR-0009 vs `hotkey_test.go` | AGENTS.md says naive **3,996**; ADR-0009 (`:101`) and the locked-seed test (`hotkey_test.go:27-29`) say naive **4,005** (seed 2253) | ⚠️ **P1 — finding #1** |
| Proxy field usage | — | `Proxy.reg` stored, never read | ⚠️ P2 — finding #2 |
| `requestHashKey` takes no `context.Context` | AGENTS.md "Contexts" convention | `hashkey.go:26` | 📄 dismissed-with-rationale in the S2.T1.2 review (pure derivation, no I/O) — but the dismissal lives only in the session log, not an ADR (finding #4) |
| Log field vocabulary is request-path-scoped vs `logger/doc.go`'s blanket "all logging MUST" | `internal/logger/doc.go:3-4` vs `main.go:59-64` startup fields | startup lines carry `config`/`listen`/`algorithm`/`backend_count` | 📄 review dismissed as request-path-scoped, but `doc.go`'s wording was never amended to say so (finding #5) |
| Mandatory TDD protocol (tests first, every task) | AGENTS.md | Red-phase confirmations are recorded per task in the session logs (e.g. S1.T6 "Red phase confirmed first", S2.T1.2 "Red-first against undefined symbols"); tests accompany every implementation commit | ✅ on the documented record; commit-level Red-order not independently re-verified by this audit (would need history archaeology), and I'm saying so rather than asserting it |
| Sprint 3 metric/log reservations frozen | contracts:143-159 | `metrics/doc.go` matches verbatim; `status_class` not `status_code` | ✅ |

---

# Part V — What's built vs. marked DONE, and what's next

## Sprint 1 — all 14 `[DONE]` entries verified against the code

| Task | Deliverable per PROGRESS.md | Verified how |
|---|---|---|
| S1.T0 | repo scaffold | exists |
| S1.T0.5 | frozen stubs + contracts doc + ADR-0002 | contracts doc read in full; every frozen signature still matches the code |
| S1.T1 | `go.mod`: yaml + testify; tidy stable | `go.mod` requires both; `tools.go` correctly deleted (ADR-0003 lifecycle) |
| S1.T2 | `Load`/`Validate` + tests + example.yaml | `config.go` + 27 test cases; `TestExampleConfig` round-trip |
| S1.T2-fix | uppercase-scheme rejection, round-trip test, ADR-0004 | `config.go:176-178`; `config_validate_test.go:80-90`; ADR-0004 present |
| S1.T2.6 | ADR-0005 scope doc | present, read |
| S1.T3 | Backend + Registry + tests | `backend.go`, `registry.go`; 100% coverage |
| S1.T4 | RoundRobin | `roundrobin.go`; cyclic/skip/error/±5%-distribution tests |
| S1.T5 | LeastConnections | `leastconn.go`; min/tie/no-mutate/error tests |
| S1.T6 | proxy + ADR-0007 + 100-concurrent drain test | all present (`proxy.go`, `proxy_test.go:182-227`) |
| S1.T7 | main wiring + factory (+ documented `-addr` removal) | `main.go`, `selector.go:42-52`, `factory_test.go` |
| S1.T8 | cross-selector health-transition tests | `selector_test.go:91-129`, incl. immediate-ejection assertion |
| S1.T9 | 3 dummy backends, chaos knobs, compose | files present (`deployments/docker/…`); live smoke documented in session log; **not re-run by this audit** (would need Docker; no drift indicators) |
| S1.T10 | architecture doc + deviations audit | `docs/architecture.md` read in full; two deviations, both documented |

**Sprint 1 exit criteria**: `make run` + curl distribution documented in the S1.T7 smoke;
`make test` and `make test-race` re-verified green by this audit. No DONE task is missing,
partial, or different from what its entry says.

## Sprint 2 (T1–T2) — 3 tickets, all verified

| Task | Deliverable | Verified how |
|---|---|---|
| S2.T1.1 | ring primitive + ADR-0008 | `ring.go`; five ring tests incl. ~1/n disruption and concurrent walk; ADR-0008 present and accurate |
| S2.T1.2 | `naiveConsistentHash` + `requestHashKey` + DCE guard | all present; comparator tests incl. key-matters teeth check; `dce_test.go` passes |
| S2.T2 | `ConsistentHashBoundedLoads` + wiring + ADR-0009 + hot-key evidence | all present; `consistent_hash` accepted by `Validate` and mapped by the factory; **offline reproducer re-run by this audit — ADR-0009's published 60-seed distribution reproduced exactly, fallback fired 0 of 600,000 selections** |

**Sprint 2 exit criteria status** (for honesty): hot-key test ✅ done; "all 4 algorithms
selectable" ❌ 3 of 4 (`p2c_ewma` pending — by design, mid-sprint); P2C load-skew test ❌
pending. Sprint 2 is *not* complete — only its consistent-hash half is.

## What's next (S2.T3 onward)

1. **`PowerOfTwoChoicesEWMA`** (`p2c_ewma.go` stub → real): two random healthy backends, lower
   EWMA latency wins. Required ADR: "why P2C over least-connections under latency skew" —
   decisions to settle: EWMA α, atomic latency representation, <2-healthy-backend behavior
   (AGENTS.md lists all three as ADR-required).
2. **P2C load-skew test** (Sprint 2 exit criterion) — converges load onto faster backends.
3. **Inherited flag**: adding `p2c_ewma` needs the known two edits
   (`config.implementedAlgorithms` + the factory switch) — the duplication was flagged in the
   S1.T7 review and deliberately left; p2c_ewma is when it bites again.
4. Then Sprint 3 (health/circuit/metrics), Sprint 4 (reload, connection lifecycle, transport
   tuning, retry-policy ADR), Sprint 5 (HTTP/2, benchmarks).

---

# Part VI — "Defend this in an interview"

**1. "Walk me through how a request flows through your load balancer."**
The server accepts and parses the request with the stdlib, then `Proxy.ServeHTTP` runs
(`proxy.go:89`). It calls the configured `Selector` itself — not the `Director`, which has no
`ResponseWriter` and couldn't short-circuit a 503 (`proxy.go:94`). If selection returns
`ErrNoHealthyBackends` we write a 503 and never touch `ReverseProxy`; any other select error
gets a 502 (`proxy.go:95-103`). On success we `IncActive` the chosen backend, attach a
per-request `reqState` to the context (`proxy.go:105-108`), and hand off: `Director` rewrites
only scheme/host, the transport round-trips, `ModifyResponse` records the status and wraps the
body so its `Close()` releases the connection slot exactly once via a `sync.Once`
(`proxy.go:33-37, 128-136, 160-163`). A deferred call emits one structured "request complete"
line on every path, including the abort-panic path (`proxy.go:92, 168-187`).

**2. "Why did you pick these load balancing algorithms?"**
Four algorithms, each answering a different failure mode. Round-robin gives O(1) fairness when
backends are homogeneous (`roundrobin.go:36-43`). Least-connections balances by *current work*
rather than turn-taking, reading the live in-flight count the proxy maintains
(`leastconn.go:29-44`). Consistent-hash-with-bounded-loads gives session affinity without the
hot-key pile-up plain consistent hashing has — measured, not asserted: naive concentrates
29.9–51.3% of a skewed workload on one backend across 60 seeds, bounded loads holds every
backend at the capacity bound (ADR-0009, reproducer re-run by this audit). P2C-EWMA (in
progress) adds latency-awareness for skewed-latency fleets — least-connections treats a
50ms-fast backend and a 500ms-slow backend with one connection each as equal; P2C does not.

**3. "How do you track in-flight connections per backend without races?"**
Each `Backend` holds an unexported `atomic.Int64` (`backend.go:25`), incremented by the proxy
before dispatch and decremented exactly once when the request is done, behind method-only
access so no caller can race the raw field. The hard part is "exactly once": two triggers can
fire for one request — the response-body wrapper's `Close()` and `ErrorHandler` on transport
failure — so both funnel through a `sync.Once`-guarded `release()` (`proxy.go:26-37`). The
decrement lives on the *body wrapper*, not in `ModifyResponse`, because a streamed response
isn't "done" until the client finishes consuming it (`proxy.go:123-127`). The invariant is
tested with 100 concurrent blocked requests asserting `ActiveConns == 100` mid-flight and a
drain to zero after release (`proxy_test.go:182-227`), and the whole suite runs under
`-race`, clean.

**4. "Why is the Selector interface in balancer rather than proxy?"**
Because four implementations live in `balancer` and they all need the contract where they're
defined; `proxy` only stores the interface and calls `Select` — it has no methods of its own
that an interface would hide (ADR-0002 decision 2). The usual Go "define interfaces at the
consumer" guidance buys nothing here and would just force `balancer` to import a type from
`proxy` or duplicate the interface. The factory `NewFromConfig` lives in `balancer` for the
same reason: the package that owns the config-string → type mapping should own the mapping
(`selector.go:29-41`).

**5. "Walk me through your consistent hashing and why bounded loads matter."**
The ring places 150 virtual nodes per backend, keyed `<index>:<name>`, hashed with stdlib
FNV-1a-64 followed by a Murmur3-style `fmix64` finalizer (`ring.go:46-61, 105-121`). The
finalizer is load-bearing: I measured raw FNV collapsing a /24 subnet onto 3 of 4 backends
(90/0/100/66 of 256 addresses) and `fmix64` fixing it (ADR-0008). The walk from a key's
position is an `iter.Seq` yielding each backend once, so each selector's skip rule stays inline
(`ring.go:71-98`). The key is the client IP with the port stripped, so affinity survives
ephemeral ports (`hashkey.go:26-32`). Bounded loads matters because affinity alone pins a hot
key to one backend: I cap each candidate at `max(1, ceil(avg_active × 1.25))` and walk to the
next when it's over (`consistent_hash.go:67-103`). The evidence is in-repo: a fixed-seed test
(naive busiest 4,005 vs bounded 3,126 of 10,000) plus a 60-seed offline reproducer showing the
fallback branch fired zero times in 600,000 selections (ADR-0009; I re-ran it this audit).

**6. "What happens if a backend URL in the config is malformed?"**
Load and validation are separate: `Load` strictly deserializes with `KnownFields(true)` so even
a typo'd field name fails at load (`config.go:78-79`), then `Validate` requires each URL to
start with a lowercase `http://`/`https://`, parse, carry a host, and carry no query or
fragment (`config.go:175-193`). The scheme check is against the raw string before `url.Parse`
because `url.Parse` lowercases the scheme and would let `HTTP://` through — a real bug my own
code review caught (S1.T2-fix). Even then, `NewRegistry` re-parses rather than blindly trusting
the struct (`registry.go:34-37`), and `main` exits 1 with a logged cause at every stage — the
process never starts with a config it can't route (`main.go:37-51`).

**7. "What's your concurrency strategy — atomics, mutexes, or channels?"**
Each primitive where it fits, with a documented why. Atomics for single-word state: `atomic.Bool`
health, `atomic.Int64` active connections, `atomic.Uint64` round-robin counter — a single
increment doesn't need mutual exclusion and can't deadlock (`roundrobin.go:14-19`).
Immutability for everything that doesn't change: the registry slice, the hash ring, the
`ReverseProxy` — built once, shared freely. `sync.Once` for the one exactly-once problem
(ADR-0007). Mutexes: none yet, deliberately — the circuit breaker's compound closed→open→
half-open transitions are the first legitimate mutex use, and that decision is explicitly
deferred to the Sprint 3 ADR (contracts:170). Channels for coordination where they appear in
Sprint 4's reload work; the ownership of every field is written down in a table
(`docs/design/sprint-1-contracts.md:161-170`).

**8. "How do you know your load balancer is correct?"**
Three layers. First, property tests, not just happy paths: round-robin's ±5% distribution over
1000 concurrent selects, the ring's ~1/n minimal-disruption and ±10% uniformity, least-conn's
deterministic tie-break, and the bounded-loads capacity boundary (admitted at exactly cap,
skipped one over). Second, the invariants that other code depends on are tested directly:
the 100-concurrent drain-to-zero of `ActiveConns`, the exactly-once release across both
trigger paths, and health-transition ejection proven *on the very next Select* via a mutation
check that temporarily broke `Registry.Healthy()` (S1.T8). Third, evidence tests: the hot-key
comparator runs a real naive selector against the bounded one on the same fixed stream, and an
offline 60-seed reproducer — which I re-ran — confirms the fallback branch is unreachable.
Everything runs under `go test -race`, zero findings; coverage is 100% backend, 94.3%
balancer, 96.1% proxy, 98.2% config. Honest gaps: no end-to-end consistent-hash test through
the proxy, and the client-disconnect-mid-stream path isn't covered until Sprint 4.

---

# Part VII — Recommended reading order

Read in this order; each builds on the previous.

1. `internal/backend/backend.go` — the shape everything else builds on: unexported atomics
   behind methods, and the concurrency comment that states the ownership contract.
2. `internal/backend/registry.go` — fresh-snapshot semantics and order preservation; 60 lines,
   the whole Sprint 4 reload seam in miniature.
3. `internal/balancer/selector.go` — the interface, the one sentinel, and the factory; the
   single seam the entire project is organized around.
4. `internal/balancer/roundrobin.go` then `leastconn.go` — the two simplest selectors; focus
   on *why* atomic-not-mutex and *why* strictly-lower tie-breaking.
5. `internal/proxy/proxy.go` — the heart. Read `ServeHTTP` top to bottom with ADR-0007 open;
   focus on the `reqState` + `sync.Once` release and the body wrapper.
6. `docs/adr/0007-proxy-request-lifecycle-and-exactly-once-decrement.md` — the why for every
   proxy decision, including the ones that look odd (the `err` log field, no `ResponseWriter`
   wrapper).
7. `internal/config/config.go` — strictness discipline: `KnownFields`, raw-string scheme
   check, fail-fast order, vocabulary-vs-acceptance.
8. `cmd/l7LoadBalancer/main.go` — how thin a wiring layer should be, and the exit-1 discipline.
9. `internal/balancer/ring.go` + `docs/adr/0008-…md` — the hash pipeline; focus on the
   `fmix64` evidence (the /24 collapse table) and the honest post-finalizer caveat.
10. `internal/balancer/hashkey.go` then `consistent_hash.go` + `docs/adr/0009-…md` — the key
    derivation, the capacity formula, and the measured hot-key evidence.
11. `internal/balancer/hotkey_test.go` — read the locked-seed test and its assertion windows;
    this is the "measured, not asserted" pattern at its best.
12. `internal/balancer/naive_consistent_hash.go` + `cmd/l7LoadBalancer/dce_test.go` — the
    unwired comparator and the binary-level guard proving it ships nothing.
13. `docs/design/sprint-1-contracts.md` + `docs/architecture.md` — the frozen contracts and the
    as-built map, including the two documented deviations.

---

# Appendix — Findings register (full detail)

**P0: none found.**

**P1 (1)**

1. **`AGENTS.md` key decision #17 cites stale hot-key figures.** AGENTS.md says the checked-in
   test shows "naive 3,996 vs bounded 3,126"; the locked-seed test (`hotkey_test.go:27-29`,
   seed 2253) and ADR-0009 (`:101`) say naive **4,005** vs bounded 3,126. The 3,996 figure is
   the pre-sweep seed-17 number that the S2.T2 code-review follow-up explicitly replaced
   (`docs/sessions/2026-09-19-opencode.md`, S2.T2 follow-up, first bullet) — the AGENTS.md
   decision-list entry was not updated with it. AGENTS.md is the canonical doc every agent and
   every interviewer reads first; a self-contradicting evidence figure between it and
   ADR-0009 is exactly the kind of thing an interview falls apart on. One-line fix.

**P2 (8)**

2. **`Proxy.reg` is stored and never read** (`proxy.go:56`, assigned at `:69`; verified by
   grep — zero reads). No documented rationale for keeping it; the frozen `New(reg, sel)`
   signature *takes* a registry but nothing in the file *uses* it. Either Sprint 3's
   health/circuit wiring will consume it, or it's dead weight. No documented rationale —
   worth asking.
3. **`MILESTONES.md:10` still says "custom `Transport` pattern"** as a Sprint 1 deliverable.
   Fully audited and dispositioned as a pre-implementation scope clarification in
   `docs/architecture.md`, but the MILESTONES.md line itself was never edited ("simply never
   updated to match" — architecture.md). A fresh reader of MILESTONES will look for a custom
   RoundTripper that doesn't exist.
4. **`requestHashKey` takes no `context.Context`** (`hashkey.go:26`) against AGENTS.md's
   "every request-scoped function takes context as first arg". Dismissed with rationale
   (pure derivation, no I/O) in the S2.T1.2 review, but the dismissal lives only in the session
   log — an ADR-bar note or a convention exception in AGENTS.md would make it discoverable.
5. **`logger/doc.go:3-4` says "All logging across this project MUST use these names"**, read
   literally, is violated by `main.go`'s startup line (`config`/`listen`/`algorithm`/
   `backend_count`, `main.go:59-64`) and by the ADR-0007-authorized `err` field
   (`proxy.go:143`). The reviews consistently scoped the six-field vocabulary to the request
   path, but the doc's blanket wording was never amended to say that. Wording, not behavior.
6. **The abort-path decrement rests on stdlib behavior no test covers.** On
   `http.ErrAbortHandler`, Go's ReverseProxy runs `defer res.Body.Close()` on the wrapped body
   (`GOROOT/src/net/http/httputil/reverseproxy.go:535` in Go 1.25), so `release()` fires — but
   nothing in this repo pins that with a client-disconnect test, and ADR-0007 discusses the
   abort path only for the log line, not the decrement. Sprint 4 owns client-cancellation
   hardening; until then this is a documented-in-source-behavior, not a documented-in-project
   guarantee. Worth a test when Sprint 4 starts.
7. **No end-to-end (proxy-level) test of `consistent_hash`** — its exhaustive testing is at
   the selector seam; the proxy's distribution test covers RoundRobin only
   (`proxy_test.go:96-126`). Justified by the interface seam (the proxy is algorithm-agnostic)
   and by the S1.T7 manual smoke, but an automated affinity-through-the-full-proxy test would
   close the loop.
8. **The accepted-algorithm set is encoded twice** (`config.implementedAlgorithms`,
   `config.go:35-39`, and the factory switch, `selector.go:43-52`) — flagged in the S1.T7
   review, deliberately left, and `consistent_hash` duly needed both edits. `p2c_ewma` will
   need them again. Known, documented, accepted; listed so it isn't rediscovered.
9. **`logger` has no test file** (0% coverage on a one-line constructor). Trivial, but the
   project's own bar is tests-with-every-task; `logger.New` predates the enforcement habit
   (S1.T0.5 stub, deemed trivial). Cosmetic.

**Race conditions found by `go test -race ./...`: 0.** `go vet ./...`: clean. `gofmt -l cmd
internal`: clean. ADR-0009's 60-seed reproducer re-run: matches published figures exactly,
fallback 0/600,000.
