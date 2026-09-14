# ADR-0002: Interface placement and Sprint 1 schema freeze

- **Status**: Accepted
- **Date**: 2026-09-01
- **Deciders**: project owner + S1.T0.5 agent

## Context

Sprint 1 (T1–T10) implements config loading, the backend registry, two
selectors, and the proxy — cross-cutting contracts that multiple,
potentially different, agent sessions will implement independently
(PROGRESS.md already anticipates this: "T1–T10, potentially executed by
different agents in different sessions"). Without a frozen contract,
different sessions could disagree on struct shapes, method signatures,
error strategy, YAML field names, log field names, or metric label names,
producing integration breakage discovered late (at T6/T7 wiring) instead
of early.

## Decision

1. **Freeze before implementation.** Before any Sprint 1 task beyond T0
   writes real logic, define every cross-package contract as a compiling
   Go stub (panic bodies or trivial impls only) plus a design doc
   (`docs/design/sprint-1-contracts.md`). All of T1–T10 must conform to
   these signatures; a deviation requires a superseding ADR or explicit
   user sign-off, not a silent change.

2. **`Selector` interface lives in `internal/balancer`, not
   `internal/proxy`.** Rationale: `balancer` hosts four implementations
   (two in Sprint 1, two in Sprint 2) that all need to satisfy the same
   contract where they're defined. `proxy`, the consumer, only stores and
   calls the interface — it exposes no methods of its own that the
   interface would need to hide, so "define interfaces at the consumer"
   would just add an import-direction inversion for no isolation benefit.
   The selector factory (`balancer.NewFromConfig`) is placed in `balancer`
   for the same reason: it owns the config-string → selector-type mapping.

3. **Algorithm identifiers are snake_case string constants** in
   `internal/config`: `AlgorithmRoundRobin = "round_robin"`,
   `AlgorithmLeastConn = "least_conn"`, `AlgorithmConsistentHash =
   "consistent_hash"`, `AlgorithmP2CEWMA = "p2c_ewma"`. Chosen for
   YAML-readability and consistency with the project's snake_case YAML
   field convention; matched 1:1 against selector types in
   `balancer.NewFromConfig`.

4. **Log field names and metric label/name reservations are frozen now**,
   in `internal/logger` and `internal/metrics` package docs, even though
   metrics aren't implemented until Sprint 3. Rationale: any request-path
   code written from S1.T6 onward will emit log lines, and getting the
   field vocabulary right before that code exists is cheaper than a
   grep-and-rename later. Metric labels specifically exclude
   `status_code` in favor of `status_class` to bound cardinality — this
   is a Sprint 3 implementation concern but the naming decision doesn't
   need to wait.

5. **`Backend.healthy` and `Backend.active` are unexported**, accessed
   only via `IsHealthy()` / `IncActive()` / `DecActive()` /
   `ActiveConns()`. This amends the field-visibility wording in
   PROGRESS.md's S1.T3 acceptance criteria (which showed them as exported
   `Healthy`/`ActiveConns` fields). Rationale: encapsulating the atomic
   type behind methods lets Sprint 3 replace `atomic.Bool` with a richer
   health-state enum without touching every caller in `balancer` and
   `proxy`.

## Consequences

- Positive: T1–T10 have one unambiguous reference to build against,
  reducing cross-session integration risk. Traceable record of
  *why* the interface lives where it does.
- Positive: log/metric vocabulary is consistent from the first line of
  request-path code, not retrofitted.
- Negative: any real deviation discovered mid-implementation (e.g. a
  selector needing an extra constructor argument) requires a follow-up
  ADR or stub edit before proceeding, adding friction at that point.
- Neutral: this freeze is Phase A only — no behavior changed; `main.go`
  still returns 501.

## Alternatives considered

- **Define `Selector` in `internal/proxy`** (consumer-side, per the
  general Go guidance in AGENTS.md): rejected — `balancer` needs the
  interface locally to declare four implementations and the factory, and
  proxy has no methods to hide behind it, so consumer-side placement
  would only add an awkward import from `balancer` back to `proxy`.
- **Skip the freeze, let T1 start immediately**: rejected — the explicit
  premise of this task is that unfrozen contracts between multi-session
  agent work is the exact failure mode AGENTS.md's session-log and ADR
  discipline exists to prevent.
- **Keep `Backend.Healthy`/`ActiveConns` exported per the literal
  PROGRESS.md S1.T3 wording**: rejected — PROGRESS.md's own acceptance
  text simultaneously requires all downstream access go through
  `IsHealthy()`, which only unexported fields can actually enforce at
  compile time.
