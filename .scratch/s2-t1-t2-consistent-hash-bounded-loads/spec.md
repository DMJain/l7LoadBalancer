# Spec: S2.T1–T2 — Consistent Hashing Ring and Bounded Loads

Status: ready-for-agent

---

## Problem Statement

Sprint 1 shipped two backend-selection algorithms, `RoundRobin` and `LeastConnections`, and neither provides session affinity: a request's destination depends only on selector-internal state (a rotation counter, live connection counts), never on anything about the client making the request. Consecutive requests from the same client can land on different backends every time, which defeats any workload that benefits from a client sticking to one backend — a per-backend warm cache, backend-local session state, anything where "same client, same backend" has value.

Consistent hashing is the textbook fix, but naive consistent hashing has its own well-known failure mode: because a hash ring has no notion of a backend's current load, a popular ("hot") key can send a disproportionate share of traffic to whichever single backend happens to own that key's ring position — the algorithm optimizes purely for routing stability, not for balance.

`docs/design/sprint-1-contracts.md`'s algorithm identifier table already reserves the `consistent_hash` config value for this need, and `config.Validate`'s `implementedAlgorithms` set is missing it — `balancer.NewFromConfig` rejects it, and `ConsistentHashBoundedLoads` is still a `panic("not implemented")` stub. An operator cannot get session-sticky routing at all today, and there's no way to demonstrate the load-balancing patterns Sprint 2 exists to showcase (per MILESTONES.md: "the intellectually meaty selection algorithms").

## Solution

Implement session-sticky routing with a load safety valve, split into two tasks that build on a shared primitive:

1. **S2.T1** — an unexported consistent-hash ring inside `internal/balancer` that deterministically maps a hash key to a backend, replicating each backend across many virtual nodes for distribution smoothness. On top of it, an unexported, **not config-selectable** `Selector` implementation (`naiveConsistentHash`) that walks the ring skipping only unhealthy candidates — health-aware but load-blind. It exists purely as a real, working, directly-testable comparator for the bounded-loads decision; it is deliberately never reachable through `balancer.NewFromConfig` or `config.implementedAlgorithms`.
2. **S2.T2** — `ConsistentHashBoundedLoads`, the fourth and final Sprint-1-reserved `Selector`, wired to the `consistent_hash` config identifier. It reuses S2.T1's ring but adds a per-candidate capacity check during the same walk: a backend is only selected if its current `ActiveConns()` is within `(1 + ε)` of the mean across healthy backends. A same-repo comparative test demonstrates, with real numbers, the exact property the algorithm exists for: under a skewed key-popularity workload, naive consistent hashing concentrates load on one backend while bounded-loads keeps every backend within its capacity bound.

After this lands, `consistent_hash` is a fourth fully-working, config-selectable algorithm (`p2c_ewma` remains a separate, out-of-scope Sprint 2 task), completing session-sticky routing with a load ceiling.

## User Stories

**Ring primitive (S2.T1)**

1. As a downstream selector implementer, I want a ring primitive that deterministically maps any hash key to a backend, so that repeated calls with the same key always agree without consulting any mutable state.
2. As a load balancer operator, I want each backend replicated across many virtual nodes on the ring, so that no single backend owns a disproportionate arc purely by chance of one hash value.
3. As a project maintainer, I want the ring's hash pipeline to run stdlib FNV-1a-64 through a fixed finalizer step before using the result as a ring position, so that IP-shaped keys differing only in a trailing octet still spread across all backends instead of collapsing onto one or two — raw FNV-1a-64 alone was verified, during this design session, to collapse an entire /24 subnet's 256 addresses onto 1–3 of 4 backends.
4. As a project maintainer, I want virtual-node keys built as `<replica-index>:<backend-name>` rather than `<backend-name>:<replica-index>`, so that different backends' vnode positions don't inherit a near-constant offset from each other — verified empirically to shift ring balance from a 9%–42% spread to a 22%–31% spread across four backends, and the gap does not close at higher vnode counts, so it isn't a vnode-count problem.
5. As a downstream developer, I want the ring built exactly once, at selector-construction time, from every configured backend regardless of health, so that a health flip never triggers a ring rebuild — only backend membership changes would, and membership changes are Sprint 4's problem (Sprint 1/2's `Registry` is immutable after construction).
6. As a downstream developer, I want the ring to expose its ordered candidate walk as a Go 1.23 range-over-func iterator (`iter.Seq[*backend.Backend]`), not a callback-style `selectWith(key, predicate)` helper, so that each selector's own skip logic stays visible inline in its `Select` method rather than hidden behind a predicate closure — and so no third traversal pattern gets invented later.
7. As a project maintainer, I want the ring's own properties (stable key→backend mapping, minimal remapping when backend membership changes, distribution uniformity across virtual nodes) tested directly against the ring's own API, so that selector-level noise (health checks, error paths) doesn't obscure what's actually being asserted — placement is a ring property, not a selector property.

