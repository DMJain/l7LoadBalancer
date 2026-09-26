package backend

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/config"
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

// circuitAllow adapts Backend.CircuitAllow's (bool, CircuitTransition) pair to
// the plain admission decision the S3.T3 tests assert on, leaving their
// expectations unchanged while the transition value is covered separately.
func circuitAllow(b *Backend, cooldown time.Duration) bool {
	ok, _ := b.CircuitAllow(cooldown)
	return ok
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

func TestBackendRemovedFlag(t *testing.T) {
	b := &Backend{Name: "backend-a"}

	assert.False(t, b.IsRemoved(), "a zero-value backend is not removed")

	b.markRemoved()
	assert.True(t, b.IsRemoved())
}

// TestBackendRetiredContext proves the retired context is created at
// construction, is not cancelled by default, and is cancelled with
// ErrDrainWindowExpired by Retire. The backend is built through NewRegistry,
// the only production constructor, so its retired context is real.
func TestBackendRetiredContext(t *testing.T) {
	reg, err := NewRegistry([]config.BackendConfig{{Name: "backend-a", URL: "http://127.0.0.1:9001"}})
	require.NoError(t, err)
	b := reg.All()[0]

	require.NotNil(t, b.RetiredContext())
	assert.NoError(t, b.RetiredContext().Err(), "a fresh backend is not retired")

	b.Retire()
	assert.Error(t, b.RetiredContext().Err(), "Retire must cancel the retired context")
	assert.True(t, errors.Is(context.Cause(b.RetiredContext()), ErrDrainWindowExpired),
		"the cancellation must carry ErrDrainWindowExpired as its cause")

	b.Retire()
	assert.True(t, errors.Is(context.Cause(b.RetiredContext()), ErrDrainWindowExpired),
		"Retire is idempotent and must not change the cause")
}

// TestBackendZeroValueRetiredContext proves the retired-context accessors are
// total for a directly-constructed Backend: the accessor returns a usable
// context and Retire is a no-op rather than a nil-context panic.
func TestBackendZeroValueRetiredContext(t *testing.T) {
	b := &Backend{Name: "backend-a"}

	require.NotNil(t, b.RetiredContext())
	assert.NoError(t, b.RetiredContext().Err())

	require.NotPanics(t, b.Retire)
	assert.NoError(t, b.RetiredContext().Err(), "Retire on a backend with no retired context is a no-op")
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
		assert.True(t, circuitAllow(b, circuitTestCooldown), "a closed circuit must admit")
	}

	b.CircuitFailure(threshold)
	assert.True(t, b.CircuitOpen(circuitTestCooldown),
		"the %dth consecutive failure must open the circuit", threshold)
	assert.False(t, circuitAllow(b, circuitTestCooldown), "an open circuit must deny")
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
	assert.False(t, circuitAllow(b, cooldown), "an open circuit denies before cooldown elapses")

	time.Sleep(200 * time.Millisecond)

	assert.False(t, b.CircuitOpen(cooldown),
		"after the cooldown a read must lazily promote Open to Half-Open")
	assert.True(t, circuitAllow(b, cooldown), "a half-open circuit admits exactly one trial")
	assert.False(t, circuitAllow(b, cooldown),
		"once the trial slot is taken, a second admission must be denied")
}

func TestBackendCircuitTrialResolves(t *testing.T) {
	const cooldown = 50 * time.Millisecond

	t.Run("a successful trial closes the circuit", func(t *testing.T) {
		b := &Backend{Name: "backend-a"}
		openCircuit(t, b, 3)
		time.Sleep(200 * time.Millisecond)

		require.True(t, circuitAllow(b, cooldown), "half-open must admit the trial")
		b.CircuitSuccess()

		assert.False(t, b.CircuitOpen(cooldown), "a successful trial must close the circuit")
		assert.True(t, circuitAllow(b, cooldown), "a closed circuit admits every request")
		assert.True(t, circuitAllow(b, cooldown))
	})

	t.Run("a failed trial reopens the circuit", func(t *testing.T) {
		b := &Backend{Name: "backend-a"}
		openCircuit(t, b, 3)
		time.Sleep(200 * time.Millisecond)

		require.True(t, circuitAllow(b, cooldown), "half-open must admit the trial")
		b.CircuitFailure(3)

		assert.True(t, b.CircuitOpen(cooldown), "a failed trial must reopen the circuit")
		assert.False(t, circuitAllow(b, cooldown))
	})
}

