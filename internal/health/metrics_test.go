package health

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/logger"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// backendHealthyGauge reads lb_backend_healthy for one backend from the
// collector's private registry. It returns -1 when the series is absent, a
// sentinel no real gauge value can take, so a missing series fails an equality
// assertion rather than reading as 0.
func backendHealthyGauge(t *testing.T, c *metrics.Collector, backendName string) float64 {
	t.Helper()
	mfs, err := c.Registry().Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != "lb_backend_healthy" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "backend" && lp.GetValue() == backendName {
					return m.GetGauge().GetValue()
				}
			}
		}
	}
	return -1
}

// TestCheckerSetsBackendHealthyGaugeOnEjection proves the active checker's
// failure-threshold crossing drives lb_backend_healthy to 0 from the same
// `==`-gated, genuine-transition-guarded site its health_ejected log line fires
// from — one shared signal, so the gauge cannot disagree with the log about
// whether the transition happened.
func TestCheckerSetsBackendHealthyGaugeOnEjection(t *testing.T) {
	l, dump := captureLogger(t)
	c := metrics.NewCollector()
	reg := newTestRegistry(t, statusServer(t, http.StatusInternalServerError).URL)
	chk := New(reg, time.Second, time.Second, l, c)
	b := reg.All()[0]
	c.SetBackendHealthy(b.Name, true) // as main seeds every backend at startup

	p := chk.newProber(b)
	for i := 0; i < 2*probeFailuresBeforeUnhealthy; i++ {
		require.False(t, p.probeOnce(context.Background()))
	}

	assert.Equal(t, 0.0, backendHealthyGauge(t, c, b.Name))
	require.Len(t, recordsWithEvent(dump(), logger.EventHealthEjected), 1,
		"the gauge shares the once-per-streak log signal")
}

// TestCheckerSetsBackendHealthyGaugeOnReinstatement proves a backend that
// crosses the success threshold after ejection drives its gauge back to 1.
func TestCheckerSetsBackendHealthyGaugeOnReinstatement(t *testing.T) {
	c := metrics.NewCollector()
	reg := newTestRegistry(t, statusServer(t, http.StatusOK).URL)
	chk := New(reg, time.Second, time.Second, discardLogger(), c)
	b := reg.All()[0]
	c.SetBackendHealthy(b.Name, false) // as an ejection would
	b.MarkUnhealthy()

	p := chk.newProber(b)
	for i := 0; i < 2*probeSuccessesBeforeHealthy; i++ {
		require.True(t, p.probeOnce(context.Background()))
	}

	assert.Equal(t, 1.0, backendHealthyGauge(t, c, b.Name))
}

// TestOutlierDetectorSetsBackendHealthyGaugeOnEjection proves the passive
// detector's existing ejected-per-episode guard drives the gauge to 0 exactly
// at the ejection, not once per failure in the burst.
func TestOutlierDetectorSetsBackendHealthyGaugeOnEjection(t *testing.T) {
	l, dump := captureLogger(t)
	c := metrics.NewCollector()
	b := outlierBackends(t, 1)[0]
	c.SetBackendHealthy(b.Name, true)
	d := NewOutlierDetector(l, c)

	for i := 0; i < outlierFailuresBeforeEject+3*outlierWindowSize; i++ {
		d.ObserveRoundTrip(b, time.Millisecond, false)
	}

	assert.Equal(t, 0.0, backendHealthyGauge(t, c, b.Name))
	require.Len(t, recordsWithEvent(dump(), logger.EventHealthEjected), 1,
		"the gauge shares the once-per-episode log signal")
}
