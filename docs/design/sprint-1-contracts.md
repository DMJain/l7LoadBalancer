# Sprint 1 — Frozen Interfaces & Schema Contracts

Written by S1.T0.5 (Phase A: analysis and contract definition only, no
logic). This is the reference every Sprint 1 implementation task (T1–T10)
must conform to. If an implementer needs to deviate from something here,
stop and write an ADR superseding ADR-0002, or ask the user — do not
silently diverge.

## Package dependency graph

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
internal/health   — leaf, no internal deps yet (Sprint 3; will depend on backend)
internal/circuit  — leaf, no internal deps yet (Sprint 3; will depend on backend)
```

Concretely, as frozen in the stubs:

- `internal/backend` imports `internal/config` (for `BackendConfig`).
- `internal/balancer` imports `internal/backend` and `internal/config`.
- `internal/proxy` imports `internal/backend` and `internal/balancer`.
- `cmd/l7LoadBalancer` (S1.T7) imports `internal/config`, `internal/backend`,
  `internal/balancer`, `internal/proxy`, `internal/logger`.

**No cycles.** Explicitly: `internal/backend` does NOT import
`internal/balancer` — a `Backend` has no notion of how it's selected. If a
future task is tempted to add that import, it's a sign the abstraction is
leaking and should be reconsidered rather than coded around.

## Interface placement decision

`Selector` is defined in `internal/balancer`, not `internal/proxy`.

**Rationale**: multiple implementations (`RoundRobin`, `LeastConnections`
in Sprint 1; `ConsistentHashBoundedLoads`, `PowerOfTwoChoicesEWMA` in
Sprint 2) live in `balancer` and all need to satisfy one contract in the
same place they're defined. The consumer (`proxy`) only ever needs the
interface *type* to hold a field and call `Select` — it has no methods of
its own to hide behind an interface, so the usual "define interfaces at
the consumer" guidance doesn't add anything here; it would just force
`balancer` to import a type from `proxy` or duplicate the interface.
Recorded in ADR-0002.

The selector **factory**, `balancer.NewFromConfig`, also lives in
`balancer` rather than `main.go`. Rationale: the config-string-to-type
mapping (see the algorithm table below) is balancer's own concern — it's
the package that knows what `"round_robin"` maps to. `main.go` (S1.T7)
just calls it. This keeps `main.go` a thin wiring layer per AGENTS.md's
architecture summary.

## YAML schema

Full example — this becomes `configs/example.yaml` verbatim in S1.T2:

```yaml
# Example configuration for l7LoadBalancer.

# Address the load balancer listens on.
listen: ":8080"

# Backend selection algorithm. One of:
#   round_robin      - rotate across healthy backends (S1.T4)
#   least_conn       - pick the healthy backend with fewest active connections (S1.T5)
#   consistent_hash  - bounded-load consistent hashing (Sprint 2)
#   p2c_ewma         - power-of-two-choices with EWMA latency (Sprint 2)
# Defaults to round_robin if omitted.
algorithm: "round_robin"

# Upstream backends. At least one required. Names must be unique.
backends:
  - name: "backend-a"
    url: "http://127.0.0.1:9001"
  - name: "backend-b"
    url: "http://127.0.0.1:9002"
  - name: "backend-c"
    url: "http://127.0.0.1:9003"