**`naiveConsistentHash` (S2.T1)**

8. As a downstream developer, I want an unexported `naiveConsistentHash` Selector that walks the ring skipping only unhealthy candidates, so that S2.T1 produces a real, working, directly-testable comparator, not just a bare data structure.
9. As a project maintainer, I want `naiveConsistentHash` to satisfy `balancer.Selector` with its own compile-time interface assertion, so that it's tested exactly like `RoundRobin`/`LeastConnections` were — instantiated directly, exercised through `Select`.
10. As a project maintainer, I want `naiveConsistentHash` to never appear in `balancer.NewFromConfig`'s switch or in `config.implementedAlgorithms`, so that an operator can never accidentally select the load-blind variant — `consistent_hash` in YAML always means the bounded-loads algorithm.
11. As a future maintainer reading this code, I want an explicit doc comment on `naiveConsistentHash` stating why it's deliberately unwired and pointing at the ADR that explains the empirical-comparator rationale, so that nobody "fixes" what looks like a missing factory case. I also want the type deliberately not named `ConsistentHash`, so nobody reading `NewFromConfig` wonders why the `consistent_hash` config string doesn't map to it — it maps to `ConsistentHashBoundedLoads`, and the naive type's name stays outside that vocabulary on purpose.
12. As a load balancer operator, I want the same client IP to reliably route to the same backend under `naiveConsistentHash`, so that session affinity is demonstrably correct before bounded-loads' capacity logic is layered on top of it.
13. As a load balancer operator, I want `naiveConsistentHash` to return `ErrNoHealthyBackends` when every backend is unhealthy, so that it fails identically to `RoundRobin`/`LeastConnections` for that condition.

**`ConsistentHashBoundedLoads` (S2.T2)**

14. As a load balancer operator using `consistent_hash`, I want requests hashed by client IP to prefer the same backend across requests, so that I get session affinity without any configuration beyond the algorithm name.
15. As a load balancer operator, I want a backend excluded from selection once its active-connection count exceeds `(1 + ε)` times the mean across healthy backends, so that one backend can never be overwhelmed just because it happens to own a hot key's ring position.
16. As a project maintainer, I want ε fixed at 0.25 — the Mirrokni-Thorup-Zadimoghaddam (2016) paper's cited production value — so that the capacity bound has a literature-grounded justification rather than an arbitrary tuning knob.
17. As a project maintainer, I want the per-backend capacity computed as `max(1, ceil(avg_active × (1 + ε)))`, so that an idle system (zero in-flight requests, average load zero) doesn't fail every backend's very first request against a zero-sized cap.
18. As a project maintainer, I want "load" defined as `Backend.ActiveConns()` — the same live in-flight counter `LeastConnections` already reads, averaged only over currently-healthy backends — so that bounded-loads reuses existing, proven, concurrency-safe state instead of introducing new per-backend bookkeeping or a cumulative-assignment counter.
19. As a load balancer operator, I want the ring walk to skip a candidate for being either unhealthy or over capacity, evaluated in the same pass, so that both conditions are handled uniformly rather than as two separate mechanisms.
20. As a project maintainer, I want the ring walk capped at one full traversal, falling back to the least-loaded candidate seen if every backend is still over capacity at that point, so that a violated invariant degrades to a safe, bounded choice instead of an infinite loop — a defensive path the bounded-loads guarantee (given ε > 0 and the capacity floor) says should never actually trigger.
21. As a load balancer operator, I want `ConsistentHashBoundedLoads` to return `ErrNoHealthyBackends` under exactly the same condition the other three selectors do — the healthy set is empty — and never a distinct "all over capacity" error, since the capacity floor and fallback guarantee a backend is always returned whenever at least one is healthy.
22. As a downstream developer, I want virtual-node count, epsilon, and hash-key source left as unexported Go constants rather than new YAML fields, so that Sprint 2 doesn't grow config surface for values nobody has asked to tune yet.
23. As a load balancer operator, I want the hash key sourced from the client's IP (`r.RemoteAddr`, with the port stripped), so that stickiness is per-client without any new request-parsing machinery — with the known, documented limitation that behind another proxy this is the upstream proxy's IP, not the original client's.
24. As a project maintainer, I want virtual-node count fixed at 150 per backend — inside the range groupcache and similar consistent-hash implementations typically use for reasonable small-cluster uniformity — so the choice has the same literature/precedent grounding as epsilon rather than being picked arbitrarily.

