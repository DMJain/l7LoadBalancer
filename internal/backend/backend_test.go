package backend

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackendHealthyState(t *testing.T) {
	b := &Backend{Name: "backend-a"}

	assert.False(t, b.IsHealthy(), "zero value is not healthy; only NewRegistry/health checks set it")

	b.SetHealthy(true)
	assert.True(t, b.IsHealthy())

	b.SetHealthy(false)
	assert.False(t, b.IsHealthy())
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
			b.DecActive()
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(0), b.ActiveConns())
}

func TestBackendConcurrentHealthToggling(t *testing.T) {
	const goroutines = 100
	b := &Backend{Name: "backend-a"}
	b.SetHealthy(true)

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b.SetHealthy(i%2 == 0)
			_ = b.IsHealthy()
		}(i)
	}
	wg.Wait()
}
