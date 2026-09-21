package chaos_test

import (
	"net/http"
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

	requireGaugeEventually(t, a.collector, "lb_backend_healthy", healthGaugeLabels(x.id), 0,
		"the killed backend's health gauge must flip to 0")

	assertTransitionLogged(t, a.logs, logger.EventHealthEjected, x.id, logger.ReasonProbeFailures)
	require.Equal(t, 1, a.logs.eventCount(logger.EventHealthEjected, x.id),
		"exactly one health ejection line for the killed backend, whatever its reason")

	// The selector must stop choosing the dead backend: a clean burst of
	// 200-served requests, none of which is the dead backend's 502/503.
	require.Eventually(t, func() bool {
		for i := 0; i < 12; i++ {
			status, body := doRequest(a.handler)
			if status != http.StatusOK || body == x.id {
				return false
			}
		}
		return true
	}, eventuallyDeadline, eventuallyTick, "selector must stop choosing the killed backend")

	// The process comes back on the same address.
	x.Restart()

	requireGaugeEventually(t, a.collector, "lb_backend_healthy", healthGaugeLabels(x.id), 1,
		"the restarted backend's health gauge must flip back to 1")

	assertTransitionLogged(t, a.logs, logger.EventHealthReinstated, x.id, logger.ReasonProbeRecovered)
	require.Equal(t, 1, a.logs.eventCount(logger.EventHealthReinstated, x.id),
		"exactly one reinstatement line for the restarted backend")

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
	// third selection; three proxy failures open the circuit — below passive
	// detection's five-in-window threshold, so the circuit excludes the bad
	// backend before passive detection can reach its own threshold — and the
	// active probes cross their threshold within a probe interval or two.
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
	require.Equal(t, 1, a.logs.eventCount(logger.EventHealthEjected, x.id),
		"exactly one health ejection line on the combined 500 signal")
	require.Equal(t, 1, a.logs.eventCount(logger.EventCircuitOpened, x.id),
		"exactly one circuit-open line on the combined 500 signal")

	// Flip the backend healthy again. Only the health axis may recover: the
	// circuit has taken no successful trial, so it stays open.
	x.Serve200()

	requireGaugeEventually(t, a.collector, "lb_backend_healthy", healthGaugeLabels(x.id), 1,
		"the health gauge must recover once the backend answers 200")

	assertTransitionLogged(t, a.logs, logger.EventHealthReinstated, x.id, logger.ReasonProbeRecovered)

	open, ok := gaugeValueOK(a.collector, "lb_circuit_state", circuitStateLabels(x.id, "open"))
	require.True(t, ok, "circuit state series must exist")
	require.Equal(t, 1.0, open,
		"the circuit must stay open while the health axis recovers (ADR-0011 decision 1)")
}