**Hot-key comparative test (S2.T2)**

25. As a project maintainer writing the ADR for "why bounded-loads over naive," I want a same-repo test demonstrating the claim with real, reproducible numbers, so that the ADR reads as evidence, not an assertion backed only by a citation.
26. As a project maintainer, I want the checked-in hot-key test to run against one fixed, deterministic seed with tight assertion bounds computed from that exact seed's real, previously-verified output, so that the test cannot flake in CI the way a loose percentage floor sitting near its own observed minimum would.
27. As a project maintainer, I want the hot-key test's synthetic request keys generated with entropy spread independently across all four IP octets — never a sequential or incrementing suffix — so that the test measures genuine Zipfian skew rather than accidentally re-triggering the FNV trailing-byte collapse the hash-pipeline finalizer (story 3) exists to fix.
28. As a project maintainer, I want the bounded-loads assertion window to account for the capacity check's `<=` admission rule (a candidate at exactly the cap is still admitted, landing one over), so that a correct implementation isn't asserted against a boundary it will legitimately exceed by exactly one.
29. As a project maintainer, I want an offline reproducer — excluded from normal `go test` runs via a build tag — that sweeps many seeds of the same workload shape, so that the ADR can cite a distribution (minimum, median, p90, maximum) instead of a single cherry-picked data point.
30. As a load balancer operator, I want to see, in the ADR, concrete evidence that naive concentrates roughly 30–60% of a skewed workload onto one backend while bounded-loads holds every backend near `(1 + ε) · avg`, so that the "why bounded loads" decision is defensible against a skeptical reader, not just a paper citation.

**Cross-cutting**

31. As a project maintainer, I want the ring-construction decisions (hash pipeline, vnode key format, vnode count) recorded in one ADR closing with S2.T1, and the bounded-loads decisions (epsilon, load metric, capacity formula, probing/fallback, hot-key evidence) recorded in a second ADR closing with S2.T2, so that each task closes cleanly per this project's established per-task-ADR pattern (ADR-0006, ADR-0007) without an open decision waiting on the other task.

## Implementation Decisions

### Shared hash pipeline (introduced in S2.T1, reused unchanged by S2.T2)

Both virtual-node placement and request-key hashing run through the same two-step pipeline: stdlib `hash/fnv`'s `New64a()` → `Sum64()`, then a fixed Murmur3-style finalizer. Verified via a real Go prototype during this design session — not merely theorized — because raw FNV-1a-64 has a near-linear response to single-trailing-byte changes (the delta between `hash("203.0.113.0")` and `hash("203.0.113.1")` is exactly the FNV prime), which collapses realistic same-subnet client populations onto a small minority of backends. The finalizer is cheap, dependency-free, and fixes this without reopening the "stdlib-only hash function" decision:

```go
func fmix64(h uint64) uint64 {
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return h
}
```

Virtual-node keys are built as `strconv.Itoa(index) + ":" + backendName` (index first) — verified empirically to distribute far more evenly than the reverse order, independent of the finalizer.

### Ring primitive (S2.T1)

- Unexported type inside `internal/balancer`. Not itself a `Selector` — no `Select(ctx, r)` method, no health or capacity awareness. Its only job is deterministic key→backend placement and ordered-candidate iteration from a given key's ring position.
- Built once, at selector-construction time, from every backend the registry holds (`Registry.All()`), independent of current health. 150 virtual nodes per backend.
- Exposes its ordered candidate walk from a key's ring position (wrapping around, distinct backends only) as a Go 1.23 range-over-func iterator (`iter.Seq[*backend.Backend]`) rather than a predicate-callback helper — a deliberate choice so each selector's skip logic stays inline and visible in its own `Select` method instead of hidden behind a closure. Both `naiveConsistentHash` and `ConsistentHashBoundedLoads` range over the same iterator, each applying its own inline skip condition, avoiding duplicated ring-traversal logic between them.

### `naiveConsistentHash` (S2.T1)

- Unexported `Selector` implementation: walks the ring from the request's hash key, returns the first candidate for which `IsHealthy()` is true, returns `ErrNoHealthyBackends` if the walk exhausts every backend without finding one.
- Deliberately absent from `balancer.NewFromConfig` and `config.implementedAlgorithms`. Carries a doc comment naming the ADR that explains why: it exists solely as the empirical comparator the bounded-loads ADR and the hot-key test depend on.
- Hash key: the request's `RemoteAddr` with the port stripped (same source `ConsistentHashBoundedLoads` uses).

### `ConsistentHashBoundedLoads` (S2.T2)

