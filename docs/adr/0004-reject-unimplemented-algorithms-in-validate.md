# ADR-0004: Reject unimplemented algorithms in Validate

- **Status**: Accepted
- **Date**: 2026-09-18
- **Deciders**: project owner + opencode agent (S1.T2)

## Context

S1.T0.5 (ADR-0002) froze four algorithm identifier constants in
`internal/config` — `round_robin`, `least_conn`, `consistent_hash`,
`p2c_ewma` — plus an identifier table mapping each to a selector type and the
sprint that delivers it. The frozen constant block's descriptive comment
claimed these are "the exact string values accepted in the YAML `algorithm`
field".

Sprint 1 implements only `RoundRobin` and `LeastConnections`. The Sprint 2
selectors (`ConsistentHashBoundedLoads`, `PowerOfTwoChoicesEWMA`) do not exist
yet, so `config.Validate` checks `Algorithm` against a package-level
`implementedAlgorithms` set = `{round_robin, least_conn}` and rejects the
other two. That makes the frozen block comment contradict the actual
behaviour.

The S1.T2 code review flagged the contradiction: a descriptive comment in a
frozen file diverged from `Validate`'s behaviour without a superseding ADR,
which ADR-0002 requires for any deviation from the freeze.

## Decision

1. `config.Validate` accepts exactly the algorithms in the package-level
   `implementedAlgorithms` map: `round_robin` and `least_conn` in Sprint 1.
   `consistent_hash` and `p2c_ewma` are rejected with an `unsupported
   algorithm` error. The check is case-sensitive.

2. The four constants remain the **canonical vocabulary**: they name every
   algorithm the project intends to support and are the config-string →
   selector-type mapping consumed by `balancer.NewFromConfig`. The acceptance
   set is narrower than the vocabulary and is defined by `implementedAlgorithms`,
   not by the constant block.

3. This ADR supersedes the frozen constant block's descriptive claim that all
   four strings are accepted now. The comment is updated to point at this ADR
   and to distinguish vocabulary from acceptance.

4. Adding a Sprint 2 selector is a one-line addition to `implementedAlgorithms`,
   made atomically with the sprint that delivers the selector.

## Consequences

- Positive: an operator who configures an algorithm this build cannot run gets
  a clear error at config load naming the exact offending value, instead of a
  runtime failure when `balancer.NewFromConfig` is asked for a selector that
  does not exist.
- Positive: the frozen comment and the code now agree, and the deviation is
  recorded where ADR-0002 requires it.
- Negative: a config that is syntactically valid (uses a recognized constant)
  can still be rejected — the operator must delete or change the algorithm
  field. Acceptable: the alternative is a config that starts and then cannot
  route.
- Neutral: when Sprint 2 lands, only the map changes; the constants and this
  ADR's vocabulary/acceptance distinction stay valid.

## Alternatives considered

- **Accept all four at config load and fail later in `balancer.NewFromConfig`**:
  rejected — validation exists to answer "will this work?", not "does this
  parse?". A runtime panic on a recognized-but-unimplemented algorithm is a
  strictly worse failure than a config-time error naming the value.
- **Keep `Validate` accepting all four and let the factory be the gate**:
  rejected — same late-failure problem, and it would make the
  `implementedAlgorithms` map pointless.
- **Leave the comment as-is and change nothing**: rejected — the audit found
  the comment contradicts behaviour, and ADR-0002 forbids silent divergence
  from a frozen contract.
