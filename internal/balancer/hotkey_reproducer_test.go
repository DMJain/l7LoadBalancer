//go:build offline

package balancer

import (
	"fmt"
	"math"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// This file is the offline reproducer the bounded-loads ADR cites: it sweeps
// many seeds of the exact hot-key workload shape the checked-in
// TestConsistentHashHotKeyRebalances pins to one seed, so the ADR can report a
// distribution rather than a single data point. It is excluded from normal
// `go test` runs by the `offline` build tag. Run it with:
//
//	go test -tags offline -run TestHotKeySeedDistribution -v ./internal/balancer/
func TestHotKeySeedDistribution(t *testing.T) {
	const (
		firstSeed = int64(1)
		seedCount = 60
	)

	var naiveShares, boundedShares []int
	totalFallbacks := 0

	for seed := firstSeed; seed < firstSeed+seedCount; seed++ {
		stream := buildKeyStream(seed, hotKeyClients, hotKeyRequests, hotKeySkew)

		naiveReg := hotKeyRegistry(t)
		naiveCounts, err := runStream(newNaiveConsistentHash(naiveReg), stream, false)
		require.NoError(t, err)
		naive := busiest(naiveCounts)
		naiveShares = append(naiveShares, naive)

		boundedReg := hotKeyRegistry(t)
		bounded := NewConsistentHashBoundedLoads(boundedReg)
		boundedCounts := make(map[string]int, 4)
		seedFallbacks := 0
		for _, addr := range stream {
			if bounded.wouldNeedFallback(addr) {
				seedFallbacks++
			}
			b, err := selectAddr(bounded, addr)
			require.NoError(t, err)
			require.NotNil(t, b)
			boundedCounts[b.Name]++
			b.IncActive()
		}
		boundedBusiest := busiest(boundedCounts)
		boundedShares = append(boundedShares, boundedBusiest)
		totalFallbacks += seedFallbacks

		fmt.Printf("seed %3d  naive busiest %4d (%4.1f%%)  bounded busiest %4d (%4.1f%%)  fallback-needed %d\n",
			seed, naive, pct(naive), boundedBusiest, pct(boundedBusiest), seedFallbacks)
	}

	nMin, nP10, nMed, nP90, nMax, nMean := summarize(naiveShares)
	bMin, bP10, bMed, bP90, bMax, _ := summarize(boundedShares)

	fmt.Printf("\nnaive   busiest-backend share over %d seeds: min %.1f%%  p10 %.1f%%  median %.1f%%  p90 %.1f%%  max %.1f%%  mean %.1f%%\n",
		seedCount, nMin, nP10, nMed, nP90, nMax, nMean)
	fmt.Printf("bounded busiest-backend share over %d seeds: min %.1f%%  p10 %.1f%%  median %.1f%%  p90 %.1f%%  max %.1f%%\n",
		seedCount, bMin, bP10, bMed, bP90, bMax)
	fmt.Printf("fallback-needed selections across all %d seeds: %d\n", seedCount, totalFallbacks)

	require.Zero(t, totalFallbacks,
		"the defensive fallback was needed at least once; the bounded-loads invariant is violated")
	fmt.Printf("capacity bound for %d requests over 4 backends: ceil(%d/4 * 1.25) = %d\n",
		hotKeyRequests, hotKeyRequests, int(math.Ceil(hotKeyRequests/4.0*1.25)))
}

func pct(n int) float64 {
	return 100 * float64(n) / hotKeyRequests
}

// summarize returns the min, p10, median, p90, max, and mean of the shares,
// as percentages of hotKeyRequests.
func summarize(shares []int) (min, p10, median, p90, max, mean float64) {
	s := append([]int(nil), shares...)
	sort.Ints(s)

	min, max = pct(s[0]), pct(s[len(s)-1])
	pick := func(p float64) float64 {
		idx := int(math.Round(p * float64(len(s)-1)))
		return pct(s[idx])
	}
	p10, median, p90 = pick(0.10), pick(0.50), pick(0.90)

	var total float64
	for _, v := range s {
		total += float64(v)
	}
	mean = 100 * (total / float64(len(s))) / float64(hotKeyRequests)
	return min, p10, median, p90, max, mean
}
