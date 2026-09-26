package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findMetric gathers c's registry and returns the dto.Metric whose label set
// exactly matches want, or nil if no such series has been written yet.
func findMetric(t *testing.T, c *Collector, name string, want map[string]string) *dto.Metric {
	t.Helper()
	mfs, err := c.Registry().Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelsMatch(m.GetLabel(), want) {
				return m
			}
		}
	}
	return nil
}

func labelsMatch(pairs []*dto.LabelPair, want map[string]string) bool {
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

func TestCollectorObserveRequest(t *testing.T) {
	t.Parallel()
	c := NewCollector()
	labels := map[string]string{"backend": "backend-a", "method": "GET", "status_class": "2xx"}

	assert.Nil(t, findMetric(t, c, "lb_requests_total", labels), "no series before the first observation")

	c.ObserveRequest("backend-a", "GET", "2xx", 12*time.Millisecond)
	c.ObserveRequest("backend-a", "GET", "2xx", 8*time.Millisecond)

	m := findMetric(t, c, "lb_requests_total", labels)
	require.NotNil(t, m)
	assert.Equal(t, 2.0, m.GetCounter().GetValue())

	// A different label combination is a distinct series.
	c.ObserveRequest("backend-b", "POST", "5xx", 30*time.Millisecond)
	other := findMetric(t, c, "lb_requests_total", map[string]string{"backend": "backend-b", "method": "POST", "status_class": "5xx"})
	require.NotNil(t, other)
	assert.Equal(t, 1.0, other.GetCounter().GetValue())
}

func TestCollectorObserveRequestHistogram(t *testing.T) {
	t.Parallel()
	c := NewCollector()
	c.ObserveRequest("backend-a", "GET", "2xx", 12*time.Millisecond)

	h := findMetric(t, c, "lb_request_duration_seconds",
		map[string]string{"backend": "backend-a", "method": "GET", "status_class": "2xx"}).GetHistogram()
	require.NotNil(t, h)
	assert.Equal(t, uint64(1), h.GetSampleCount())
	assert.InDelta(t, 0.012, h.GetSampleSum(), 1e-9)

	bounds := make([]float64, 0, len(h.GetBucket()))
	for _, b := range h.GetBucket() {
		bounds = append(bounds, b.GetUpperBound())
	}
	assert.Equal(t, []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}, bounds,
		"provisional bucket set from doc.go, pending Sprint 5 benchmark data")
}

func TestCollectorRecordProbe(t *testing.T) {
	t.Parallel()
	c := NewCollector()

	assert.Nil(t, findMetric(t, c, "lb_health_probe_total", map[string]string{"endpoint": "/livez", "status": "2xx"}),
		"no series before the first probe")

	c.RecordProbe("/livez", http.StatusOK)
	c.RecordProbe("/livez", http.StatusOK)
	c.RecordProbe("/readyz", http.StatusServiceUnavailable)
	c.RecordProbe("/startupz", http.StatusOK)

	m := findMetric(t, c, "lb_health_probe_total", map[string]string{"endpoint": "/livez", "status": "2xx"})
	require.NotNil(t, m)
	assert.Equal(t, 2.0, m.GetCounter().GetValue())

	// The label is a status *class*, not the raw code: a 503 lands in "5xx".
	fail := findMetric(t, c, "lb_health_probe_total", map[string]string{"endpoint": "/readyz", "status": "5xx"})
	require.NotNil(t, fail)
	assert.Equal(t, 1.0, fail.GetCounter().GetValue())
	assert.Nil(t, findMetric(t, c, "lb_health_probe_total", map[string]string{"endpoint": "/readyz", "status": "503"}),
		"raw status codes must never become a label value")

	// Distinct endpoints are distinct series.
	other := findMetric(t, c, "lb_health_probe_total", map[string]string{"endpoint": "/startupz", "status": "2xx"})
	require.NotNil(t, other)
	assert.Equal(t, 1.0, other.GetCounter().GetValue())
}