- Exported `Selector`, wired to the `consistent_hash` config identifier in both `balancer.NewFromConfig`'s switch and `config.implementedAlgorithms` (a one-line addition to each, per the pattern ADR-0004 already established for adding a Sprint 2 selector).
- Same ring, same iterator-based walk from the key's ring position as `naiveConsistentHash`, but its inline skip logic checks two conditions per candidate: `IsHealthy()` and current load against capacity.
- Load = `ActiveConns()`, averaged only over currently-healthy backends. Capacity = `max(1, ceil(avg_active * 1.25))`. A candidate is admitted if its `ActiveConns()` is `<=` capacity.
- If the walk exhausts a full ring traversal without admitting a candidate, falls back to the least-loaded candidate seen during that traversal — a defensive path expected to never trigger given the capacity floor, not a normal-operation branch.
- Returns `ErrNoHealthyBackends` under the same single condition every other selector does: the healthy set is empty. No separate "all over capacity" error exists or is needed.
- Virtual-node count, epsilon, and hash-key source (`RemoteAddr`, port stripped) are unexported Go constants, not YAML fields — no `config.Config` schema change in this scope.

## Testing Decisions

### What makes a good test here

Tests exercise external behavior — selection outcome, error returns, measured load distribution — never internal implementation details. Table-driven where the input space is enumerable; `testify/require` for setup, `testify/assert` for value checks, matching the convention every prior Sprint 1 package established.

### Seams

Two seams, matching the package-level pattern ADR-0002 already established, plus a narrower, new, additive one:

1. **The ring's own API** — placement and walk-ordering properties (stable mapping, minimal remapping on backend-set changes, vnode distribution uniformity) exercised directly against the ring, with no `Selector`, no `Registry.Healthy()` filtering, and no HTTP involved. New in this scope, justified because these are ring properties, not selector properties, and testing them through `Select` would add health/error-path noise irrelevant to what's being asserted.
2. **`balancer.Selector` interface** — the existing Sprint 1 seam, reused unchanged for both `naiveConsistentHash` and `ConsistentHashBoundedLoads`: instantiated directly (not through `NewFromConfig`), exercised via `Select(ctx, r)` against a real `*backend.Registry` fixture, matching exactly how `RoundRobin` and `LeastConnections` are tested today.

### Test groupings and cases

