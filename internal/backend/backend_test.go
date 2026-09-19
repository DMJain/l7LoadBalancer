package backend

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
