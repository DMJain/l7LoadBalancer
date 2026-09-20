package backend

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// circuitTestCooldown is long enough that an opened circuit never promotes to
// half-open within a test, so "is it open" assertions are not racing a clock.
const circuitTestCooldown = time.Hour

// openCircuit drives threshold consecutive failures, which the setup asserts
// is enough to open the circuit, and returns the now-open backend.
func openCircuit(t *testing.T, b *Backend, threshold int) {
	t.Helper()
	for i := 0; i < threshold; i++ {
		b.CircuitFailure(threshold)
	}
	require.True(t, b.CircuitOpen(circuitTestCooldown), "setup: circuit must be open")
}

func TestBackendHealthyState(t *testing.T) {
	b := &Backend{Name: "backend-a"}

	assert.False(t, b.IsHealthy(), "zero value is not healthy; only NewRegistry/health checks set it")

	b.MarkHealthy()
	assert.True(t, b.IsHealthy())

	b.MarkUnhealthy()
	assert.False(t, b.IsHealthy())

	b.MarkHealthy()
	assert.True(t, b.IsHealthy(), "MarkHealthy must be able to recover a backend marked unhealthy")
}

func TestBackendActiveConns(t *testing.T) {
	b := &Backend{Name: "backend-a"}
	assert.Equal(t, int64(0), b.ActiveConns())

	b.IncActive()
	b.IncActive()
	assert.Equal(t, int64(2), b.ActiveConns())

	b.DecActive()
	assert.Equal(t, int64(1), b.ActiveConns())
}

func TestBackendConcurrentActiveConns(t *testing.T) {
	const goroutines = 100
	b := &Backend{Name: "backend-a"}

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.IncActive()
		}()
	}
	wg.Wait()
	require.Equal(t, int64(goroutines), b.ActiveConns())

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.IncActive()
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.DecActive()
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(goroutines), b.ActiveConns(), "interleaved inc/dec must net out")
}

func TestBackendLatencyRecordAndEWMA(t *testing.T) {
	b := &Backend{Name: "backend-a"}

	assert.Zero(t, b.EWMALatency(), "a never-recorded backend reads zero")

	b.RecordLatency(100 * time.Millisecond)
	assert.Equal(t, 100*time.Millisecond, b.EWMALatency(),
		"the first sample must be stored directly, not blended from a zero baseline")

	b.RecordLatency(200 * time.Millisecond)
	assert.InDelta(t, 110*time.Millisecond, b.EWMALatency(), float64(time.Millisecond),
		"second sample blends α·observed + (1-α)·previous with α=0.1")

	b.RecordLatency(0)
	assert.InDelta(t, 99*time.Millisecond, b.EWMALatency(), float64(time.Millisecond),
		"a zero observation blends toward zero rather than being treated as cold again")
}

func TestBackendConcurrentRecordLatency(t *testing.T) {
	const goroutines = 100
	b := &Backend{Name: "backend-a"}

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.RecordLatency(time.Millisecond)
			_ = b.EWMALatency()
		}()
	}
	wg.Wait()

	assert.InDelta(t, time.Millisecond, b.EWMALatency(), float64(time.Microsecond),
		"recording the same sample concurrently must converge to that value, race-free")
}

func TestBackendCircuitOpensAfterConsecutiveFailures(t *testing.T) {
	const threshold = 3
	b := &Backend{Name: "backend-a"}

	for i := 1; i < threshold; i++ {
		b.CircuitFailure(threshold)
		assert.False(t, b.CircuitOpen(circuitTestCooldown),
			"after %d consecutive failures the circuit must stay closed", i)
		assert.True(t, b.CircuitAllow(circuitTestCooldown), "a closed circuit must admit")
	}

	b.CircuitFailure(threshold)
	assert.True(t, b.CircuitOpen(circuitTestCooldown),
		"the %dth consecutive failure must open the circuit", threshold)
	assert.False(t, b.CircuitAllow(circuitTestCooldown), "an open circuit must deny")
}

