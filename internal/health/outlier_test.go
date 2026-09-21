package health

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// outlierBackends builds n real registry-backed Backends. Their URLs are never
// contacted by the detector — it works purely from ObserveRoundTrip outcomes —
// so any syntactically valid URL will do.
func outlierBackends(t *testing.T, n int) []*backend.Backend {
	t.Helper()
	urls := make([]string, n)
	for i := range urls {
		urls[i] = "http://127.0.0.1:1"
	}
	return newTestRegistry(t, urls...).All()
}

// ejectRecorder counts ejections per backend through the detector's unexported
// eject seam. Backend.MarkUnhealthy is idempotent (an atomic store), so its
// invocation count is otherwise unobservable — the seam is what makes the
// ticket's "called exactly once, not once per failure" assertion checkable.
type ejectRecorder struct {
	mu     sync.Mutex
	counts map[*backend.Backend]int
}

func newEjectRecorder() *ejectRecorder {
	return &ejectRecorder{counts: make(map[*backend.Backend]int)}
}

func (r *ejectRecorder) eject(b *backend.Backend) {
	r.mu.Lock()
	r.counts[b]++
	r.mu.Unlock()
	b.MarkUnhealthy()
}

func (r *ejectRecorder) count(b *backend.Backend) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[b]
}

func newRecordingDetector() (*OutlierDetector, *ejectRecorder) {
	d := NewOutlierDetector(discardLogger())
	rec := newEjectRecorder()
	d.eject = rec.eject
	return d, rec
}

// TestOutlierDetectorEjectsAfterFailuresWithinWindow pins the threshold
// boundary: fewer than N failures leave the backend healthy; the Nth ejects it,
// calling MarkUnhealthy exactly once.
func TestOutlierDetectorEjectsAfterFailuresWithinWindow(t *testing.T) {
	b := outlierBackends(t, 1)[0]
	d, rec := newRecordingDetector()

	require.True(t, b.IsHealthy(), "NewRegistry starts every backend healthy")
	for i := 0; i < outlierFailuresBeforeEject-1; i++ {
		d.ObserveRoundTrip(b, time.Millisecond, false)
	}
	require.True(t, b.IsHealthy(),
		"fewer than %d failures within the window must not eject", outlierFailuresBeforeEject)
	require.Zero(t, rec.count(b))

	d.ObserveRoundTrip(b, time.Millisecond, false)
	assert.False(t, b.IsHealthy(),
		"%d failures within the window must eject the backend", outlierFailuresBeforeEject)
	assert.Equal(t, 1, rec.count(b))
}

// TestOutlierDetectorEjectsWithMixedOutcomes proves the window is a sliding
// count, not a consecutive streak: successes interspersed among the failures do
// not reset ejection progress, matching the "tolerant and slow" posture
// ADR-0011 decision 8 assigns to passive detection.
func TestOutlierDetectorEjectsWithMixedOutcomes(t *testing.T) {
	b := outlierBackends(t, 1)[0]
	d, rec := newRecordingDetector()

	// Alternate failure/success, ending on a failure, so the window holds N
	// failures interspersed with successes — never a consecutive run of N.
	for i := 0; i < 2*outlierFailuresBeforeEject-1; i++ {
		d.ObserveRoundTrip(b, time.Millisecond, i%2 == 1)
	}
	assert.False(t, b.IsHealthy(),
		"failures interleaved with successes must still eject once the count reaches the threshold")
	assert.Equal(t, 1, rec.count(b))
}

// TestOutlierDetectorDoesNotEjectBelowThreshold covers the quiet-badly-behaving
// case: some failures, but never enough within the window.
func TestOutlierDetectorDoesNotEjectBelowThreshold(t *testing.T) {
	b := outlierBackends(t, 1)[0]
	d, rec := newRecordingDetector()

	for i := 0; i < outlierFailuresBeforeEject-1; i++ {
		d.ObserveRoundTrip(b, time.Millisecond, false)
	}
	for i := 0; i < outlierWindowSize; i++ {
		d.ObserveRoundTrip(b, time.Millisecond, true)
	}

	assert.True(t, b.IsHealthy())
	assert.Zero(t, rec.count(b))
}