```

Field names are lowercase snake_case: `listen`, `algorithm`, `backends`,
`name`, `url`. `Load` uses `yaml.NewDecoder(f).KnownFields(true)` (per
PROGRESS.md S1.T2 acceptance) — unknown fields are a load error, not
silently ignored.

## Algorithm identifier table

| Config string      | Const name                       | Selector type                          | Delivered in |
|---------------------|-----------------------------------|-----------------------------------------|--------------|
| `round_robin`       | `config.AlgorithmRoundRobin`      | `balancer.RoundRobin`                   | S1.T4        |
| `least_conn`        | `config.AlgorithmLeastConn`       | `balancer.LeastConnections`             | S1.T5        |
| `consistent_hash`   | `config.AlgorithmConsistentHash`  | `balancer.ConsistentHashBoundedLoads`   | Sprint 2     |
| `p2c_ewma`          | `config.AlgorithmP2CEWMA`         | `balancer.PowerOfTwoChoicesEWMA`        | Sprint 2     |

## Error handling convention

Wrapped errors via `fmt.Errorf("<pkg>: %w", err)` throughout — e.g.
`fmt.Errorf("config: %w", err)`, `fmt.Errorf("balancer: %w", err)`.

Exported sentinels exist **only** for conditions a caller needs to branch
on programmatically. As of this freeze that's exactly one:
`balancer.ErrNoHealthyBackends`, which `proxy` checks via `errors.Is` to
decide "return 503" vs. "return 502 / log unexpected error". `config`
deliberately has no exported validation sentinels — callers don't need to
distinguish "empty listen" from "bad URL" programmatically, only render
the wrapped message.

## Log field vocabulary

Canonical field names (from `internal/logger/doc.go`): `backend`,
`method`, `status`, `latency_ms`, `remote_addr`, `path`.

Example log line per event type (JSON via `slog.NewJSONHandler`):

- **Request start**
  ```json
  {"time":"2026-09-01T16:10:00Z","level":"INFO","msg":"request start","method":"GET","path":"/api/widgets","remote_addr":"10.0.0.7:54321"}
  ```
- **Request complete**
  ```json
  {"time":"2026-09-01T16:10:00Z","level":"INFO","msg":"request complete","method":"GET","path":"/api/widgets","backend":"backend-a","status":200,"latency_ms":12.4}
  ```
- **Backend ejected** (Sprint 3)
  ```json
  {"time":"2026-09-01T16:10:00Z","level":"WARN","msg":"backend ejected","backend":"backend-b"}
  ```
- **Config reloaded** (Sprint 4)
  ```json
  {"time":"2026-09-01T16:10:00Z","level":"INFO","msg":"config reloaded","event":"config_reloaded","added":1,"removed":1,"unchanged":2}
  ```
  Logged at WARN instead of INFO when `unchanged` is zero (a blue/green
  reload's brief empty-selectable window). S4.T3 extended this frozen line
  with the event and the three counts.
- **Config reload failed** (Sprint 4)
  ```json
  {"time":"2026-09-01T16:10:00Z","level":"WARN","msg":"config reload failed","event":"config_reload_failed","reason":"non_backend_change","fields":["listen"]}
  ```
  `reason` is one of `parse_error`, `validation_error`, `non_backend_change`,
  or the defensive `apply_error`; a non-backend change names the changed
  fields under `fields`. Added by S4.T3.

## Metric name and label reservations

From `internal/metrics/doc.go`:

- Metric name prefixes: `lb_requests_total`, `lb_request_duration_seconds`,
  `lb_backend_healthy`, `lb_circuit_state`, `lb_active_connections`
  (added in S3.T4, amending this reservation — ADR-0013 decision 7),
  `lb_health_probe_total` (added in S3.T12, amending this reservation —
  ADR-0014).
- Labels: `backend`, `method`, `status_class` — deliberately **not**
  `status_code`, to avoid unbounded cardinality from arbitrary upstream
  status codes. `lb_circuit_state` additionally carries `state`
  (`closed`/`open`/`half_open`) as a label enum. `lb_health_probe_total` is
  probe-scoped: it carries `endpoint` (`/livez`/`/readyz`/`/startupz`) and
  `status` (the same status-class vocabulary as `status_class`), and does not
  carry `backend`/`method` — so the request and probe counters never share a
  label set.

Proposed latency histogram bucket boundaries (seconds), to be overridden
with real measured data once Sprint 1's benchmark/manual-smoke numbers
exist (see S1.T10):

```
.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10
```

## Concurrency ownership table

| Field | Owner (writer) | Readers | Sync mechanism |
|---|---|---|---|
| `Backend.healthy` | Health checker (active probe + passive outlier detection, Sprint 3); initial value set by `NewRegistry` (S1.T3) | Selectors (via `IsHealthy()`), proxy, metrics (Sprint 3) | `atomic.Bool`, accessed only via `IsHealthy()` |
| `Backend.active` | Proxy: `IncActive()` before dispatch, `DecActive()` on response-body `Close()` (S1.T6) | `LeastConnections` selector (via `ActiveConns()`), metrics (Sprint 3) | `atomic.Int64`, accessed only via `IncActive`/`DecActive`/`ActiveConns()` |
| `Backend.removed` | `Registry.Apply` (S4.T2), immediately before the snapshot swap | Proxy observer fan-out (via `IsRemoved()`), `ConsistentHashBoundedLoads` admission | `atomic.Bool`, set once, accessed only via `IsRemoved()`; never reset on an instance (a re-added identity is a fresh `Backend`) |
| `Registry`'s backend set | `NewRegistry` at construction (S1.T3); `Registry.Apply` (S4.T2), single writer, called by the reload goroutine (S4.T3) | `All()`, `Selectable()`, `Version()`, `Snapshot()`, all selectors | One `atomic.Pointer` to an immutable `(version, []*Backend)` snapshot; readers load once and never lock. See ADR-0015. |
| `RoundRobin`'s rotation counter | `RoundRobin.Select`, on every call (S1.T4) | none external | Atomic counter (`atomic.Uint64` or `atomic.Int64`), no mutex |
| `config.Config` (loaded) | `Build` at startup (S4.T0); the reload operation (S4.T3) replaces it last | `internal/app` readers via `LoadedConfig()`, the reload operation | One `atomic.Pointer[Config]` on `App` holding the last applied config. See ADR-0015. |
| Circuit breaker state (Sprint 3) | `circuit` package's state machine goroutine | Proxy `Director`/`ModifyResponse` | TBD — likely mutex, since closed→open→half-open transitions are compound (check-then-act), not a single atomic op. Decide in the Sprint 3 ADR. |

## Deviations / amendments from PROGRESS.md flagged by this freeze

- **`Backend.healthy` / `Backend.active` visibility**: PROGRESS.md's S1.T3
  acceptance text lists `Healthy atomic.Bool` and `ActiveConns
  atomic.Int64` as if they were exported struct fields, while also saying
  downstream code must only touch them via `IsHealthy()`. This freeze
  resolves the ambiguity by making both fields **unexported**
  (`healthy`, `active`), reachable only through
  `IsHealthy`/`IncActive`/`DecActive`/`ActiveConns()`. The frozen stub in
  `internal/backend/backend.go` is authoritative for S1.T3; PROGRESS.md's
  own acceptance wording was not edited (out of scope for this task) but
  should be read in light of this note.
