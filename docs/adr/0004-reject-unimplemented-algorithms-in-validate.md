# ADR-0004: Reject unimplemented algorithms in Validate

**Status:** Accepted

## Context

`internal/config` defines four algorithm constants: `round_robin`, `least_conn`, `consistent_hash` and `p2c_ewma` (ADR-0002). A build can name an algorithm before it has a selector for it. If config load accepts that name, the failure moves to `balancer.NewFromConfig` at startup or later.

## Decision

1. `config.Validate` accepts only the algorithms in the package-level map `implementedAlgorithms`. Any other value fails with an `unsupported algorithm` error. The check is case-sensitive.
2. The four constants are the vocabulary: every algorithm the project supports, and the config string that `balancer.NewFromConfig` maps to a selector type. `implementedAlgorithms` is the accepted set. It can be smaller than the vocabulary.
3. To add a selector, add one line to `implementedAlgorithms` in the same change that adds the selector.

All four algorithms are now in `implementedAlgorithms`.

## Consequences

- An operator who configures an algorithm that the build cannot run gets a clear error at config load. The error names the bad value.
- A recognized constant can still fail validation. This is better than a config that starts and then cannot route.
- Accepting all four names and failing in `balancer.NewFromConfig` was not used. Validation answers "will this work?", not "does this parse?".
