package balancer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// This file is the cross-cutting counterpart to roundrobin_test.go and
// leastconn_test.go. Rather than re-testing each algorithm's distribution,
// it proves the two Sprint 1 selectors are interchangeable with respect to
// health transitions: health-awareness must be a property of the Selector
// contract, not something one implementation happens to get right.
//
// The per-implementation compile-time assertions
// (`var _ Selector = (*RoundRobin)(nil)` / `(*LeastConnections)(nil)`) live
// next to each type in roundrobin.go and leastconn.go and are re-checked on
// every build, so they are deliberately not duplicated here.

// selectorFactory builds a fresh selector over reg, so each table case
// starts from identical state — notably RoundRobin's rotation counter.
type selectorFactory struct {
	name string
	new  func(reg *backend.Registry) Selector
}

func sprint1Selectors() []selectorFactory {
	return []selectorFactory{
		{name: "RoundRobin", new: func(reg *backend.Registry) Selector { return NewRoundRobin(reg) }},
		{name: "LeastConnections", new: func(reg *backend.Registry) Selector { return NewLeastConnections(reg) }},
	}
}

// selectNames makes n selections through s and returns the chosen backend
// names in order. Every selection is required to succeed (non-nil, no error)
// against the three-backend fixture.
func selectNames(t *testing.T, s Selector, n int) []string {
	t.Helper()
	got := make([]string, 0, n)
	for i := 0; i < n; i++ {
		got = append(got, selectName(t, s))
	}
	return got
}

// TestSelectorsRespectHealthTransitions runs one scripted sequence against
// both Sprint 1 selectors: a target backend is chosen while healthy, stops
// being chosen the moment it goes unhealthy, and resumes once it recovers.
func TestSelectorsRespectHealthTransitions(t *testing.T) {
	const target = "backend-b"

	for _, sf := range sprint1Selectors() {
		t.Run(sf.name, func(t *testing.T) {
			reg := newTestRegistry(t)
			// Model in-flight traffic on the two non-target backends so that
			// LeastConnections actually prefers the idle target. RoundRobin
			// ignores ActiveConns, so one fixture drives both selectors and
			// the script stays identical across implementations.
			seedActive(t, reg, "backend-a", 1)
			seedActive(t, reg, "backend-c", 1)
			sel := sf.new(reg)

			t.Run("healthy baseline chooses target", func(t *testing.T) {
				chosen := selectNames(t, sel, 6)
				assert.Contains(t, chosen, target,
					"target must be selectable while healthy")
			})

			t.Run("unhealthy mid-run stops choosing target", func(t *testing.T) {
				setHealthy(t, reg, target, false)
				chosen := selectNames(t, sel, 6)
				assert.NotContains(t, chosen, target,
					"target must not be chosen while unhealthy")
			})

			t.Run("recovered resumes choosing target", func(t *testing.T) {
				setHealthy(t, reg, target, true)
				chosen := selectNames(t, sel, 6)
				assert.Contains(t, chosen, target,
					"target must be chosen again after recovery")
			})
		})
	}
}
