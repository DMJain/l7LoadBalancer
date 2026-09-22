package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// endpointFixture bundles the probe handler with the state it reads, so a test
// can drive the checker and registry into a known state and then issue a real
// HTTP request through the handler.
type endpointFixture struct {
	handler   http.Handler
	registry  *backend.Registry
	checker   *Checker
	collector *metrics.Collector
}

// newEndpointFixture builds a handler over n healthy backends answering 200,
// with configLoaded supplied as-is. No probe has run yet, so
// ProbeRoundComplete is false and every backend is selectable.
func newEndpointFixture(t *testing.T, configLoaded bool, n int) *endpointFixture {
	t.Helper()
	srv := statusServer(t, http.StatusOK)
	urls := make([]string, n)
	for i := range urls {
		urls[i] = srv.URL
	}
	reg := newTestRegistry(t, urls...)
	collector := metrics.NewCollector()
	checker := New(reg, time.Second, time.Second, discardLogger(), collector)
	return &endpointFixture{
		handler:   NewHandler(checker, reg, configLoaded, collector),
		registry:  reg,
		checker:   checker,
		collector: collector,
	}
}

// completeProbeRound drives one successful probe per backend, closing the
// checker's one-shot first-round latch (the /startupz and /readyz precondition).
func (f *endpointFixture) completeProbeRound(t *testing.T) {
	t.Helper()
	for _, b := range f.registry.All() {
		require.True(t, f.checker.newProber(b).probeOnce(context.Background()))
	}
	require.True(t, f.checker.ProbeRoundComplete(), "precondition: the first probe round is complete")
}

// markAllUnhealthy evicts every backend, emptying Registry.Selectable().
func (f *endpointFixture) markAllUnhealthy() {
	for _, b := range f.registry.All() {
		b.MarkUnhealthy()
	}
}

// get issues a GET through the handler and returns the recorder.
func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// TestLivezAlwaysAlive pins decision D4: /livez answers 200 with
// {"status":"alive"} under every state, including a not-yet-probed checker, an
// unloaded config, and a fully-evicted fleet. The probe timing out — not this
// handler's body — is the liveness failure signal.
func TestLivezAlwaysAlive(t *testing.T) {
	cases := []struct {
		name         string
		configLoaded bool
		state        func(f *endpointFixture, t *testing.T)
	}{
		{"fresh, unprobed, config not loaded", false, func(*endpointFixture, *testing.T) {}},
		{"fresh, unprobed, config loaded", true, func(*endpointFixture, *testing.T) {}},
		{"probe round complete", true, func(f *endpointFixture, t *testing.T) { f.completeProbeRound(t) }},
		{"no selectable backends", true, func(f *endpointFixture, t *testing.T) {
			f.completeProbeRound(t)
			f.markAllUnhealthy()
			require.Empty(t, f.registry.Selectable())
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newEndpointFixture(t, tc.configLoaded, 2)
			tc.state(f, t)

			rec := get(t, f.handler, "/livez")
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.JSONEq(t, `{"status":"alive"}`, rec.Body.String())
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
		})
	}
}

// TestStartupzGate covers D5: /startupz is 503 until config is loaded *and* the
// first probe round is complete, then 200 forever — including after a full
// fleet eviction, because it does not gate on the selectable set.
func TestStartupzGate(t *testing.T) {
	t.Run("before the first probe round", func(t *testing.T) {
		f := newEndpointFixture(t, true, 2)
		rec := get(t, f.handler, "/startupz")
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.JSONEq(t,
			`{"status":"not_ready","checks":{"config_loaded":true,"initial_probe_complete":false}}`,
			rec.Body.String())
	})

	t.Run("config not loaded holds the gate even after probing", func(t *testing.T) {
		f := newEndpointFixture(t, false, 2)
		f.completeProbeRound(t)
		rec := get(t, f.handler, "/startupz")
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.JSONEq(t,
			`{"status":"not_ready","checks":{"config_loaded":false,"initial_probe_complete":true}}`,
			rec.Body.String())
	})

	t.Run("after the first probe round", func(t *testing.T) {
		f := newEndpointFixture(t, true, 2)
		f.completeProbeRound(t)
		rec := get(t, f.handler, "/startupz")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t,
			`{"status":"ready","checks":{"config_loaded":true,"initial_probe_complete":true}}`,
			rec.Body.String())
	})

	t.Run("stays 200 under full backend eviction", func(t *testing.T) {
		f := newEndpointFixture(t, true, 2)
		f.completeProbeRound(t)
		f.markAllUnhealthy()
		require.Empty(t, f.registry.Selectable())

		rec := get(t, f.handler, "/startupz")
		assert.Equal(t, http.StatusOK, rec.Code,
			"startup is a one-way transition: it does not gate on the selectable set")
		assert.JSONEq(t,
			`{"status":"ready","checks":{"config_loaded":true,"initial_probe_complete":true}}`,
			rec.Body.String())
	})
}