func TestCollectorSetBackendHealthy(t *testing.T) {
	t.Parallel()
	c := NewCollector()
	labels := map[string]string{"backend": "backend-a"}

	c.SetBackendHealthy("backend-a", true)
	require.NotNil(t, findMetric(t, c, "lb_backend_healthy", labels))
	assert.Equal(t, 1.0, findMetric(t, c, "lb_backend_healthy", labels).GetGauge().GetValue())

	c.SetBackendHealthy("backend-a", false)
	assert.Equal(t, 0.0, findMetric(t, c, "lb_backend_healthy", labels).GetGauge().GetValue())
}

func TestCollectorSetCircuitStateZeroesOtherStates(t *testing.T) {
	t.Parallel()
	c := NewCollector()
	backend := "backend-a"

	stateValue := func(state CircuitState) float64 {
		t.Helper()
		m := findMetric(t, c, "lb_circuit_state", map[string]string{"backend": backend, "state": string(state)})
		require.NotNilf(t, m, "state %q series must exist", state)
		return m.GetGauge().GetValue()
	}

	c.SetCircuitState(backend, CircuitStateClosed)
	assert.Equal(t, 1.0, stateValue(CircuitStateClosed))
	assert.Equal(t, 0.0, stateValue(CircuitStateOpen))
	assert.Equal(t, 0.0, stateValue(CircuitStateHalfOpen))

	// The second transition must zero the now-stale closed series, not just
	// set the new one — the exactly-one-state-is-1 invariant.
	c.SetCircuitState(backend, CircuitStateOpen)
	assert.Equal(t, 0.0, stateValue(CircuitStateClosed))
	assert.Equal(t, 1.0, stateValue(CircuitStateOpen))
	assert.Equal(t, 0.0, stateValue(CircuitStateHalfOpen))

	// A third transition proves zeroing is unconditional on every call.
	c.SetCircuitState(backend, CircuitStateHalfOpen)
	assert.Equal(t, 0.0, stateValue(CircuitStateClosed))
	assert.Equal(t, 0.0, stateValue(CircuitStateOpen))
	assert.Equal(t, 1.0, stateValue(CircuitStateHalfOpen))

	// An unrecognized state is ignored: the previous series stays intact
	// rather than all three being zeroed.
	c.SetCircuitState(backend, CircuitState("bogus"))
	assert.Equal(t, 0.0, stateValue(CircuitStateClosed))
	assert.Equal(t, 0.0, stateValue(CircuitStateOpen))
	assert.Equal(t, 1.0, stateValue(CircuitStateHalfOpen))
}

func TestCollectorActiveConnections(t *testing.T) {
	t.Parallel()
	c := NewCollector()
	labels := map[string]string{"backend": "backend-a"}

	c.IncActiveConnections("backend-a")
	assert.Equal(t, 1.0, findMetric(t, c, "lb_active_connections", labels).GetGauge().GetValue())
	c.IncActiveConnections("backend-a")
	assert.Equal(t, 2.0, findMetric(t, c, "lb_active_connections", labels).GetGauge().GetValue())
	c.DecActiveConnections("backend-a")
	assert.Equal(t, 1.0, findMetric(t, c, "lb_active_connections", labels).GetGauge().GetValue())

	c.SetActiveConnections("backend-a", 5)
	assert.Equal(t, 5.0, findMetric(t, c, "lb_active_connections", labels).GetGauge().GetValue())

	// Setting zero materializes the series for a fresh backend at startup.
	c.SetActiveConnections("backend-b", 0)
	require.NotNil(t, findMetric(t, c, "lb_active_connections", map[string]string{"backend": "backend-b"}))
}

// TestCollectorDeletesBackendSeries proves the deletion operations reload needs
// for a removed backend: its healthy series and every circuit-state series are
// gone, while its active-connections series is deliberately left in place
// (deleted only at drain completion, S4.T4).
func TestCollectorDeletesBackendSeries(t *testing.T) {
	t.Parallel()
	c := NewCollector()
	c.SetBackendHealthy("backend-a", true)
	c.SetCircuitState("backend-a", CircuitStateOpen)
	c.SetActiveConnections("backend-a", 2)

	c.DeleteBackendHealthy("backend-a")
	c.DeleteCircuitState("backend-a")

	assert.Nil(t, findMetric(t, c, "lb_backend_healthy", map[string]string{"backend": "backend-a"}),
		"the healthy series must be gone")
	for _, state := range circuitStates {
		assert.Nil(t, findMetric(t, c, "lb_circuit_state",
			map[string]string{"backend": "backend-a", "state": string(state)}),
			"every circuit-state series must be gone")
	}
	require.NotNil(t, findMetric(t, c, "lb_active_connections", map[string]string{"backend": "backend-a"}),
		"the active-connections series is not deleted here")
}

