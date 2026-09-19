# 01: Consistent-Hash Ring Primitive

**What to build:** An internal, deterministic hash ring that maps any hash key to a backend, with a hardened hash pipeline and virtual-node replication, so a stable, evenly-distributed backend mapping exists as the shared foundation both consistent-hash selectors will build on — and so its own correctness (stable mapping, minimal disruption on membership change, distribution uniformity) is proven before any selector logic is layered on top of it.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [x] Ring type is unexported, lives in `internal/balancer`; no `Select` method, no health or capacity awareness — placement only
- [x] Hash pipeline: stdlib `hash/fnv`'s `New64a()` → `Sum64()`, then a fixed Murmur3-style `fmix64` finalizer (xor-shift/multiply ×2); the same pipeline is used for virtual-node placement here and, in later tickets, for request-key hashing
- [x] Virtual-node keys are built as `<replica-index>:<backend-name>` (index first, not name first)
- [x] 150 virtual nodes per backend
- [x] Ring is built exactly once, at construction time, from every backend the registry holds (`Registry.All()`), regardless of health — no rebuild on health change; only backend-membership changes would require one, and that's Sprint 4's problem
- [x] Exposes an ordered candidate walk from a given key's ring position as a Go 1.23 range-over-func iterator (`iter.Seq[*backend.Backend]`), wrapping around the ring, yielding each distinct backend once — not a callback/predicate-style helper
- [x] Table-driven test: the same key always maps to the same backend across repeated calls
- [x] Test: adding or removing a backend remaps only roughly `1/n` of keys, not a large fraction (the classic consistent-hashing minimal-disruption property)
- [x] Test: virtual-node placement is reasonably uniform across backends under a large sample of random keys
- [x] ADR recording the hash pipeline, virtual-node key order, and virtual-node count decisions, citing the measured evidence behind each:
  - raw FNV-1a-64 alone collapses an entire /24 subnet (256 addresses differing only in the last octet) onto 1–3 of 4 backends; the finalizer fixes it
  - `index:name` key order was measured at a 22%–31% balance spread across four backends vs. 9%–42% for `name:index`, and the gap does not close at higher vnode counts
  - 150 vnodes is inside the range groupcache and similar implementations use for reasonable small-cluster uniformity