// TestReadyzMatrix covers D6/D7: /readyz is 200 only when all three conditions
// hold, and on 503 the failing check(s) stay visible in the per-check body.
func TestReadyzMatrix(t *testing.T) {
	cases := []struct {
		name         string
		configLoaded bool
		state        func(f *endpointFixture, t *testing.T)
		wantCode     int
		wantBody     string
	}{
		{
			name:         "all three conditions hold",
			configLoaded: true,
			state:        func(f *endpointFixture, t *testing.T) { f.completeProbeRound(t) },
			wantCode:     http.StatusOK,
			wantBody:     `{"status":"ready","checks":{"config_loaded":true,"initial_probe_complete":true,"selectable_backends":2}}`,
		},
		{
			name:         "config not loaded",
			configLoaded: false,
			state:        func(f *endpointFixture, t *testing.T) { f.completeProbeRound(t) },
			wantCode:     http.StatusServiceUnavailable,
			wantBody:     `{"status":"not_ready","checks":{"config_loaded":false,"initial_probe_complete":true,"selectable_backends":2}}`,
		},
		{
			name:         "before the first probe round",
			configLoaded: true,
			state:        func(*endpointFixture, *testing.T) {},
			wantCode:     http.StatusServiceUnavailable,
			wantBody:     `{"status":"not_ready","checks":{"config_loaded":true,"initial_probe_complete":false,"selectable_backends":2}}`,
		},
		{
			name:         "empty selectable set",
			configLoaded: true,
			state: func(f *endpointFixture, t *testing.T) {
				f.completeProbeRound(t)
				f.markAllUnhealthy()
			},
			wantCode: http.StatusServiceUnavailable,
			wantBody: `{"status":"not_ready","checks":{"config_loaded":true,"initial_probe_complete":true,"selectable_backends":0}}`,
		},
		{
			name:         "probe round incomplete and selectable set empty",
			configLoaded: true,
			state: func(f *endpointFixture, _ *testing.T) {
				f.markAllUnhealthy()
			},
			wantCode: http.StatusServiceUnavailable,
			wantBody: `{"status":"not_ready","checks":{"config_loaded":true,"initial_probe_complete":false,"selectable_backends":0}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newEndpointFixture(t, tc.configLoaded, 2)
			tc.state(f, t)

			rec := get(t, f.handler, "/readyz")
			assert.Equal(t, tc.wantCode, rec.Code)
			assert.JSONEq(t, tc.wantBody, rec.Body.String())
		})
	}
}

// TestReadyzLiveTransitions proves the selectable-set check runs per request:
// one running handler goes 200 → 503 → 200 as the fleet is evicted and
// restored via MarkUnhealthy/MarkHealthy.
func TestReadyzLiveTransitions(t *testing.T) {
	f := newEndpointFixture(t, true, 2)
	f.completeProbeRound(t)

	rec := get(t, f.handler, "/readyz")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"selectable_backends":2`)

	f.markAllUnhealthy()
	rec = get(t, f.handler, "/readyz")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), `"selectable_backends":0`)
	assert.Contains(t, rec.Body.String(), `"status":"not_ready"`)

	for _, b := range f.registry.All() {
		b.MarkHealthy()
	}
	rec = get(t, f.handler, "/readyz")
	assert.Equal(t, http.StatusOK, rec.Code, "a regained backend must restore readiness")
	assert.Contains(t, rec.Body.String(), `"selectable_backends":2`)
}

// TestProbeCounterIncrements covers D8/D9: every probe hit increments
// lb_health_probe_total{endpoint,status} with status-class labels, and probe
// traffic never touches lb_requests_total (D10).
func TestProbeCounterIncrements(t *testing.T) {
	f := newEndpointFixture(t, true, 1)
	f.completeProbeRound(t)

	get(t, f.handler, "/livez")
	get(t, f.handler, "/livez")
	get(t, f.handler, "/readyz")
	get(t, f.handler, "/startupz")

	const want = `
# HELP lb_health_probe_total Total health-endpoint probe responses, by probe endpoint and response status class.
# TYPE lb_health_probe_total counter
lb_health_probe_total{endpoint="/livez",status="2xx"} 2
lb_health_probe_total{endpoint="/readyz",status="2xx"} 1
lb_health_probe_total{endpoint="/startupz",status="2xx"} 1
`
	require.NoError(t, testutil.GatherAndCompare(
		f.collector.Registry(), strings.NewReader(want), "lb_health_probe_total"))

	assert.Zero(t, testutil.CollectAndCount(f.collector.Registry(), "lb_requests_total"),
		"probes must never be counted as client traffic")
}

// TestProbeCounterRecordsFailures pins that a 503 lands in the "5xx" status
// class, not a raw code, on the endpoint that produced it.
func TestProbeCounterRecordsFailures(t *testing.T) {
	f := newEndpointFixture(t, true, 1) // probe round never completes

	get(t, f.handler, "/readyz")
	get(t, f.handler, "/startupz")

	const want = `
# HELP lb_health_probe_total Total health-endpoint probe responses, by probe endpoint and response status class.
# TYPE lb_health_probe_total counter
lb_health_probe_total{endpoint="/readyz",status="5xx"} 1
lb_health_probe_total{endpoint="/startupz",status="5xx"} 1
`
	require.NoError(t, testutil.GatherAndCompare(
		f.collector.Registry(), strings.NewReader(want), "lb_health_probe_total"))
}