// TestCollectorDeleteActiveConnections proves the drain's deletion (S4.T4): a
// removed backend's active-connections series is gone, and a same-name fresh
// backend's series survives when the drain skips the deletion because the name
// is in use — modelled here as deleting the old instance's series before the
// fresh one is seeded.
func TestCollectorDeleteActiveConnections(t *testing.T) {
	t.Parallel()
	c := NewCollector()
	c.SetActiveConnections("backend-a", 3)

	c.DeleteActiveConnections("backend-a")
	assert.Nil(t, findMetric(t, c, "lb_active_connections", map[string]string{"backend": "backend-a"}),
		"the active-connections series must be gone")

	c.SetActiveConnections("backend-a", 0)
	require.NotNil(t, findMetric(t, c, "lb_active_connections", map[string]string{"backend": "backend-a"}),
		"a fresh same-name series must exist after seeding")
	assert.Equal(t, 0.0, findMetric(t, c, "lb_active_connections", map[string]string{"backend": "backend-a"}).GetGauge().GetValue())
}

// TestCollectorDeleteUnknownSeriesIsNoop proves deletion is safe for a backend
// with no series, as it is for a backend removed before serving any traffic.
func TestCollectorDeleteUnknownSeriesIsNoop(t *testing.T) {
	t.Parallel()
	c := NewCollector()
	require.NotPanics(t, func() {
		c.DeleteBackendHealthy("absent")
		c.DeleteCircuitState("absent")
		c.DeleteActiveConnections("absent")
	})
}

func TestCollectorInstancesIndependent(t *testing.T) {
	t.Parallel()
	c1 := NewCollector()
	c2 := NewCollector()

	c1.ObserveRequest("backend-a", "GET", "2xx", time.Millisecond)

	assert.Nil(t, findMetric(t, c2, "lb_requests_total",
		map[string]string{"backend": "backend-a", "method": "GET", "status_class": "2xx"}),
		"a second collector must not see the first collector's series")
	require.NotNil(t, findMetric(t, c1, "lb_requests_total",
		map[string]string{"backend": "backend-a", "method": "GET", "status_class": "2xx"}))
}

func TestCollectorExpositionEndpoint(t *testing.T) {
	t.Parallel()
	c := NewCollector()
	c.ObserveRequest("backend-a", "GET", "2xx", 12*time.Millisecond)
	c.SetBackendHealthy("backend-a", true)
	c.SetCircuitState("backend-a", CircuitStateOpen)
	c.SetActiveConnections("backend-a", 1)
	c.RecordProbe("/livez", http.StatusOK)

	srv := httptest.NewServer(promhttp.HandlerFor(c.Registry(), promhttp.HandlerOpts{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	text := string(body)

	// Label names are emitted in alphabetical order by the exposition format.
	wants := []string{
		`lb_requests_total{backend="backend-a",method="GET",status_class="2xx"} 1`,
		`lb_request_duration_seconds_count{backend="backend-a",method="GET",status_class="2xx"} 1`,
		`lb_request_duration_seconds_bucket{backend="backend-a",method="GET",status_class="2xx",le="+Inf"} 1`,
		`lb_backend_healthy{backend="backend-a"} 1`,
		// All three state series are present; only the target is 1.
		`lb_circuit_state{backend="backend-a",state="closed"} 0`,
		`lb_circuit_state{backend="backend-a",state="open"} 1`,
		`lb_circuit_state{backend="backend-a",state="half_open"} 0`,
		`lb_active_connections{backend="backend-a"} 1`,
		`lb_health_probe_total{endpoint="/livez",status="2xx"} 1`,
	}
	for _, want := range wants {
		assert.Contains(t, text, want)
	}
	assert.NotContains(t, text, "status_code", "status_code is deliberately never a label")
}
