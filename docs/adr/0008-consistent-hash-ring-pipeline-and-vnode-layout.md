# ADR-0008: Consistent-hash ring hash pipeline, virtual-node key order, and vnode count

**Status:** Accepted. Amended by ADR-0015: the ring rebuilds when the registry snapshot version changes after a reload.

## Context

Session-sticky routing uses a hash ring in `internal/balancer`. The ring maps a hash key to a backend. Two selectors use it: `ConsistentHashBoundedLoads` (the `consistent_hash` algorithm) and `naiveConsistentHash`, a comparator that only tests use. Three choices control how well the ring spreads keys: the hash function, the virtual-node key format, and the virtual-node count. All three look like arbitrary style, so a later cleanup could change them. This ADR records the measurements behind them.

## Decision

1. **The ring is an unexported type in `internal/balancer`.** It has no `Select` method and does not know about health or load. It places keys and walks candidates in ring order.
2. **The hash is `hash/fnv` `New64a()` → `Sum64()`, then a Murmur3 `fmix64` finalizer:**

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

   The same pipeline places virtual nodes and hashes request keys. It needs only the standard library.
3. **Virtual-node keys are `<replica-index>:<backend-name>`**, index first.
4. **Each backend has 150 virtual nodes** (`virtualNodesPerBackend`). The ring includes every backend in the registry snapshot, healthy or not. A health change never rebuilds the ring. A reload that changes membership gives a new snapshot version, and the selector then builds a new ring (ADR-0015).
5. **The candidate walk is an `iter.Seq[*backend.Backend]`.** It starts at the key's position, wraps around, and yields each distinct backend once. Each selector keeps its own skip condition inline in its `Select` method. A predicate callback was not used, because it hides the condition behind a closure.

## Evidence

The fixture has 4 backends (`http://10.0.0.1:9001` … `http://10.0.0.4:9004`), index-first keys and 150 virtual nodes, unless the text says otherwise.

**The `fmix64` finalizer is necessary.** Raw FNV-1a-64 responds almost linearly to a change in the last byte. Clients that differ only in the last octet (an office behind NAT, a cloud VPC) then fall in a narrow band of the ring. With the 256 keys `203.0.113.0` … `203.0.113.255`:

| Pipeline | Backends hit (of 4) | Per-backend count |
|---|---|---|
| raw FNV-1a-64 | 3 of 4 | 90 / 0 / 100 / 66 |
| FNV-1a-64 + `fmix64` | 4 of 4 | 82 / 59 / 54 / 61 |

Do not remove the finalizer as a no-op.

**Index-first keys.** With `"<name>:<index>"`, all virtual-node keys of a backend share a long prefix, so their hashes correlate. On raw FNV with 20,000 random client IPs:

| Vnode key order | Min share | Max share | Spread |
|---|---|---|---|
| `index:name` | 23.0% | 26.8% | 3.9 pts |
| `name:index` | 6.1% | 36.2% | 30.1 pts |

More virtual nodes do not close the gap: `name:index` still spreads 26.1 points at 1,000 virtual nodes. With `fmix64`, both orders are close to uniform (`index:name` 5.5 pts, `name:index` 3.6 pts). The project keeps `index:name` as defense in depth. It costs nothing, and the ring then does not depend only on the finalizer.

**150 virtual nodes.** Too few virtual nodes give a high variance in arc share. 150 is inside the usual range (groupcache uses 40, many libraries use 100–200). A 4-backend ring has 600 entries, which is cheap to sort at startup and to search for each request. With the finalizer, 150 gives a spread in single-digit percent. The count is a trade-off between smoothness and cost, not a correctness limit.

## Consequences

- Tests can check ring placement (stable mapping, small remap on membership change, uniformity) without a selector or HTTP.
- Both consistent-hash selectors share one hash pipeline and one walk.
- There is no new dependency and no config surface. Pipeline, key order and count are Go constants.
- A membership change remaps about `1/n` of keys. This is normal for consistent hashing.
- A different hash (xxhash, CRC32, SipHash) was not used. xxhash adds a dependency, CRC32 spreads keys less well, and SipHash is keyed and heavier than necessary.
- One ring position per backend was not used. One hash per backend gives too much variance for 3–5 backends.
- YAML fields for vnode count or key source were not added. No operator needs them, and each is a small change later.
- A rebuild on health change was not used. Health changes often, and the ring does not depend on health.