// TestBackendRearmTrialLetsAnotherRequestTakeTheTrial pins the S4.T5 fix for the
// half-open wedge: a request that took the trial but ended without a backend
// outcome (a client cancellation) releases the slot via RearmTrial, so a later
// request may probe the backend instead of the circuit denying every future
// request forever.
func TestBackendRearmTrialLetsAnotherRequestTakeTheTrial(t *testing.T) {
	const cooldown = 50 * time.Millisecond
	b := &Backend{Name: "backend-a"}
	openCircuit(t, b, 3)
	time.Sleep(200 * time.Millisecond)

	require.False(t, b.CircuitOpen(cooldown), "after cooldown the circuit is half-open")
	require.True(t, circuitAllow(b, cooldown), "the first request takes the trial")
	require.False(t, circuitAllow(b, cooldown), "no second request may take the same trial")

	b.RearmTrial()

	assert.True(t, circuitAllow(b, cooldown),
		"after a client-gone trial is re-armed, a later request must be able to take it")
}

// TestBackendRearmTrialIsNoOpWhenNotHalfOpen proves the release is a no-op on a
// closed circuit and never panics on a zero-value backend, so the proxy can
// call it unconditionally on a client-gone request.
func TestBackendRearmTrialIsNoOpWhenNotHalfOpen(t *testing.T) {
	b := &Backend{Name: "backend-a"}
	assert.NotPanics(t, b.RearmTrial)
	assert.True(t, circuitAllow(b, time.Hour), "a closed circuit admits every request")
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
			if circuitAllow(b, cooldown) {
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
				_, _ = b.CircuitAllow(time.Millisecond)
			default:
				_ = b.CircuitOpen(time.Millisecond)
			}
		}(i)
	}
	wg.Wait()
}

// TestBackendCircuitFailureTransition pins which CircuitFailure call changed
// the circuit: only the threshold-crossing Closed→Open call reports Opened,
// and a failure observed while already Open changes nothing.
func TestBackendCircuitFailureTransition(t *testing.T) {
	const threshold = 3
	b := &Backend{Name: "backend-a"}

	assert.Equal(t, CircuitNoChange, b.CircuitFailure(threshold),
		"the first failure only counts; it does not open the circuit")
	assert.Equal(t, CircuitNoChange, b.CircuitFailure(threshold))
	assert.Equal(t, CircuitOpened, b.CircuitFailure(threshold),
		"the threshold-crossing failure is the one that opens the circuit")
	assert.Equal(t, CircuitNoChange, b.CircuitFailure(threshold),
		"a stale failure observed while already Open changes nothing")
}

// TestBackendCircuitSuccessTransition pins the success-side transition: a
// success in Closed (or while Open, which is ignored) is not a transition; a
// success in Half-Open closes the circuit and reports Closed.
func TestBackendCircuitSuccessTransition(t *testing.T) {
	const threshold = 3
	b := &Backend{Name: "backend-a"}

	assert.Equal(t, CircuitNoChange, b.CircuitSuccess(),
		"a success on an already-closed circuit changes nothing")

	for i := 0; i < threshold; i++ {
		b.CircuitFailure(threshold)
	}
	assert.Equal(t, CircuitNoChange, b.CircuitSuccess(),
		"a stale success observed while Open is ignored")
}

