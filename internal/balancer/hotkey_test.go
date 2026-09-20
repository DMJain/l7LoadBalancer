package balancer

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// The fixed hot-key workload and its runner are shared by the checked-in
// comparative test and the build-tagged offline reproducer
// (hotkey_reproducer_test.go). Everything that consumes randomness is defined
// in one place and in one call order: any change to the sequence of draws
// changes what a given seed produces, so the checked-in seed's expected
// numbers only hold against this exact generator.

const (
	// hotKeySeed is a locked constant: the checked-in test asserts the
	// previously-verified busiest-backend shares this seed produces (naive
	// 4005, bounded 3126 of 10000 — the exact figures the Sprint 2 spec
	// froze). Changing it re-derives the numbers but not the property under
	// test.
	hotKeySeed = 2253

	hotKeyClients  = 100
	hotKeyRequests = 10000
	hotKeySkew     = 1.0
)

// zipfPMF returns the Zipf probability mass for ranks 1..n at exponent s,
// normalized to sum to 1. The standard harmonic-sum formula.
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

// buildKeyStream reproduces the fixed workload exactly: `clients` unique
// octet-diverse IPs via randomIP + dedup, shuffled, weighted by a Zipf(skew)
// distribution, then `requests` samples drawn as rng.Float64() and mapped
// through sort.SearchFloat64s against the cumulative distribution. Each key
// is returned as "host:port" so it is fed through requestHashKey like a real
// RemoteAddr.
func buildKeyStream(seed int64, clients, requests int, skew float64) []string {
	rng := rand.New(rand.NewSource(seed))

	seen := make(map[string]struct{}, clients)
	ips := make([]string, 0, clients)
	for len(ips) < clients {
		ip := randomIP(rng)
		if _, dup := seen[ip]; dup {
			continue
		}
		seen[ip] = struct{}{}
		ips = append(ips, ip)
	}
	rng.Shuffle(len(ips), func(i, j int) { ips[i], ips[j] = ips[j], ips[i] })

	pmf := zipfPMF(clients, skew)
	cumulative := make([]float64, len(pmf))
	running := 0.0
	for i, p := range pmf {
		running += p
		cumulative[i] = running
	}

	stream := make([]string, requests)
	for i := range stream {
		u := rng.Float64()
		idx := sort.SearchFloat64s(cumulative, u)
		if idx >= clients {
			idx = clients - 1
		}
		stream[i] = ips[idx] + ":12345"
	}
	return stream
}

// runStream selects every key in stream through sel, returning the per-backend
// selection counts. When accumulateLoad is true each selection increments the
// chosen backend's ActiveConns and never decrements it — a closed-loop
// worst case where in-flight requests do not drain. naiveConsistentHash
// ignores load, so it runs with accumulateLoad false; bounded-loads depends on
// the accumulating counts for its capacity check.
func runStream(sel Selector, stream []string, accumulateLoad bool) (map[string]int, error) {
	counts := make(map[string]int, 4)
	for _, addr := range stream {
		b, err := selectAddr(sel, addr)
		if err != nil {
			return nil, err
		}
		if b == nil {
			return nil, fmt.Errorf("selector returned nil backend for %q", addr)
		}
		counts[b.Name]++
		if accumulateLoad {
			b.IncActive()
		}
	}
	return counts, nil
}

// busiest returns the largest per-backend count.
func busiest(counts map[string]int) int {
	max := 0
	for _, c := range counts {
		if c > max {
			max = c
		}
	}
	return max
}

// wouldNeedFallback reports, from the selector's own ring and capacity
// formula, whether Select would exhaust a full traversal without admitting a
// healthy candidate. It is the offline reproducer's window into the defensive
// fallback branch: the branch reports nothing, so the reproducer checks the
// condition that would drive it. Call it immediately before the corresponding
// Select to see the same load snapshot. It shares admits with Select, so the
// evidence it produces cannot drift from the behavior it measures.
func (s *ConsistentHashBoundedLoads) wouldNeedFallback(addr string) bool {
	selectable := s.reg.Selectable()
	if len(selectable) == 0 {
		return false
	}
	capacity := capacityFor(selectable)
	for b := range s.ring.candidates(requestHashKey(requestForAddr(addr))) {
		if s.admits(b, capacity) {
			return false
		}
	}
	return true
}

// TestConsistentHashHotKeyRebalances is the checked-in, fixed-seed instance of
// the bounded-loads claim: under a Zipfian-skewed workload naive consistent
// hashing piles roughly two-fifths of all traffic onto one backend, while
// bounded-loads holds the busiest backend to the capacity bound
// (ceil(10000/4 * 1.25) = 3125, plus the one-over the `<=` admission rule
// allows). The assertion windows are the ones the Sprint 2 spec froze from
// this seed's verified output; the general distribution is in ADR-0009, backed
// by the offline reproducer.
func TestConsistentHashHotKeyRebalances(t *testing.T) {
	stream := buildKeyStream(hotKeySeed, hotKeyClients, hotKeyRequests, hotKeySkew)

	naiveCounts, err := runStream(newNaiveConsistentHash(hotKeyRegistry(t)), stream, false)
	require.NoError(t, err, "naive stream")

	boundedCounts, err := runStream(NewConsistentHashBoundedLoads(hotKeyRegistry(t)), stream, true)
	require.NoError(t, err, "bounded stream")

	naiveBusiest := busiest(naiveCounts)
	boundedBusiest := busiest(boundedCounts)

	assert.GreaterOrEqualf(t, naiveBusiest, 3800,
		"naive busiest backend took %d of %d (want in [3800, 4200]); counts=%v",
		naiveBusiest, hotKeyRequests, naiveCounts)
	assert.LessOrEqualf(t, naiveBusiest, 4200,
		"naive busiest backend took %d of %d (want in [3800, 4200]); counts=%v",
		naiveBusiest, hotKeyRequests, naiveCounts)
	assert.GreaterOrEqualf(t, boundedBusiest, 3100,
		"bounded busiest backend took %d of %d (want in [3100, 3130]); counts=%v",
		boundedBusiest, hotKeyRequests, boundedCounts)
	assert.LessOrEqualf(t, boundedBusiest, 3130,
		"bounded busiest backend took %d of %d (want in [3100, 3130]); counts=%v",
		boundedBusiest, hotKeyRequests, boundedCounts)
	assert.Lessf(t, boundedBusiest, naiveBusiest,
		"bounded-loads did not improve on naive: bounded=%d naive=%d",
		boundedBusiest, naiveBusiest)
}

// hotKeyRegistry builds the four-backend fixture the hot-key workload routes
// across. Names, not pointers, determine ring placement, so the naive and
// bounded selectors see the same ring.
func hotKeyRegistry(t *testing.T) *backend.Registry {
	t.Helper()
	return ringRegistry(t, "backend-a", "backend-b", "backend-c", "backend-d")
}
