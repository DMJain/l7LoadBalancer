# ADR Index

One-line decision summaries. Read the full ADR when your task touches that subsystem.

| # | Title | Status | Key decision |
|---|---|---|---|
| [0001](0001-record-adrs.md) | Record architecture decisions | Accepted | Every non-trivial decision gets an ADR in `docs/adr/`, numbered sequentially. |
| [0002](0002-interface-placement-and-schema-freeze.md) | Interface placement and Sprint 1 schema freeze | Accepted | Selector interface lives in `balancer`; Backend fields are unexported with method-only access. |
| [0003](0003-pin-unused-deps-with-tools-go.md) | Pin not-yet-imported dependencies with a build-tagged tools.go | Superseded (tools.go deleted) | Blank-import unused deps under `//go:build tools` until a real task imports them. |
| [0004](0004-reject-unimplemented-algorithms-in-validate.md) | Reject unimplemented algorithms in Validate | Accepted | `Validate` accepts only algorithms in `implementedAlgorithms`; unimplemented ones are rejected. |
| [0005](0005-scope-of-production-grade.md) | Scope of "production-grade" | Accepted | Demonstrates production LB patterns with defensible decisions; explicitly excludes security hardening and ops. |
| [0006](0006-backend-sethealthy-amends-adr-0002.md) | Add Backend.SetHealthy, amending ADR-0002 decision 5 | Superseded by ADR-0011 d2 | Added `SetHealthy(bool)` to Backend; later replaced by `MarkHealthy()`/`MarkUnhealthy()`. |
| [0007](0007-proxy-request-lifecycle-and-exactly-once-decrement.md) | Proxy request lifecycle and exactly-once active-connection decrement | Accepted | Proxy uses `reqState` + `sync.Once` for exactly-once active-connection decrement via body-wrapper Close. |
| [0008](0008-consistent-hash-ring-pipeline-and-vnode-layout.md) | Consistent-hash ring hash pipeline, vnode key order, and vnode count | Accepted | Hash pipeline is FNV-1a-64 → `fmix64`; 150 vnodes per backend; `index:name` vnode keys. |
| [0009](0009-consistent-hash-bounded-loads-capacity-and-evidence.md) | Consistent-hash bounded loads: epsilon, load metric, capacity formula, and probing/fallback | Accepted | ε = 0.25 constant; load = `ActiveConns()` over healthy; capacity `max(1, ceil(avg × 1.25))`. |
| [0010](0010-p2c-ewma-backend-latency-state-cold-start-and-failure-penalty.md) | P2C-EWMA: Backend-owned latency state, cold-start semantics, and the failure penalty | Accepted | Backend owns EWMA latency via `atomic.Int64`; cold-start sets directly; failures record fixed 2s penalty. |
| [0011](0011-health-passive-outlier-and-circuit-breaker-composition.md) | Health, passive-outlier, and circuit-breaker composition | Accepted | Selection eligibility is `healthy AND circuit-not-open`; circuit never writes `Backend.healthy`. |
| [0012](0012-circuit-gate-interface-and-backend-state-methods.md) | Circuit breaker gate interface, Registry-mediated admission, and Backend state API | Accepted | `CircuitGate` is consumer-defined in `backend`; Registry mediates admission; state is one CAS-able snapshot. |
| [0013](0013-observability-metrics-logging-and-integration.md) | Observability: metrics collector, transition logging, and their integration | Accepted | `metrics` is a push-only leaf `Collector`; transitions are edge-triggered and shared by log and gauge. |
| [0014](0014-health-endpoint-contract-and-probe-semantics.md) | Health endpoint contract and probe semantics | Accepted | Separate always-on listener serving `/livez`, `/readyz`, `/startupz` with pinned JSON envelope. |
| [0015](0015-reload-architecture.md) | Zero-downtime reload architecture: in-process snapshot swap, backend identity, and admission | Accepted | Reload swaps an immutable versioned snapshot in process; identity is `(name, URL)`; only backends are reloadable. |
| [0016](0016-drain-lifecycle.md) | Drain lifecycle: retired-context join, two-phase drain, and cancel-at-window | Accepted | Per-backend retired context joined via `context.AfterFunc`; drain window is config; cut-off signal is structural. |

## Decisions without a dedicated ADR

These decisions are recorded here because they have no standalone ADR document.

- **`ErrNoHealthyBackends` is the only branchable sentinel:** `proxy` branches on `errors.Is` to decide 503 vs 502; all other errors use wrapping. `ErrDrainWindowExpired` is a `context.Cause` comparison value, not branchable. (AGENTS.md key decision 7)
- **`httputil.ReverseProxy` over hand-rolled proxy:** handles hop-by-hop headers, `X-Forwarded-For`, buffering; the interesting work is the selection layer. (AGENTS.md key decision 8)
- **RoundRobin index:** atomic uint64 counter, modulo len(healthy). No mutex. (AGENTS.md key decision 9)
- **LeastConnections tie-break:** first-in-registry-order among backends with equal ActiveConns. (AGENTS.md key decision 10)
- **LeastConnections scan:** full scan of healthy slice on every Select; no heap. (AGENTS.md key decision 11)
