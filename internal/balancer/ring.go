package balancer

import (
	"cmp"
	"hash/fnv"
	"iter"
	"slices"
	"strconv"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// virtualNodesPerBackend is the number of ring positions each backend
// occupies. A backend's share of the ring is the sum of the arcs between
// its positions; too few positions makes that share high-variance. 150 is
// inside the range consistent-hash implementations use for reasonable
// small-cluster uniformity. See ADR-0008.
const virtualNodesPerBackend = 150

// vnode is one position on the ring: a hash position and the backend that
// owns it. Each backend owns virtualNodesPerBackend of them.
type vnode struct {
	position uint64
	backend  *backend.Backend
}

// ring is the unexported consistent-hash ring: a deterministic mapping from
// a hash key to a backend, via sorted virtual-node positions. It is
// placement-only — no Select method, no health or capacity awareness.
//
// Concurrency: a ring is immutable after construction; vnodes is never
// written once newRing returns. candidates reads it without synchronization,
// so one ring may be shared by many goroutines. It is built once, at
// selector-construction time, from every backend the registry holds
// (Registry.All()) regardless of health — health flips never rebuild a
// ring; only backend-membership changes would, and that is Sprint 4's
// concern. See ADR-0008 and docs/design/sprint-1-contracts.md.
type ring struct {
	vnodes []vnode
}

// newRing builds the ring over every backend in all, placing each backend
// at virtualNodesPerBackend positions and sorting by hash position. The
// sort is stable so that (astronomically unlikely) equal hash positions
// keep a deterministic order.
func newRing(all []*backend.Backend) *ring {
	r := &ring{vnodes: make([]vnode, 0, len(all)*virtualNodesPerBackend)}
	for _, b := range all {
		for i := 0; i < virtualNodesPerBackend; i++ {
			// Index first: "<index>:<name>". Hashing "<name>:<index>" gives
			// every vnode of a backend a near-constant prefix, correlating
			// their hashes. Measured in ADR-0008.
			key := strconv.Itoa(i) + ":" + b.Name
			r.vnodes = append(r.vnodes, vnode{position: hashKey(key), backend: b})
		}
	}
	slices.SortStableFunc(r.vnodes, func(a, b vnode) int {
		return cmp.Compare(a.position, b.position)
	})
	return r
}

// candidates returns an iterator over the distinct backends on the ring,
// in ring order, starting at key's ring position and wrapping around. Each
// backend is yielded exactly once. Callers apply their own eligibility
// rules inline (health-only, or health-plus-capacity); the ring has none.
//
// It is an iter.Seq rather than a predicate-callback helper so that each
// selector's skip condition stays visible in its own Select method. See
// ADR-0008.
func (r *ring) candidates(key string) iter.Seq[*backend.Backend] {
	return func(yield func(*backend.Backend) bool) {
		n := len(r.vnodes)
		if n == 0 {
			return
		}
		h := hashKey(key)
		// A position with hash >= h: BinarySearchFunc returns the insertion
		// point, or any matching index on an exact hash collision. n means h
		// is past the end and the modulo below wraps the walk to the start.
		// Starting at any equal position is correct — equal positions share
		// one arc.
		start, _ := slices.BinarySearchFunc(r.vnodes, h, func(v vnode, target uint64) int {
			return cmp.Compare(v.position, target)
		})
		seen := make(map[*backend.Backend]struct{}, n/virtualNodesPerBackend)
		for i := 0; i < n; i++ {
			b := r.vnodes[(start+i)%n].backend
			if _, dup := seen[b]; dup {
				continue
			}
			seen[b] = struct{}{}
			if !yield(b) {
				return
			}
		}
	}
}

// hashKey maps a key to a ring position via stdlib FNV-1a-64 followed by a
// Murmur3-style fmix64 finalizer. The same pipeline places virtual nodes
// and, in the selectors, hashes request keys. The finalizer is load-bearing:
// raw FNV-1a-64 alone collapses a /24 subnet (keys differing only in the
// trailing octet) onto a minority of backends. See ADR-0008.
func hashKey(key string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(key)) // fnv's Writer never returns an error
	return fmix64(h.Sum64())
}

// fmix64 is the Murmur3 64-bit finalizer: an xor-shift/multiply avalanche
// that decorrelates the near-linear response FNV-1a-64 has to a change in
// the final input byte.
func fmix64(h uint64) uint64 {
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return h
}