func TestBackendCircuitSuccessResetsConsecutiveFailures(t *testing.T) {
	const threshold = 3
	b := &Backend{Name: "backend-a"}

	b.CircuitFailure(threshold)
	b.CircuitFailure(threshold)
	b.CircuitSuccess()
	b.CircuitFailure(threshold)
	b.CircuitFailure(threshold)
	assert.False(t, b.CircuitOpen(circuitTestCooldown),
		"a success resets the consecutive count, so two more failures must not open")

	b.CircuitFailure(threshold)
	assert.True(t, b.CircuitOpen(circuitTestCooldown))
}

func TestBackendCircuitLazyHalfOpenAfterCooldown(t *testing.T) {
	const cooldown = 50 * time.Millisecond
	b := &Backend{Name: "backend-a"}
	openCircuit(t, b, 3)

	assert.True(t, b.CircuitOpen(cooldown), "immediately after opening the circuit is still open")
	assert.False(t, b.CircuitAllow(cooldown), "an open circuit denies before cooldown elapses")

	time.Sleep(200 * time.Millisecond)

	assert.False(t, b.CircuitOpen(cooldown),
		"after the cooldown a read must lazily promote Open to Half-Open")
	assert.True(t, b.CircuitAllow(cooldown), "a half-open circuit admits exactly one trial")
	assert.False(t, b.CircuitAllow(cooldown),
		"once the trial slot is taken, a second admission must be denied")
}

func TestBackendCircuitTrialResolves(t *testing.T) {
	const cooldown = 50 * time.Millisecond

	t.Run("a successful trial closes the circuit", func(t *testing.T) {
		b := &Backend{Name: "backend-a"}
		openCircuit(t, b, 3)
		time.Sleep(200 * time.Millisecond)

		require.True(t, b.CircuitAllow(cooldown), "half-open must admit the trial")
		b.CircuitSuccess()

		assert.False(t, b.CircuitOpen(cooldown), "a successful trial must close the circuit")
		assert.True(t, b.CircuitAllow(cooldown), "a closed circuit admits every request")
		assert.True(t, b.CircuitAllow(cooldown))
	})

	t.Run("a failed trial reopens the circuit", func(t *testing.T) {
		b := &Backend{Name: "backend-a"}
		openCircuit(t, b, 3)
		time.Sleep(200 * time.Millisecond)

		require.True(t, b.CircuitAllow(cooldown), "half-open must admit the trial")
		b.CircuitFailure(3)

		assert.True(t, b.CircuitOpen(cooldown), "a failed trial must reopen the circuit")
		assert.False(t, b.CircuitAllow(cooldown))
	})
}

func TestBackendCircuitAdmitsExactlyOneConcurrentTrial(t *testing.T) {
	const (
		cooldown   = 50 * time.Millisecond
		goroutines = 50
	)
	b := &Backend{Name: "backend-a"}
	openCircuit(t, b, 3)
	time.Sleep(200 * time.Millisecond)

	var admitted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if b.CircuitAllow(cooldown) {
				admitted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.Equal(t, int32(1), admitted.Load(),
		"exactly one request may win the half-open trial slot")
}

func TestBackendCircuitIgnoresOutcomesWhileOpen(t *testing.T) {
	const threshold = 3
	b := &Backend{Name: "backend-a"}
	openCircuit(t, b, threshold)

	b.CircuitSuccess()
	assert.True(t, b.CircuitOpen(circuitTestCooldown),
		"a stale in-flight success must not close an open circuit and bypass the cooldown")

	b.CircuitFailure(threshold)
	assert.True(t, b.CircuitOpen(circuitTestCooldown),
		"a stale in-flight failure must leave an already-open circuit open")
}

func TestBackendCircuitConcurrentTransitions(t *testing.T) {
	b := &Backend{Name: "backend-a"}
	b.CircuitSuccess()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 4 {
			case 0:
				b.CircuitFailure(3)
			case 1:
				b.CircuitSuccess()
			case 2:
				_ = b.CircuitAllow(time.Millisecond)
			default:
				_ = b.CircuitOpen(time.Millisecond)
			}
		}(i)
	}
	wg.Wait()
}

func TestBackendConcurrentHealthToggling(t *testing.T) {
	const goroutines = 100
	b := &Backend{Name: "backend-a"}
	b.MarkHealthy()

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				b.MarkHealthy()
			} else {
				b.MarkUnhealthy()
			}
			_ = b.IsHealthy()
		}(i)
	}
	wg.Wait()
}
