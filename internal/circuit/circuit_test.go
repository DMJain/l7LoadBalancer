package circuit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/config"
)

// Breaker must satisfy the gate interface backend defines and consumes, so a
// registry can be handed one without backend importing circuit (ADR-0012).
var _ backend.CircuitGate = (*Breaker)(nil)

// testRegistry builds a two-backend registry over unresolvable-by-design URLs;
// no request is ever dispatched in these tests, the breaker state is driven
// directly.
func testRegistry(t *testing.T) *backend.Registry {
	t.Helper()
	reg, err := backend.NewRegistry([]config.BackendConfig{
		{Name: "backend-a", URL: "http://127.0.0.1:9001"},
		{Name: "backend-b", URL: "http://127.0.0.1:9002"},
	})
	require.NoError(t, err)
	return reg
}

// failUntilOpen drives circuitFailuresBeforeOpen consecutive failures and
// asserts the circuit is now open under the breaker's own cooldown.
func failUntilOpen(t *testing.T, br *Breaker, b *backend.Backend) {
	t.Helper()
	for i := 0; i < circuitFailuresBeforeOpen; i++ {
		br.ObserveRoundTrip(b, 0, false)
	}
	require.False(t, br.Allow(b), "setup: circuit must be open")
}

func TestBreakerOpensAfterConsecutiveFailures(t *testing.T) {
	reg := testRegistry(t)
	br := New(time.Hour)
	b := reg.All()[0]

	for i := 1; i < circuitFailuresBeforeOpen; i++ {
		br.ObserveRoundTrip(b, 0, false)
		assert.True(t, br.Allow(b),
			"after %d consecutive failures the circuit must stay closed", i)
	}

	br.ObserveRoundTrip(b, 0, false)
	assert.False(t, br.Allow(b),
		"the %dth consecutive failure must open the circuit", circuitFailuresBeforeOpen)
}

func TestBreakerSuccessResetsConsecutiveFailures(t *testing.T) {
	reg := testRegistry(t)
	br := New(time.Hour)
	b := reg.All()[0]

	br.ObserveRoundTrip(b, 0, false)
	br.ObserveRoundTrip(b, 0, false)
	br.ObserveRoundTrip(b, 0, true) // any success resets the consecutive count
	br.ObserveRoundTrip(b, 0, false)
	br.ObserveRoundTrip(b, 0, false)

	assert.True(t, br.Allow(b),
		"the success reset the count, so two more failures must not open the circuit")

	br.ObserveRoundTrip(b, 0, false)
	assert.False(t, br.Allow(b))
}

func TestBreakerIgnoresOutcomesWhileOpen(t *testing.T) {
	reg := testRegistry(t)
	br := New(time.Hour)
	b := reg.All()[0]
	failUntilOpen(t, br, b)

	br.ObserveRoundTrip(b, 0, true)
	assert.False(t, br.Allow(b),
		"a stale in-flight success must not close an open circuit and bypass the cooldown")

	br.ObserveRoundTrip(b, 0, false)
	assert.False(t, br.Allow(b),
		"a stale in-flight failure must leave an already-open circuit open")
}

// TestRegistrySelectableExcludesOpenIncludesHalfOpen exercises the promotion
// path through Registry.Selectable itself: an open circuit is excluded, and
// the same read that advances past the cooldown promotes it to half-open, which
// stays selectable (ADR-0011 decisions 1 and 6).
func TestRegistrySelectableExcludesOpenIncludesHalfOpen(t *testing.T) {
	const cooldown = 50 * time.Millisecond
	reg := testRegistry(t)
	br := New(cooldown)
	reg.SetCircuitGate(br)

	b := reg.All()[0]
	for i := 0; i < circuitFailuresBeforeOpen; i++ {
		br.ObserveRoundTrip(b, 0, false)
	}

	assert.NotContains(t, reg.Selectable(), b,
		"an open-circuit backend must be excluded from selection")

	time.Sleep(200 * time.Millisecond)

	assert.Contains(t, reg.Selectable(), b,
		"after the cooldown a selection read promotes Open to Half-Open, which stays selectable")
	assert.False(t, br.Open(b), "a promoted circuit is half-open, not open")
}

func TestBreakerHalfOpenAdmitsExactlyOneConcurrentTrial(t *testing.T) {
	const cooldown = 50 * time.Millisecond
	reg := testRegistry(t)
	br := New(cooldown)
	b := reg.All()[0]
	for i := 0; i < circuitFailuresBeforeOpen; i++ {
		br.ObserveRoundTrip(b, 0, false)
	}
	time.Sleep(200 * time.Millisecond)

	const goroutines = 50
	var admitted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if br.Allow(b) {
				admitted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.Equal(t, int32(1), admitted.Load(),
		"exactly one concurrent request may be admitted as the half-open trial")
}

func TestBreakerTrialResolves(t *testing.T) {
	const cooldown = 50 * time.Millisecond

	t.Run("a successful trial closes the circuit", func(t *testing.T) {
		reg := testRegistry(t)
		br := New(cooldown)
		b := reg.All()[0]
		for i := 0; i < circuitFailuresBeforeOpen; i++ {
			br.ObserveRoundTrip(b, 0, false)
		}
		time.Sleep(200 * time.Millisecond)

		require.True(t, br.Allow(b), "half-open must admit the trial")
		br.ObserveRoundTrip(b, 0, true)

		assert.True(t, br.Allow(b),
			"a single successful trial must close the circuit, which then admits all requests")
	})

	t.Run("a failed trial reopens the circuit", func(t *testing.T) {
		reg := testRegistry(t)
		br := New(cooldown)
		b := reg.All()[0]
		for i := 0; i < circuitFailuresBeforeOpen; i++ {
			br.ObserveRoundTrip(b, 0, false)
		}
		time.Sleep(200 * time.Millisecond)

		require.True(t, br.Allow(b), "half-open must admit the trial")
		br.ObserveRoundTrip(b, 0, false)

		assert.False(t, br.Allow(b),
			"a single failed trial must reopen the circuit, with no inner threshold")
	})
}

func TestBreakerOpenReportsStateWithoutTakingTheTrial(t *testing.T) {
	reg := testRegistry(t)
	br := New(50 * time.Millisecond)
	b := reg.All()[0]

	assert.False(t, br.Open(b), "a fresh backend's circuit starts closed")

	for i := 0; i < circuitFailuresBeforeOpen; i++ {
		br.ObserveRoundTrip(b, 0, false)
	}
	time.Sleep(200 * time.Millisecond)

	assert.False(t, br.Open(b), "a cooled-down circuit reads as half-open, not open")
	assert.True(t, br.Allow(b), "Open must not have consumed the trial slot")
	assert.False(t, br.Allow(b), "the trial slot is still available exactly once")
}