// TestOutlierDetectorFailuresFallOutOfWindow proves the window is bounded: once
// enough later outcomes push an old failure burst past the window edge, it no
// longer contributes to the count.
func TestOutlierDetectorFailuresFallOutOfWindow(t *testing.T) {
	b := outlierBackends(t, 1)[0]
	d, rec := newRecordingDetector()

	for i := 0; i < outlierFailuresBeforeEject-1; i++ {
		d.ObserveRoundTrip(b, time.Millisecond, false)
	}
	for i := 0; i < outlierWindowSize; i++ {
		d.ObserveRoundTrip(b, time.Millisecond, true)
	}
	for i := 0; i < outlierFailuresBeforeEject-1; i++ {
		d.ObserveRoundTrip(b, time.Millisecond, false)
	}

	assert.True(t, b.IsHealthy(),
		"failures pushed out of the window must not count toward a later ejection")
	assert.Zero(t, rec.count(b))
}

// TestOutlierDetectorEjectsExactlyOncePerEpisode proves ejection fires at the
// threshold crossing only — further failures while the backend is already
// ejected do not re-invoke MarkUnhealthy — and that a backend reinstated by an
// active probe (the only recovery path, ADR-0011 decision 3) begins a fresh
// episode needing its own full threshold.
func TestOutlierDetectorEjectsExactlyOncePerEpisode(t *testing.T) {
	b := outlierBackends(t, 1)[0]
	d, rec := newRecordingDetector()

	for i := 0; i < outlierFailuresBeforeEject; i++ {
		d.ObserveRoundTrip(b, time.Millisecond, false)
	}
	require.False(t, b.IsHealthy())
	require.Equal(t, 1, rec.count(b))

	for i := 0; i < outlierWindowSize*3; i++ {
		d.ObserveRoundTrip(b, time.Millisecond, false)
	}
	assert.Equal(t, 1, rec.count(b),
		"MarkUnhealthy must be called once per ejection episode, not once per failure")

	// Recovery is the active probe's job; the detector only observes it.
	b.MarkHealthy()
	for i := 0; i < outlierFailuresBeforeEject-1; i++ {
		d.ObserveRoundTrip(b, time.Millisecond, false)
	}
	assert.Equal(t, 1, rec.count(b), "a fresh episode needs its own full threshold")
	require.True(t, b.IsHealthy())

	d.ObserveRoundTrip(b, time.Millisecond, false)
	assert.Equal(t, 2, rec.count(b))
	assert.False(t, b.IsHealthy())
}

// TestOutlierDetectorDefaultEjectsViaMarkUnhealthy exercises the production
// wiring rather than the counting seam: an unmodified detector — whose eject
// function is the real (*backend.Backend).MarkUnhealthy assigned in
// NewOutlierDetector — must still transition the backend's health on threshold
// breach.
func TestOutlierDetectorDefaultEjectsViaMarkUnhealthy(t *testing.T) {
	b := outlierBackends(t, 1)[0]
	d := NewOutlierDetector(discardLogger())

	require.True(t, b.IsHealthy())
	for i := 0; i < outlierFailuresBeforeEject; i++ {
		d.ObserveRoundTrip(b, time.Millisecond, false)
	}
	assert.False(t, b.IsHealthy(),
		"the production default must eject through Backend.MarkUnhealthy")
}

// TestOutlierDetectorConcurrentObserveEjectsEachBackendOnce drives the detector
// from many goroutines per backend under -race: the lock-free-looking contract
// must still eject each backend exactly once, not once per racing observer.
func TestOutlierDetectorConcurrentObserveEjectsEachBackendOnce(t *testing.T) {
	backends := outlierBackends(t, 4)
	d, rec := newRecordingDetector()

	var wg sync.WaitGroup
	for _, b := range backends {
		for i := 0; i < 200; i++ {
			wg.Add(1)
			go func(b *backend.Backend) {
				defer wg.Done()
				d.ObserveRoundTrip(b, time.Millisecond, false)
			}(b)
		}
	}
	wg.Wait()

	for _, b := range backends {
		assert.False(t, b.IsHealthy())
		assert.Equal(t, 1, rec.count(b),
			"concurrent observations must still eject each backend exactly once")
	}
}
