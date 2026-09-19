package balancer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// This file is the cross-cutting counterpart to the per-selector test files.
// Rather than re-testing each algorithm's distribution, it proves the Sprint 1
// selectors are interchangeable with respect to health transitions:
// health-awareness must be a property of the Selector contract, not something
// one implementation happens to get right. It also hosts the address-aware
// selection helpers shared by every selector test, so a request is always
// built through one path rather than each file re-rolling httptest setup.
//
// The per-implementation compile-time assertions
// (`var _ Selector = (*RoundRobin)(nil)` / `(*LeastConnections)(nil)`) live
// next to each type in roundrobin.go and leastconn.go and are re-checked on
// every build, so they are deliberately not duplicated here.

// requestForAddr builds the request every address-aware selector test uses:
// a GET / whose RemoteAddr is remoteAddr. One construction path, whether the
// caller has a *testing.T or not.
func requestForAddr(remoteAddr string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remoteAddr
	return r
}

// selectAddr selects through s for a request whose client address is
// remoteAddr. It is selectForAddr without the *testing.T, for helpers (like
// the hot-key stream runner) that exercise many requests.
func selectAddr(s Selector, remoteAddr string) (*backend.Backend, error) {
	return s.Select(context.Background(), requestForAddr(remoteAddr))
}

// selectForAddr selects through s for a request whose client address is
// remoteAddr, returning the backend and error unmodified so error-path tests
// can assert both.
func selectForAddr(t *testing.T, s Selector, remoteAddr string) (*backend.Backend, error) {
	t.Helper()
	return selectAddr(s, remoteAddr)
}

// selectNameForAddr is selectForAddr for the happy path: it fails the test
// immediately if selection errors or returns no backend.
func selectNameForAddr(t *testing.T, s Selector, remoteAddr string) string {
	t.Helper()
	b, err := selectForAddr(t, s, remoteAddr)
	require.NoError(t, err)
	require.NotNil(t, b)
	return b.Name
}

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

			// Each phase sets up the health state it needs, so any one of
			// them can be run in isolation (e.g. `go test -run .../unhealthy`).
			t.Run("healthy baseline chooses target", func(t *testing.T) {
				chosen := selectNames(t, sel, 6)
				assert.Contains(t, chosen, target,
					"target must be selectable while healthy")
			})

			t.Run("unhealthy mid-run stops choosing target", func(t *testing.T) {
				markUnhealthy(t, reg, target)
				chosen := selectNames(t, sel, 6)
				assert.NotEqual(t, target, chosen[0],
					"target must not be chosen on the very next Select after going unhealthy")
				assert.NotContains(t, chosen, target,
					"target must not be chosen while unhealthy")
			})

			t.Run("recovered resumes choosing target", func(t *testing.T) {
				markHealthy(t, reg, target)
				chosen := selectNames(t, sel, 6)
				assert.Contains(t, chosen, target,
					"target must be chosen again after recovery")
			})
		})
	}
}
