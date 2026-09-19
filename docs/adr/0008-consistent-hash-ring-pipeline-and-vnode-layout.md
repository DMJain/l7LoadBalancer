# ADR-0008: Consistent-hash ring hash pipeline, virtual-node key order, and vnode count

- **Status**: Accepted
- **Date**: 2026-09-19
- **Deciders**: Darshan Jain (project owner) + opencode agent (S2.T1.1)

## Context

Sprint 2 adds session-sticky routing. The shared foundation is an
unexported, deterministic hash **ring** in `internal/balancer` that maps
any hash key to a backend, built on top of it by two selectors: the
deliberately-unwired `naiveConsistentHash` comparator (S2.T1.2) and the
operator-facing `ConsistentHashBoundedLoads` (S2.T2). The ring itself is
placement-only — no `Select`, no health awareness, no capacity awareness.

Three choices determine whether the ring distributes keys well or badly,
and all three are easy to "simplify" later because they look like
arbitrary style:

1. **Hash function and pipeline** — how a key string becomes a ring
   position.
2. **Virtual-node key format** — the string hashed to place each of a
   backend's virtual nodes.
3. **Virtual-node count** — how many ring positions each backend occupies.

The Sprint 2 design session
(`.scratch/s2-t1-t2-consistent-hash-bounded-loads/spec.md`) measured all
three and frozen the results; this ADR is the durable record, with the
evidence restated and independently reproduced during S2.T1.1 so a future
reader sees measurements, not bare assertions.

## Decision

1. **The ring is an unexported type in `internal/balancer`.** It has no
   `Select` method and no knowledge of health or load. Its only job is
   deterministic key→backend placement and ordered candidate iteration
   from a given key's ring position.

2. **The hash pipeline is stdlib `hash/fnv`'s `New64a()` → `Sum64()`,
   followed by a fixed Murmur3-style `fmix64` finalizer:**

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

   The **same** pipeline is used for virtual-node placement here and for
   request-key hashing in both selectors. It adds no dependency and stays
   inside the project's stdlib-only constraint.

3. **Virtual-node keys are built `<replica-index>:<backend-name>`** — index
   first, not name first.

4. **Each backend occupies 150 virtual nodes.** The ring is built exactly
   once, at construction, from every backend the registry holds
   (`Registry.All()`), regardless of health. No rebuild API exists:
   health flips must never rebuild the ring, and backend-membership
   changes belong to Sprint 4's hot-reload work.

5. **The ring exposes its ordered candidate walk as a Go 1.23
   range-over-func iterator (`iter.Seq[*backend.Backend]`).** The walk
   starts at the key's ring position, wraps around, and yields each
   distinct backend once. It is deliberately not a predicate callback
   (`selectWith(key, keep)`) — each selector's own skip logic (health-only,
   or health-plus-capacity) stays inline and visible in its own `Select`
   method instead of hidden behind a closure, and both selectors share one
   traversal implementation rather than duplicating ring-walk code.

## Evidence

Measurements below use the 4-backend fixture shape from the design session
(`http://10.0.0.1:9001` … `http://10.0.0.4:9004`), index-first vnode keys,
and 150 virtual nodes unless stated otherwise. The design session's numbers
are cited first; the reproduction column is what S2.T1.1 measured with an
independent harness.

### Why the `fmix64` finalizer is not decorative

