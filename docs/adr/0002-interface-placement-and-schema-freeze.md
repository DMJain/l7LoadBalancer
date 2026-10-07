# ADR-0002: Interface placement and schema freeze

**Status:** Accepted. Amended by ADR-0006, ADR-0010, ADR-0011, ADR-0012, ADR-0015, ADR-0016 and ADR-0017. Each of these adds methods to `Backend` (decision 5).

## Context

Config loading, the backend registry, the selectors and the proxy share contracts across packages. If different changes disagree on struct shapes, method signatures, error strategy, YAML field names, log field names or metric labels, the parts break when they are wired together. The project defines these contracts first, as compiling Go stubs, before it writes the logic.

## Decision

1. **Contracts come before logic.** Every cross-package signature exists before its implementation. A change to one of these signatures needs an ADR.
2. **The `Selector` interface is in `internal/balancer`, not `internal/proxy`.** `balancer` holds all four implementations and the factory `balancer.NewFromConfig`, which maps the config string to a selector type. The proxy only stores and calls the interface. A consumer-side interface would only reverse the import direction and give no isolation. This is the one exception to the rule "define interfaces at the consumer".
3. **Algorithm identifiers are snake_case string constants in `internal/config`:** `AlgorithmRoundRobin = "round_robin"`, `AlgorithmLeastConn = "least_conn"`, `AlgorithmConsistentHash = "consistent_hash"`, `AlgorithmP2CEWMA = "p2c_ewma"`. This matches the snake_case YAML field style.
4. **Log field names and metric names are fixed early**, in the package docs of `internal/logger` and `internal/metrics`. Metric labels use `status_class`, not `status_code`, to bound cardinality.
5. **`Backend` state fields are unexported.** Callers use methods such as `IsHealthy()`, `IncActive()`, `DecActive()` and `ActiveConns()`. The project can then change the type behind a field (for example, `atomic.Bool` to a state enum) without a change to callers in `balancer` and `proxy`.

## Consequences

- Each part has one reference to build against, so integration problems show early.
- The log and metric vocabulary is the same from the first line of request-path code.
- A real need to change a signature costs an ADR or a stub edit first.
- Each later ADR that adds a `Backend` method amends decision 5. The fields stay unexported.
- `Selector` in `internal/proxy` was not used. `balancer` needs the interface locally for its implementations and factory, and an import from `balancer` to `proxy` adds nothing.
- Exported `Healthy`/`ActiveConns` fields were not used. Only unexported fields can force all access through methods at compile time.
