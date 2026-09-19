# 01: Power-of-Two-Choices with EWMA Latency Tracking

**What to build:** The fourth and final Sprint-1-reserved algorithm,
`p2c_ewma`, end-to-end: `Backend` gains an EWMA-smoothed latency estimate
that the proxy updates on every request regardless of configured selector,
`PowerOfTwoChoicesEWMA` samples two random healthy backends and routes to the
faster one, and `p2c_ewma` becomes selectable in YAML exactly like the other
three algorithms.

**Blocked by:** None (S1.T3's `Backend`, S1.T6/ADR-0007's proxy lifecycle,
and the `balancer.Selector`/`config.implementedAlgorithms` seams are all
`[DONE]`)

**Status:** ready-for-agent

- [x] `Backend` gains `RecordLatency(d time.Duration)` and
      `EWMALatency() time.Duration`, backed by an unexported `atomic.Int64`
      nanoseconds field, method-only access (no exported field) — amends
      ADR-0002, symmetric with ADR-0006's `SetHealthy`
- [x] First-ever `RecordLatency` call for a given backend sets the value
      directly; every subsequent call blends via
      `latency_new = α·observed + (1-α)·latency_old` with `α = 0.1` as an
      unexported constant, applied through a CAS retry loop (not a mutex)
- [x] `errorHandler`'s failure path also calls `RecordLatency`, passing a
      fixed `2 * time.Second` penalty constant rather than the real elapsed
      time-to-failure
- [x] `modifyResponse` and `errorHandler` call `RecordLatency`
      unconditionally, on every request, regardless of which selector is
      actually configured — mirrors `IncActive`/`DecActive`'s existing
      unconditional behavior
- [x] `reqState` gains a new timestamp (distinct from the existing `start`
      used by the request-complete log line) set at the end of `director()`,
      so the recorded latency window is the backend round trip only
      (`director()` → `modifyResponse`), not the full client-facing request
      duration — with a comment at the field explaining why it's not the same
      measurement as `latency_ms`
- [x] `PowerOfTwoChoicesEWMA` fills in the frozen stub's exact signature
      (`NewPowerOfTwoChoicesEWMA(reg *backend.Registry) *PowerOfTwoChoicesEWMA`,
      `Select(ctx, r) (*backend.Backend, error)`); compile-time
      `Selector` assertion
- [x] `Select`: zero healthy → `ErrNoHealthyBackends`; exactly one healthy →
      returned directly with no draw; two or more → two distinct backends
      drawn via `math/rand/v2` package-level functions, lower `EWMALatency()`
      wins
- [x] `config.AlgorithmP2CEWMA` added to `config.implementedAlgorithms`;
      `balancer.NewFromConfig`'s switch routes it to
      `NewPowerOfTwoChoicesEWMA`
- [x] Test: first `RecordLatency` call sets the value directly (no blend from
      zero); a second call matches the α-blended expected value; concurrent
      `RecordLatency` calls are `-race`-clean and converge to a stable value
- [x] Test: `PowerOfTwoChoicesEWMA` empty healthy set → `ErrNoHealthyBackends`
- [x] Test: exactly one healthy backend is always returned with no draw
- [x] Test: a backend going unhealthy mid-run is skipped and resumes being
      chosen once healthy again (S1.T8's health-transition pattern)
- [x] Test: given two backends with a pre-seeded, sustained `EWMALatency()`
      gap, repeated `Select` calls choose the faster one measurably more
      often over a large sample — real recorded numbers, same evidentiary bar
      as ADR-0009's hot-key test
- [x] Test (proxy seam): a successful round trip records a real, non-zero,
      round-trip-scoped latency on the backend that served the request
- [x] Test (proxy seam): a round trip that fails before a response is
      received records the fixed `2s` penalty, not the real elapsed time
- [x] Test: `config.Validate` accepts `p2c_ewma`; `balancer.NewFromConfig`
      returns a working `*PowerOfTwoChoicesEWMA` for it
- [x] ADR-0010 recording: `Backend`-owned latency state (amends ADR-0002,
      symmetric with ADR-0006); cold-start first-sample-direct-set semantics;
      the fixed `2s` failure penalty, including why raw elapsed
      time-to-failure was rejected (a fast failure would look attractively
      fast) and the explicit note that `SLEEP_MS` has no enforced ceiling, so
      the penalty's margin is not resting on an assumed hard bound; one line
      noting `RecordLatency` runs unconditionally regardless of active
      selector
- [x] `CONTEXT.md` gains an entry for the EWMA-tracked latency estimate
- [x] `PROGRESS.md`: S2.T3 added and flipped to `[DONE]` on completion, per
      the single-task-entry pattern (S1.T4/T5/T8), not a multi-ticket spec
      split like S2.T1/T2

## Comments
