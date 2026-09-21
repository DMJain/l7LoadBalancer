package proxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/balancer"
	"github.com/DMJain/l7LoadBalancer/internal/circuit"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// labeledSeries returns the gathered dto.Metric whose label set exactly matches
// want for the named metric family, or nil if no such series has been written.
// The collector's private registry is the external, observable surface the
// spec's whole-request-hook seam asserts against (spec Testing Decisions §3).
//
// It reads the typed dto model via Registry().Gather rather than
// prometheus/testutil's CollectAndCompare helpers: those compare whole
// exposition families as text, and no typed "select this series by label set"
// helper exists. Gathering dto avoids scraped-text matching while letting the
// table assert one series at a time.
func labeledSeries(t *testing.T, c *metrics.Collector, name string, want map[string]string) *dto.Metric {
	t.Helper()
	mfs, err := c.Registry().Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelsEqual(m.GetLabel(), want) {
				return m
			}
		}
	}
	return nil
}

func labelsEqual(pairs []*dto.LabelPair, want map[string]string) bool {
	if len(pairs) != len(want) {
		return false
	}
	for _, p := range pairs {
		v, ok := want[p.GetName()]
		if !ok || v != p.GetValue() {
			return false
		}
	}
	return true
}

// TestProxyObservesWholeRequestOnEveryExitPath proves the whole-request counter
// and duration histogram fire exactly once on each of ServeHTTP's four exit
// paths, with the label set each path requires — real backend, real HTTP
// method, and status class derived from the response — and specifically that
// the no-healthy-backend 503 carries backend="" while a circuit-denied 503
// carries the real backend Select already chose.
func TestProxyObservesWholeRequestOnEveryExitPath(t *testing.T) {
	tests := []struct {
		name string
		// setup builds the proxy under test (with a fresh collector installed)
		// and returns it alongside the collector to assert against.
		setup func(t *testing.T) (*Proxy, *metrics.Collector)
		// method and path are the request issued to the proxy.
		method string
		path   string
		// wantStatus is the HTTP status ServeHTTP must answer with.
		wantStatus int
		// wantLabels is the exact expected label set on both the counter and
		// the histogram.
		wantLabels map[string]string
	}{
		{
			name: "successful 2xx response",
			setup: func(t *testing.T) (*Proxy, *metrics.Collector) {
				serving := startBackend(t, "backend-a")
				reg := registryFrom(t, backendEntry{"backend-a", serving.URL})
				p := New(reg, balancer.NewRoundRobin(reg))
				c := metrics.NewCollector()
				p.SetMetrics(c)
				return p, c
			},
			method:     http.MethodPost,
			path:       "/ok",
			wantStatus: http.StatusOK,
			wantLabels: map[string]string{"backend": "backend-a", "method": "POST", "status_class": "2xx"},
		},
		{
			name: "no healthy backend 503",
			setup: func(t *testing.T) (*Proxy, *metrics.Collector) {
				reg := registryFrom(t, backendEntry{"backend-a", "http://127.0.0.1:1"})
				reg.All()[0].MarkUnhealthy()
				p := New(reg, balancer.NewRoundRobin(reg))
				c := metrics.NewCollector()
				p.SetMetrics(c)
				return p, c
			},
			method:     http.MethodGet,
			path:       "/none",
			wantStatus: http.StatusServiceUnavailable,
			wantLabels: map[string]string{"backend": "", "method": "GET", "status_class": "5xx"},
		},
		{
			name: "circuit-denied 503",
			setup: func(t *testing.T) (*Proxy, *metrics.Collector) {
				reg := registryFrom(t, backendEntry{"backend-a", "http://127.0.0.1:1"})
				b := reg.All()[0]
				br := circuit.New(time.Minute, slog.Default(), metrics.NewCollector())
				openCircuit(t, reg, br, b)
				p := New(reg, fixedSelector{b: b})
				c := metrics.NewCollector()
				p.SetMetrics(c)
				return p, c
			},
			method:     http.MethodGet,
			path:       "/denied",
			wantStatus: http.StatusServiceUnavailable,
			// The real backend label, distinct from the no-healthy case above:
			// Select identified "backend-a" before Allow denied the dispatch.
			wantLabels: map[string]string{"backend": "backend-a", "method": "GET", "status_class": "5xx"},
		},
		{
			name: "backend error via ErrorHandler 502",
			setup: func(t *testing.T) (*Proxy, *metrics.Collector) {
				reg := registryFrom(t, backendEntry{"backend-a", deadBackendURL(t)})
				p := New(reg, balancer.NewRoundRobin(reg))
				c := metrics.NewCollector()
				p.SetMetrics(c)
				return p, c
			},
			method:     http.MethodGet,
			path:       "/dead",
			wantStatus: http.StatusBadGateway,
			wantLabels: map[string]string{"backend": "backend-a", "method": "GET", "status_class": "5xx"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, c := tt.setup(t)

			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			require.Equal(t, tt.wantStatus, rec.Code)

			counter := labeledSeries(t, c, "lb_requests_total", tt.wantLabels)
			require.NotNil(t, counter, "the request counter must have a series for the expected labels")
			assert.Equal(t, 1.0, counter.GetCounter().GetValue(),
				"exactly one request was served, so the counter must be 1")

			hist := labeledSeries(t, c, "lb_request_duration_seconds", tt.wantLabels)
			require.NotNil(t, hist, "the duration histogram must have a series for the expected labels")
			assert.Equal(t, uint64(1), hist.GetHistogram().GetSampleCount(),
				"exactly one observation must be recorded")
			assert.Positive(t, hist.GetHistogram().GetSampleSum(),
				"the whole-request duration must be a real positive measurement")

			// No request in this table should ever land on a bare "" backend
			// unless that case explicitly expects it.
			if tt.wantLabels["backend"] != "" {
				blank := labeledSeries(t, c, "lb_requests_total",
					map[string]string{"backend": "", "method": tt.method, "status_class": tt.wantLabels["status_class"]})
				assert.Nil(t, blank, "a request with a chosen backend must not be counted under backend=\"\"")
			}
		})
	}
}