// TestBackendCircuitAllowTransition pins the admission-side transition: only
// the call that promotes Open→Half-Open and takes the trial reports
// HalfOpened; a plain Closed admission and a denied second admission report
// NoChange.
func TestBackendCircuitAllowTransition(t *testing.T) {
	const (
		cooldown  = 50 * time.Millisecond
		threshold = 3
	)
	b := &Backend{Name: "backend-a"}

	admitted, transition := b.CircuitAllow(circuitTestCooldown)
	assert.True(t, admitted, "a closed circuit admits")
	assert.Equal(t, CircuitNoChange, transition,
		"admitting from a Closed circuit is not a state transition")

	for i := 0; i < threshold; i++ {
		b.CircuitFailure(threshold)
	}
	time.Sleep(200 * time.Millisecond)

	admitted, transition = b.CircuitAllow(cooldown)
	assert.True(t, admitted, "a half-open circuit admits exactly one trial")
	assert.Equal(t, CircuitHalfOpened, transition,
		"the call that promotes Open→Half-Open and takes the trial reports HalfOpened")

	admitted, transition = b.CircuitAllow(cooldown)
	assert.False(t, admitted, "the trial slot is already taken")
	assert.Equal(t, CircuitNoChange, transition,
		"a denied admission changes nothing")
}

// TestBackendCircuitTrialResolutionTransitions pins the two ways a half-open
// trial resolves: success reports Closed, failure reports Reopened (distinct
// from Closed→Open's Opened, so the breaker can log trial_failure).
func TestBackendCircuitTrialResolutionTransitions(t *testing.T) {
	const cooldown = 50 * time.Millisecond

	t.Run("a successful trial reports Closed", func(t *testing.T) {
		b := &Backend{Name: "backend-a"}
		openCircuit(t, b, 3)
		time.Sleep(200 * time.Millisecond)

		require.True(t, circuitAllow(b, cooldown), "half-open must admit the trial")
		assert.Equal(t, CircuitClosed, b.CircuitSuccess())
	})

	t.Run("a failed trial reports Reopened", func(t *testing.T) {
		b := &Backend{Name: "backend-a"}
		openCircuit(t, b, 3)
		time.Sleep(200 * time.Millisecond)

		require.True(t, circuitAllow(b, cooldown), "half-open must admit the trial")
		assert.Equal(t, CircuitReopened, b.CircuitFailure(3),
			"a half-open trial failure reopens the circuit, distinct from Closed→Open")
	})
}

// TestBackendCircuitAllowDoesNotReportScanWonPromotion pins the documented,
// permanent logging gap: when a Registry.Selectable() scan (Backend.CircuitOpen)
// promotes Open→Half-Open, the later trial admission did not itself perform the
// promotion, so it reports NoChange — the promotion has no logger path.
func TestBackendCircuitAllowDoesNotReportScanWonPromotion(t *testing.T) {
	const cooldown = 50 * time.Millisecond
	b := &Backend{Name: "backend-a"}
	openCircuit(t, b, 3)
	time.Sleep(200 * time.Millisecond)

	require.False(t, b.CircuitOpen(cooldown),
		"a selection scan reads the circuit and promotes Open→Half-Open first")

	admitted, transition := b.CircuitAllow(cooldown)
	assert.True(t, admitted, "the promoted circuit still admits the trial")
	assert.Equal(t, CircuitNoChange, transition,
		"this call did not promote, so there is no transition for it to report")
}

// TestBackendCircuitAllowReportsPromotionOnceConcurrently pins the exactly-once
// property under a race: the promotion CAS has one winner, so exactly one of
// many concurrent admissions reports HalfOpened — even though only one of them
// also wins the trial slot.
func TestBackendCircuitAllowReportsPromotionOnceConcurrently(t *testing.T) {
	const (
		cooldown   = 50 * time.Millisecond
		goroutines = 50
	)
	b := &Backend{Name: "backend-a"}
	openCircuit(t, b, 3)
	time.Sleep(200 * time.Millisecond)

	var halfOpened atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, transition := b.CircuitAllow(cooldown); transition == CircuitHalfOpened {
				halfOpened.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.Equal(t, int32(1), halfOpened.Load(),
		"exactly one caller may report the Open→Half-Open promotion")
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