Raw FNV-1a-64 has a near-linear response to a change in the final byte of
its input, so realistic client populations that differ only in a trailing
octet (a NAT'd office, a cloud VPC) hash to a narrow band of the ring.
Measured with 256 keys `203.0.113.0` … `203.0.113.255`:

| Pipeline | Backends hit (of 4) | Per-backend count |
|---|---|---|
| raw FNV-1a-64 | **3 of 4** | 90 / 0 / 100 / 66 |
| FNV-1a-64 + `fmix64` | **4 of 4** | 82 / 59 / 54 / 61 |

The design session reported the same finding ("1–3 of 4 backends"); the
reproduction lands at 3 of 4, with one backend receiving zero addresses.
The finalizer is the only thing standing between an entire /24 subnet and
a 2–3× imbalance, so it must not be removed as an apparent no-op.

### Why `index:name` rather than `name:index`

Hashing `"<name>:<index>"` makes every one of a backend's vnode strings
share a long, near-constant prefix, so their hashes inherit a correlated
offset; hashing `"<index>:<name>"` varies the prefix first. Measured on
raw FNV across 20,000 random client IPs:

| Vnode key order | Min share | Max share | Spread |
|---|---|---|---|
| `index:name` | 23.0% | 26.8% | **3.9 pts** |
| `name:index` | 6.1% | 36.2% | **30.1 pts** |

The design session measured the same separation (22–31% vs 9–42%) and
verified the gap does **not** close as vnode count rises, so it is a key
ordering problem, not a vnode-count problem. The reproduction confirms
both: `name:index` still spread 26.1 points at 1,000 vnodes.

**Post-finalizer caveat.** The gap above is measured on raw FNV, which is
where the ordering effect is stark. With the `fmix64` finalizer applied,
both orders are close to uniform (reproduction: `index:name` spread 5.5
pts, `name:index` 3.6 pts on the same fixture). `index:name` is retained
as the design-record choice — it costs nothing, it is the demonstrated
safer ordering before the finalizer, and it avoids relying on the
finalizer as the sole defense against correlated vnode prefixes. It is
not load-bearing *after* the finalizer.

### Why 150 virtual nodes

A backend on a ring occupies the arcs between its virtual-node positions;
with too few virtual nodes the per-backend arc share has high variance.
150 is inside the range consistent-hash implementations use for
reasonable small-cluster uniformity (groupcache's classic default is 40
replicas; many production libraries use 100–200), and it keeps a
4-backend ring at 600 entries — trivial to sort at startup and binary
search at request time. The reproduction shows 150 vnodes already yields
single-digit-percent spread after the finalizer; the exact count is a
smoothness/cost trade-off, not a correctness boundary.

## Consequences

- Positive: the ring's placement properties (stable mapping, minimal
  remapping on membership change, uniformity) are testable in isolation,
  without a selector, a registry health filter, or HTTP.
- Positive: one hash pipeline and one traversal implementation shared by
  both consistent-hash selectors; their skip conditions stay inline and
  diffable.
- Positive: no new dependency and no config surface — the pipeline, key
  order, and count are Go-level decisions, not operator knobs.
- Negative: the `fmix64` finalizer and the index-first key order look like
  arbitrary choices and are therefore at risk of being "cleaned up" later;
  this ADR exists to prevent exactly that.
- Negative: the ring is immutable for the life of the process, so
  membership changes require a rebuild (Sprint 4).
- Neutral: adding/removing a backend remaps roughly `1/n` of keys, the
  standard consistent-hashing property; this is expected, not a defect.
- Neutral: key ordering's measurable benefit is pre-finalizer only; the
  decision is retained for consistency with the design record and as
  defense in depth.

## Alternatives considered

- **Raw FNV-1a-64 with no finalizer** (`hash/fnv` alone, the obvious
  stdlib choice): rejected — collapses a /24 subnet onto 3 of 4 backends
  in the reproduction; see the table above.
- **A different hash family (xxhash, CRC32, SipHash):** rejected — xxhash
  adds a third-party dependency for no measured need, violating the
  stdlib-only constraint; CRC32 has weaker distribution; SipHash is keyed
  and heavier than needed for non-adversarial ring placement.
- **`name:index` vnode keys:** rejected — measured at a 30-point balance
  spread vs 4 points for `index:name` on raw FNV, with no improvement as
  vnode count rises.
- **A single ring position per backend (no virtual nodes):** rejected —
  one position gives each backend an arc length drawn from a single hash,
  whose variance is far too high for a 3–5 backend cluster.
- **A predicate-based `selectWith(key, keep func(*backend.Backend) bool)`:** rejected — it hides each selector's eligibility rule (health-only, or health-plus-capacity) behind a closure at the call site instead of stating it inline in that selector's own `Select` method, and two overlapping rules read worse as predicates than as a short `if`. The `iter.Seq` iterator keeps each selector's condition inline while sharing one traversal implementation.
- **A configurable vnode count / epsilon / key source as YAML fields:**
  deferred — no operator has asked to tune them; adding config surface is
  a one-line change later if one does.
- **Rebuilding the ring on health change:** rejected — health changes are
  frequent and the ring is health-independent by design; rebuilding would
  churn every key's placement for no reason. Only membership changes
  justify a rebuild, and those are Sprint 4.