- **Ring properties**: same key always maps to the same backend across repeated calls; adding or removing a backend remaps only roughly `1/n` of keys (the classic consistent-hashing minimal-disruption property), not a large fraction; virtual-node placement is reasonably uniform across backends under a large sample of random keys.
- **`naiveConsistentHash` conformance**: compile-time `Selector` assertion; same key → same backend repeatedly; a backend going unhealthy mid-run is skipped, and resumes being chosen once healthy again (mirroring S1.T8's cross-selector health-transition pattern); empty healthy set → `ErrNoHealthyBackends`.
- **Bounded-loads capacity behavior**: compile-time `Selector` assertion; pre-seeded `ActiveConns` values that push a backend over its capacity are skipped in favor of an under-capacity candidate; a backend going unhealthy mid-run is skipped regardless of its capacity headroom, and resumes being chosen once healthy again (the same health-transition case `naiveConsistentHash` and S1.T8 cover, since bounded-loads applies the identical health check alongside its capacity check); the idle-system floor (`avg_active = 0`) does not reject a backend's first request; empty healthy set → `ErrNoHealthyBackends`.
- **Hot-key comparative test**: constructs both `naiveConsistentHash` and `ConsistentHashBoundedLoads` over a fixed, deterministic, Zipfian-skewed (skew ≈ 1.0) workload of 10,000 requests across 100 distinct, octet-diverse synthetic client IPs routed to 4 backends. Asserts the busiest backend under naive selection falls inside a tight, previously-verified window (a fixed seed produced exactly 4,005 of 10,000 selections, ~40%, on the busiest backend — the assertion window is [3,800, 4,200]) and the busiest backend under bounded-loads falls inside a tight window around the capacity bound accounting for the `<=`-admission off-by-one (the same seed produced exactly 3,126 against a computed cap of 3,125 — the assertion window is [3,100, 3,130]). Exact request-key and request-stream generation (four independently-random IP octets per key, deduplicated to 100 keys, Zipfian weights via the standard harmonic-sum formula, a fixed `math/rand` source seeded at a locked constant) must be reproduced byte-for-byte between the reproducer and the checked-in test, since any change to the sequence of random draws changes what that seed produces:

```go
func randomIP(r *rand.Rand) string {
	return fmt.Sprintf("%d.%d.%d.%d", r.Intn(254)+1, r.Intn(254)+1, r.Intn(254)+1, r.Intn(254)+1)
}

func zipfPMF(n int, s float64) []float64 {
	w := make([]float64, n)
	total := 0.0
	for k := 1; k <= n; k++ {
		w[k-1] = 1.0 / math.Pow(float64(k), s)
		total += w[k-1]
	}
	for i := range w {
		w[i] /= total
	}
	return w
}
// Generate 100 unique IPs via randomIP + dedup, shuffle them, build the
// zipf cumulative distribution, then draw 10,000 r.Float64() samples via
// sort.SearchFloat64s against the cumulative distribution to build the
// request stream — in that exact call order.
```

- **Offline reproducer**: same workload shape as the hot-key test, swept across many seeds (build-tagged out of normal `go test` runs, invoked manually). Its summary statistics — verified during this design session at minimum 28.7%, p10 32.7%, median 39.1%, p90 48.9%, maximum 67.0%, mean 40.1% busiest-backend share under naive selection, across 60 seeds — are what the bounded-loads ADR cites as the general claim; the checked-in hot-key test's fixed seed is the specific, stable, CI-safe instance of that claim. Across every one of those 60 seeds (and 50 more in an earlier pre-fmix64 pass), the ring-exhaustion fallback described above never fired once — empirical support, not just theoretical, for treating it as a defensive path rather than a normal-operation branch.

### Prior art

- `internal/balancer`'s existing selector tests (S1.T4, S1.T5, S1.T8) set the pattern this phase follows: direct instantiation, a real `*backend.Registry` fixture, no mocks, `-race`-clean concurrency where relevant.
- This grilling session's design-tree record is the authoritative source for the empirically-derived numbers above (hash pipeline finalizer, vnode key order, both hot-key assertion windows) — restated here, not independently re-derived.

## Out of Scope

- **`PowerOfTwoChoicesEWMA` (`p2c_ewma`)** — the other Sprint 2 algorithm; a separate, not-yet-scoped task. Its stub remains a panic.
- **YAML config surface for consistent-hash tuning** (virtual-node count, epsilon, hash-key source) — deliberately kept as Go constants; no `config.Config` schema change in this scope.
- **Dynamic ring membership** (adding/removing backends without restart) — `Registry` is immutable after construction through Sprint 3; the ring is built once and never needs an `Add`/`Remove` API until Sprint 4's hot-reload work exists.
- **`X-Forwarded-For` / behind-another-proxy client IP resolution** — the hash key is `RemoteAddr` as-is; the known limitation (an intermediate proxy's IP, not the original client's) is documented, not solved, here.
- **Health checking, circuit breaking, metrics** (Sprint 3) — both new selectors call `IsHealthy()` exactly like Sprint 1's selectors; no new health-state producer is introduced.
- **Connection pool tuning, HTTP/2, the benchmark rig** — unrelated later-sprint work.
- **A cumulative/lifetime per-backend load counter** — considered and rejected in favor of reusing `ActiveConns()`; not built.

## Further Notes

### Two ADRs, not one

MILESTONES.md lists a single Sprint 2 ADR ("why bounded-loads over naive CH"), written before this two-task split existed. This spec splits it into two, following this project's established per-task-ADR pattern (ADR-0006 for S1.T3, ADR-0007 for S1.T6): the S2.T1 ADR covers hash pipeline, vnode key format, and vnode count; the S2.T2 ADR covers epsilon, the load metric, the capacity formula, and the hot-key evidence. Each task closes with its own decision recorded, rather than S2.T1 closing with an open decision waiting on S2.T2.

### Findings that must survive into the ADRs, not just this spec

Two things discovered during this design session are easy to accidentally "simplify away" later because they look like arbitrary implementation choices rather than verified fixes for real bugs:

- The finalizer step after FNV-1a-64 is not decorative. Removing it silently reintroduces the /24-subnet collapse (a realistic NAT'd-office or cloud-VPC client population landing almost entirely on 1–3 of 4 backends).
- The `index:name` virtual-node key order is not arbitrary style. The reverse order (`name:index`) was measured to produce a persistently worse ring balance that does not improve with more virtual nodes.

Both are cited with their measured numbers in the Implementation Decisions section above specifically so a future reader — human or agent — sees the evidence, not just the choice.

### `CONTEXT.md`

This design session created the project's first `CONTEXT.md`, defining `Backend`, `Selector`, `Ring`, `Virtual node`, `Hash key`, `Load`, and `Capacity` in the consistent-hash-bounded-loads sense. Read it alongside this spec — it's the canonical vocabulary these user stories and implementation decisions use throughout.
