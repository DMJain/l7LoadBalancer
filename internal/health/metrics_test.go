package health

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/logger"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// wantHealthyGauge renders the expected lb_backend_healthy family for a single
// backend, for comparison via testutil.GatherAndCompare.
func wantHealthyGauge(backendName string, value int) string {
	return fmt.Sprintf(`# HELP lb_backend_healthy Whether a backend is healthy (1) or unhealthy (0).
# TYPE lb_backend_healthy gauge
lb_backend_healthy{backend=%q} %d
`, backendName, value)
}

// assertHealthyGauge asserts one backend's lb_backend_healthy series reads
// value, read back from the collector's private registry.
func assertHealthyGauge(t *testing.T, c *metrics.Collector, backendName string, value int) {
	t.Helper()
	require.NoError(t, testutil.GatherAndCompare(
		c.Registry(), strings.NewReader(wantHealthyGauge(backendName, value)), "lb_backend_healthy"))
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

	assertHealthyGauge(t, c, b.Name, 0)
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

	assertHealthyGauge(t, c, b.Name, 1)
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

	assertHealthyGauge(t, c, b.Name, 0)
	require.Len(t, recordsWithEvent(dump(), logger.EventHealthEjected), 1,
		"the gauge shares the once-per-episode log signal")
}