// TestProxyWithoutMetricsCollectorServes proves the metrics reference is
// optional: a bare New(reg, sel) with no SetMetrics call records nothing and
// does not panic, mirroring how a bare New registers no round-trip observers.
func TestProxyWithoutMetricsCollectorServes(t *testing.T) {
	serving := startBackend(t, "backend-a")
	reg := registryFrom(t, backendEntry{"backend-a", serving.URL})
	p := New(reg, balancer.NewRoundRobin(reg))

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestProxyActiveConnectionsGaugeTracksConcurrentInFlightRequests mirrors
// S1.T6's ActiveConns leak-check against the metric: with requests blocked in
// the backend, lb_active_connections must match Backend.ActiveConns() at every
// point, and after they drain both must return to 0. Asserting the gauge
// alongside the backend counter proves the gauge shares the IncActive/DecActive
// call sites rather than merely counting something correlated (ADR-0013
// decision 7).
func TestProxyActiveConnectionsGaugeTracksConcurrentInFlightRequests(t *testing.T) {
	const requests = 100

	var inFlight atomic.Int64
	release := make(chan struct{})
	backendSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inFlight.Add(1)
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(backendSrv.Close)

	reg := registryFrom(t, backendEntry{"backend-a", backendSrv.URL})
	c := metrics.NewCollector()
	p := New(reg, balancer.NewLeastConnections(reg))
	p.SetMetrics(c)
	front := httptest.NewServer(p)
	t.Cleanup(front.Close)

	// gauge reads lb_active_connections for the one backend, returning -1 if
	// the series does not exist yet so a missing series fails an equality
	// assertion rather than panicking.
	gauge := func() float64 {
		m := labeledSeries(t, c, "lb_active_connections", map[string]string{"backend": "backend-a"})
		if m == nil {
			return -1
		}
		return m.GetGauge().GetValue()
	}

	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := front.Client().Get(front.URL)
			if err != nil {
				t.Errorf("request failed: %v", err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()
	}

	require.Eventually(t, func() bool {
		return inFlight.Load() == int64(requests)
	}, 5*time.Second, 10*time.Millisecond, "all requests should reach the backend and block")

	assert.Equal(t, int64(requests), reg.All()[0].ActiveConns(),
		"all in-flight requests should be counted by the backend")
	assert.Equal(t, float64(requests), gauge(),
		"the gauge must match the backend's active-connection count while requests are in flight")

	close(release)
	wg.Wait()

	require.Eventually(t, func() bool {
		return reg.All()[0].ActiveConns() == 0 && gauge() == 0
	}, 5*time.Second, 10*time.Millisecond,
		"both the backend counter and the gauge should drain back to zero")
}
