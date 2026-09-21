package chaos_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/logger"
)

// healthGaugeLabels is the lb_backend_healthy label selector for one backend.
func healthGaugeLabels(id string) map[string]string {
	return map[string]string{"backend": id}
}

// circuitStateLabels is the lb_circuit_state label selector for one backend
// and state.
func circuitStateLabels(id, state string) map[string]string {
	return map[string]string{"backend": id, "state": state}
}

// assertAllHealthy is the baseline every arc starts from: every backend's
// health gauge at 1 and no transition line captured yet.
func assertAllHealthy(t *testing.T, a *assembly, fbs []*flippableBackend) {
	t.Helper()
	for _, fb := range fbs {
		assertGauge(t, a.collector, "lb_backend_healthy", healthGaugeLabels(fb.id), 1)
	}
	require.Empty(t, a.logs.transitionRecords(), "no transition lines before any failure")
}

// TestChaosActiveOnlyEvictionAndRecovery is S3.T8 arc (i). A backend process
// dies (connection refused, not a handler swap to 503), the active checker
// ejects it after its consecutive-failure threshold, the selector stops
// choosing it, and a restart on the same address drives reinstatement and
// resumed selection.
func TestChaosActiveOnlyEvictionAndRecovery(t *testing.T) {
	fbs := newFlippableBackends(t, 3)
	a := assemble(t, chaosConfig(fbs))
	assertAllHealthy(t, a, fbs)

	x := fbs[0]

	// The process dies: real ECONNREFUSED for probes and proxy dispatch alike.
	x.Kill()

	require.Eventually(t, func() bool {
		v, ok := gaugeValueOK(a.collector, "lb_backend_healthy", healthGaugeLabels(x.id))
		return ok && v == 0
	}, eventuallyDeadline, eventuallyTick,
		"the killed backend's health gauge must flip to 0")

	assertTransitionLogged(t, a.logs, logger.EventHealthEjected, x.id, logger.ReasonProbeFailures)

	// The selector must stop choosing the dead backend: a clean burst of
	// requests, none of which is served by it.
	require.Eventually(t, func() bool {
		for i := 0; i < 12; i++ {
			if _, body := doRequest(a.handler); body == x.id {
				return false
			}
		}
		return true
	}, eventuallyDeadline, eventuallyTick, "selector must stop choosing the killed backend")

	// The process comes back on the same address.
	x.Restart()

	require.Eventually(t, func() bool {
		v, ok := gaugeValueOK(a.collector, "lb_backend_healthy", healthGaugeLabels(x.id))
		return ok && v == 1
	}, eventuallyDeadline, eventuallyTick,
		"the restarted backend's health gauge must flip back to 1")

	assertTransitionLogged(t, a.logs, logger.EventHealthReinstated, x.id, logger.ReasonProbeRecovered)

	require.Eventually(t, func() bool {
		for i := 0; i < 12; i++ {
			if _, body := doRequest(a.handler); body == x.id {
				return true
			}
		}
		return false
	}, eventuallyDeadline, eventuallyTick, "selector must resume choosing the restarted backend")
}

// TestChaosCombined500SignalHealthOnlyRecovery is S3.T8 arc (iii). A backend
// starts returning pure 500s: the failure signal ejects it (active-probe
// failure path) and opens its circuit. Flipping it back to 200 recovers only
// the health axis — the circuit stays open — which is the mechanical
// verification of the two-gate orthogonality ADR-0011 decision 1 claims.
func TestChaosCombined500SignalHealthOnlyRecovery(t *testing.T) {
	fbs := newFlippableBackends(t, 3)
	a := assemble(t, chaosConfig(fbs))
	assertAllHealthy(t, a, fbs)

	x := fbs[0]
	x.Serve500()

	// Drive enough requests for the circuit to open and let the active checker
	// eject on the same 500 signal. Round-robin gives the bad backend every
	// third selection; three proxy failures open the circuit (below passive
	// detection's five-in-window threshold), and the active probes cross their
	// own threshold within a probe interval or two.
	require.Eventually(t, func() bool {
		for i := 0; i < 9; i++ {
			doRequest(a.handler)
		}
		healthy, okH := gaugeValueOK(a.collector, "lb_backend_healthy", healthGaugeLabels(x.id))
		open, okO := gaugeValueOK(a.collector, "lb_circuit_state", circuitStateLabels(x.id, "open"))
		return okH && okO && healthy == 0 && open == 1
	}, eventuallyDeadline, eventuallyTick,
		"the 500 signal must eject the backend from health and open its circuit")

	assertTransitionLogged(t, a.logs, logger.EventHealthEjected, x.id, logger.ReasonProbeFailures)
	assertTransitionLogged(t, a.logs, logger.EventCircuitOpened, x.id, logger.ReasonConsecutiveFailures)

	// Flip the backend healthy again. Only the health axis may recover: the
	// circuit has taken no successful trial, so it stays open.
	x.Serve200()

	require.Eventually(t, func() bool {
		v, ok := gaugeValueOK(a.collector, "lb_backend_healthy", healthGaugeLabels(x.id))
		return ok && v == 1
	}, eventuallyDeadline, eventuallyTick, "the health gauge must recover once the backend answers 200")

	assertTransitionLogged(t, a.logs, logger.EventHealthReinstated, x.id, logger.ReasonProbeRecovered)

	open, ok := gaugeValueOK(a.collector, "lb_circuit_state", circuitStateLabels(x.id, "open"))
	require.True(t, ok, "circuit state series must exist")
	require.Equal(t, 1.0, open,
		"the circuit must stay open while the health axis recovers (ADR-0011 decision 1)")
}
