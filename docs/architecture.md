# Architecture

_This document describes the as-built architecture, sprint by sprint. See
`MILESTONES.md` for the plan, `PROGRESS.md` for live task state, and
`docs/design/sprint-1-contracts.md` for the frozen Sprint 1 contracts.
Sprint 1 is complete: this document is the Sprint 1 reference._

## Overview

l7LoadBalancer is a Layer 7 HTTP load balancer built entirely on Go's
standard library (`net/http`, `net/http/httputil`) for the request path. It
layers a pluggable backend-selection policy (`balancer.Selector`) over
`httputil.ReverseProxy`:

- the **proxy** owns the request lifecycle and active-connection accounting,
- the **selector** owns "which backend",
- the **registry** owns backend identity and shared mutable state,
- **config** owns strict YAML loading and validation.

`httputil.ReverseProxy` is used rather than a hand-rolled proxy because it
already handles hop-by-hop header stripping, `X-Forwarded-For`, buffering,
and flushing. The interesting work is the selection layer and the
concurrency/resilience/lifecycle code wrapped around it. Resilience (health
checking, circuit breaking) and observability (Prometheus metrics) arrive in
Sprint 3; zero-downtime reload and connection-lifecycle hardening arrive in
Sprint 4. All non-trivial decisions are tracked as ADRs — see the
[decision index](#decision-index).

Scope claims are deliberately bounded; "production-grade" here means the
patterns are demonstrated and defensible, not that the project is hardened
for adversarial traffic. See [ADR-0005](adr/0005-scope-of-production-grade.md).

## Request path

The following is the Sprint 1 request lifecycle as built. It is the same
path for every algorithm; only the `selector.Select` call differs.

```
Client
  │
  ▼
http.Server  (listen address from cfg.Listen)
  │
  ▼
proxy.Proxy.ServeHTTP                              internal/proxy/proxy.go
  │
  ├─ selector.Select(ctx, r) ──────────────► balancer.RoundRobin
  │                                            or balancer.LeastConnections
  │                                               │ snapshots
  │                                               ▼
  │                                          backend.Registry.Healthy()
  │
  ├─ ErrNoHealthyBackends ─────────────────► 503 short-circuit
  │                                            (ReverseProxy never runs)
  ├─ other select error ───────────────────► 502
  │
  ├─ backend.IncActive()
  ├─ attach reqState{backend, status, once} to request context
  ▼
httputil.ReverseProxy.ServeHTTP
  │
  ├─ Director            reads state from context; sets req.URL.Scheme/Host
  │                      (scheme/host only — see ADR-0007)
  ├─ Transport           stdlib default; dispatches to the backend
  │
  ├─ ModifyResponse      records resp.StatusCode;
  │                      wraps resp.Body in releaseBody
  │                        └─ releaseBody.Close()
  │                             └─ reqState.release()   (sync.Once)
  │                                  └─ backend.DecActive()
  │
  ├─ ErrorHandler        records 502, calls reqState.release(),
  │                      logs the transport error at WARN, writes 502
  │
  └─ deferred logRequest emits one "request complete" slog line
                         (canonical fields) on every path
  ▼
Client
```

Lifecycle notes (full rationale in
[ADR-0007](adr/0007-proxy-request-lifecycle-and-exactly-once-decrement.md)):

1. `ServeHTTP` calls `selector.Select` itself, **not** `Director` — `Director`
   has no `ResponseWriter` and cannot short-circuit a 503. On success it calls
   `IncActive()` and carries a per-request `reqState` in the request context.
2. `DecActive` runs through `reqState.release()`, guarded by `sync.Once`, so it
   fires **exactly once** whether the response body's `Close()` or
   `ErrorHandler` reaches it first. `LeastConnections` depends on this
   invariant: a leak or double-decrement corrupts its view of load.
3. The decrement lives on the response-body wrapper, not in `ModifyResponse`
   directly — a response may be streamed, and "request done" must mean the
   client has finished consuming (or abandoned) the body.
4. Status is captured from `resp.StatusCode` rather than wrapping the
   `ResponseWriter`, preserving `ReverseProxy`'s `ResponseController` flushing
   and hijacking behavior.
5. Every request emits exactly one `msg:"request complete"` line with the
   canonical field vocabulary (`backend`, `method`, `status`, `latency_ms`,
   `remote_addr`, `path`) on the success, 503, 502, and abort paths. The 502
   path additionally logs the transport cause at WARN (the vocabulary has no
   `err` field).

## Component map

Package dependency graph (acyclic — no cycles allowed):

```
cmd/l7LoadBalancer
      │
      ▼
   internal/proxy ──────┐
      │                 │
      ▼                 ▼
internal/balancer → internal/backend
      │                 │
      └───────┬─────────┘
              ▼
        internal/config

internal/logger   — leaf, no internal deps
internal/metrics  — leaf, no internal deps (Sprint 3)
internal/health   — depends on backend (Sprint 3)
internal/circuit  — depends on backend (Sprint 3)
```

As-built package status:

| Package | Status | Responsibility |
|---|---|---|
| `cmd/l7LoadBalancer` | Sprint 1 | Thin wiring layer: `config.Load`/`Validate` → `backend.NewRegistry` → `balancer.NewFromConfig` → `proxy.New` → `http.Server`; SIGINT/SIGTERM graceful shutdown. Fatal + exit 1 on any startup failure (no silent fallback). |
| `internal/proxy` | Sprint 1 | Wraps `httputil.ReverseProxy`; owns the request lifecycle, 503/502 short-circuits, active-connection accounting, and the per-request log line. |
| `internal/balancer` | Sprint 1 | `Selector` interface + `ErrNoHealthyBackends` (the only exported sentinel), `RoundRobin` and `LeastConnections`, and the `NewFromConfig` factory. Sprint 2 adds `ConsistentHashBoundedLoads` and `PowerOfTwoChoicesEWMA` here. |
| `internal/backend` | Sprint 1 | `Backend` (identity + unexported `atomic` health/active state, methods-only access) and `Registry` (ordered, immutable in Sprint 1). |
| `internal/config` | Sprint 1 | Strict YAML loading (`KnownFields(true)`) and fail-fast validation; algorithm identifier constants. Immutable after init in Sprint 1. |
| `internal/logger` | Sprint 1 | `log/slog` JSON setup and the frozen canonical field vocabulary. Leaf. |
| `internal/metrics` | Sprint 3 (stub) | Prometheus instruments. Names/labels reserved in `internal/metrics/doc.go`. |
| `internal/health` | Sprint 3 (stub) | Active probes + passive outlier detection. |
| `internal/circuit` | Sprint 3 (stub) | Per-backend circuit breaker state machine. |

**Dependency rule**: `internal/backend` does **not** import `internal/balancer`.
A `Backend` has no notion of how it is selected; adding that import is a sign
the abstraction is leaking.

The full concurrency-ownership table (which field is written by whom, under
which sync primitive) lives in
[`docs/design/sprint-1-contracts.md`](design/sprint-1-contracts.md#concurrency-ownership-table).

### Where new selection algorithms plug in

- The seam is `balancer.Selector` (`internal/balancer/selector.go`):
  `Select(ctx context.Context, r *http.Request) (*backend.Backend, error)`.
  It is defined in `balancer`, not `proxy`, because all implementations live
  there — see [ADR-0002](adr/0002-interface-placement-and-schema-freeze.md).
- `balancer.NewFromConfig(cfg, reg)` maps a config algorithm string to a
  selector type; `main.go` only calls it.
- The config-string → constant → selector-type mapping is the **algorithm
  identifier table** in
  [`docs/design/sprint-1-contracts.md`](design/sprint-1-contracts.md#algorithm-identifier-table).
  `config.Validate` accepts only the implemented set (Sprint 1:
  `round_robin`, `least_conn`) — see
  [ADR-0004](adr/0004-reject-unimplemented-algorithms-in-validate.md).
- Every selector carries a compile-time assertion
  `var _ Selector = (*X)(nil)`.

## Decision index

All non-trivial decisions are recorded in `docs/adr/`. Accepted:

| ADR | Title | Status |
|-----|-------|--------|
| [0001](adr/0001-record-adrs.md) | Record architecture decisions | Accepted |
| [0002](adr/0002-interface-placement-and-schema-freeze.md) | Interface placement and Sprint 1 schema freeze | Accepted |
| [0003](adr/0003-pin-unused-deps-with-tools-go.md) | Pin not-yet-imported dependencies with a build-tagged tools.go | Accepted |
| [0004](adr/0004-reject-unimplemented-algorithms-in-validate.md) | Reject unimplemented algorithms in Validate | Accepted |
| [0005](adr/0005-scope-of-production-grade.md) | Scope of "production-grade" | Accepted |
| [0006](adr/0006-backend-sethealthy-amends-adr-0002.md) | Add Backend.SetHealthy, amending ADR-0002 decision 5 | Accepted |
| [0007](adr/0007-proxy-request-lifecycle-and-exactly-once-decrement.md) | Proxy request lifecycle and exactly-once active-connection decrement | Accepted |

Tracked but not yet written (each decides in the sprint that delivers the
feature):

| Sprint | Decision |
|--------|----------|
| 2 | Why bounded-loads consistent hashing over naive CH |
| 2 | Why P2C-EWMA over least-connections for latency-skewed workloads |
| 3 | Circuit breaker concurrency model |
| 4 | Reload architecture: atomic pointer swap vs SO_REUSEPORT |
| 4 | Deployment target decision (deferred from Sprint 1 per ADR-0005) |
| 4 | Retry policy (or deliberate absence) |

## Deviations from plan

Two items differ between the frozen plan and the as-built state. Neither
required a new ADR, and the reasoning for that is recorded with each.

### 1. S1.T7 removed the scaffold's `-addr` CLI flag — owner signed off

The initial scaffold exposed a `-addr` flag for the listen address. S1.T7
removed it: `cfg.Listen`, part of the frozen YAML schema and checked by
`config.Validate`, is the single validated source of truth, so a second,
unvalidated address source was removed rather than reconciled.

This was flagged during the S1.T7 code review as needing explicit owner
sign-off (`docs/sessions/2026-09-18-opencode.md`, "S1.T7 — Code-review
follow-up" → *Flagged for owner awareness*). It is recorded here as
**signed off by the project owner during the S1.T10 retro**. No ADR: the
change was evaluated against the project's three-part ADR bar (hard to
reverse / surprising without context / real trade-off) and, while it is
arguably surprising and is a real trade-off, it is trivially reversible — a
contained one-flag change with no ripples — so it fails the bar on the
reversibility leg. Original implementation reasoning:
`docs/sessions/2026-09-18-opencode.md` (S1.T7 → Decisions).

### 2. `MILESTONES.md`'s "custom `Transport` pattern" wording — pre-implementation scope clarification

`MILESTONES.md:10` lists, as a Sprint 1 deliverable, an
"`httputil.ReverseProxy` wrapper using `Director` + custom `Transport`
pattern". No custom `http.Transport`/`RoundTripper` was ever built, or
intended to be built, in Sprint 1. The Transport work was reassigned to
Sprint 4 **before** S1.T6 was implemented:

- `.scratch/s1-t3-t9-backend-selector-proxy/spec.md:115` states, for the
  proxy phase: "No `http.Transport` tuning (`MaxIdleConnsPerHost`, timeouts,
  etc.) in this phase — stock defaults; connection pool tuning is explicitly
  Sprint 4 per AGENTS.md." See also `:171`.
- `AGENTS.md:291` and `MILESTONES.md:63` both place connection-pool tuning
  (`MaxIdleConnsPerHost`, `IdleConnTimeout`, `DialContext` timeout,
  `ResponseHeaderTimeout`) in Sprint 4.
- The frozen contracts doc never specifies a custom Transport for Sprint 1;
  its proxy contract (`Director` sets only scheme/host) is consistent with
  the stock transport.
- "RoundTripper" appears nowhere in the project's git history.

So this is a **pre-implementation scope clarification**, not a
mid-implementation deviation: `MILESTONES.md`'s original scaffold wording
(unchanged since the initial commit) simply was never edited to match the
scope the frozen contract had already settled. No ADR — it is a wording
mismatch in a planning document, not a design decision.

## Later amendments to Sprint 1 contracts

`docs/design/sprint-1-contracts.md` is **frozen** and is intentionally not
edited. Two ADRs accepted after the freeze amend rows of its
concurrency-ownership table:

- [ADR-0006](adr/0006-backend-sethealthy-amends-adr-0002.md) amends the
  `Backend.healthy` row: `Backend` now exposes `SetHealthy(bool)` in
  addition to `IsHealthy()`. `SetHealthy` is the permanent contract the
  Sprint 3 health checker will call.
- [ADR-0007](adr/0007-proxy-request-lifecycle-and-exactly-once-decrement.md)
  amends the `Backend.active` row / proxy lifecycle: `DecActive` is driven
  through a `sync.Once`-guarded `reqState.release()` so it runs exactly once
  across the body-wrapper and `ErrorHandler` paths.

Read those two ADRs alongside the contracts doc's
[concurrency-ownership table](design/sprint-1-contracts.md#concurrency-ownership-table).

## Deliberately not here yet

A future agent should not assume any of the following exist. Each names its
owning sprint:

- **Consistent-hash-bounded-loads and P2C-EWMA selectors** — Sprint 2.
  (Stubs with `panic("not implemented")` bodies live in
  `internal/balancer/consistent_hash.go` and `p2c_ewma.go`.)
- **Health checking (active + passive)** — Sprint 3.
- **Circuit breaking** — Sprint 3.
- **Prometheus metrics** — Sprint 3.
- **Hot-reload (SIGHUP, `atomic.Pointer[Config]`)** — Sprint 4.
- **Connection-lifecycle hardening** (client cancellation, backend death
  mid-response, slow-loris timeouts) — Sprint 4.
- **Connection-pool tuning** (`http.Transport` settings) — Sprint 4.
- **Retry policy** (or its deliberate absence) — Sprint 4.
- **Deployment-target decision** (bare binary vs. Docker vs. Kubernetes) —
  Sprint 4, per [ADR-0005](adr/0005-scope-of-production-grade.md).
- **HTTP/2 (client-facing and to backends)** — Sprint 5.
- **The benchmark rig and published numbers** — Sprint 5.
