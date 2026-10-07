package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// nthSelector always returns backend n of the registry's current set.
type nthSelector struct {
	reg *backend.Registry
	n   int
}

func (s nthSelector) Select(context.Context, *http.Request) (*backend.Backend, error) {
	return s.reg.All()[s.n], nil
}

// TestSetSelectorRoutesNextRequest proves SetSelector changes which selector
// the next request asks (ADR-0015 decision 18).
func TestSetSelectorRoutesNextRequest(t *testing.T) {
	reg := registryFrom(t,
		backendEntry{"backend-a", startBackend(t, "backend-a").URL},
		backendEntry{"backend-b", startBackend(t, "backend-b").URL},
	)
	p := New(reg, nthSelector{reg: reg, n: 0})

	get := func() string {
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		return rec.Body.String()
	}
	assert.Contains(t, get(), "backend-a")

	p.SetSelector(nthSelector{reg: reg, n: 1})
	assert.Contains(t, get(), "backend-b")
}

// TestSetSelectorConcurrentWithRequests swaps the selector repeatedly while
// requests are served. Run under -race it proves the swap is a safe publish,
// and every request still succeeds (ADR-0015 decision 19).
func TestSetSelectorConcurrentWithRequests(t *testing.T) {
	reg := registryFrom(t,
		backendEntry{"backend-a", startBackend(t, "backend-a").URL},
		backendEntry{"backend-b", startBackend(t, "backend-b").URL},
	)
	p := New(reg, nthSelector{reg: reg, n: 0})

	stop := make(chan struct{})
	var failures atomic.Int64
	var served atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				rec := httptest.NewRecorder()
				p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
				if rec.Code != http.StatusOK {
					failures.Add(1)
				}
				served.Add(1)
			}
		}()
	}
	// Keep swapping until enough requests have been served alongside the
	// swaps, so the two genuinely overlap.
	for i := 0; served.Load() < 2000; i++ {
		p.SetSelector(nthSelector{reg: reg, n: i % 2})
	}
	close(stop)
	wg.Wait()

	assert.Zero(t, failures.Load())
	assert.Positive(t, served.Load())
}
