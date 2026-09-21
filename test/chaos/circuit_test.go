package chaos_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
)

// circuitIsolationProbeInterval pushes the active checker's cadence far past the
// test window. T9 owns the circuit-gate axis, and it must be isolated from the
// health axis to be observable: active probes treat a 5xx as a failure, so the
// fast 20ms path used by the eviction arcs would health-eject the 500-serving
// backend and drop it out of Registry.Selectable before a half-open trial could
// ever be taken. A long interval keeps the backend healthy for the test's
// duration without bending any production constant (the cadence is a config
// knob, ADR-0011 decision 10). See the ticket's recorded deviations.
const circuitIsolationProbeInterval = time.Hour

// circuitChaosConfig builds a single-backend, round-robin config whose active
// checker is neutralized for the test window. A single backend makes the
// round-robin trial deterministic: the one post-cooldown request is the trial,
// rather than one of N peers that a multi-backend registry might select first.
func circuitChaosConfig(fbs []*flippableBackend) *config.Config {
	probeInterval := circuitIsolationProbeInterval
	probeTimeout := time.Second
	cooldown := chaosCooldown
	backends := make([]config.BackendConfig, len(fbs))
	for i, fb := range fbs {
		backends[i] = config.BackendConfig{Name: fb.id, URL: fb.URL()}
	}
	return &config.Config{
		Listen:    ":0",
		Algorithm: config.AlgorithmRoundRobin,
		Health: config.HealthConfig{
			ProbeInterval: &probeInterval,
			ProbeTimeout:  &probeTimeout,
		},
		Circuit:  config.CircuitConfig{Cooldown: &cooldown},
		Backends: backends,
	}
}

// tripCircuit drives consecutive 5xx round trips through the proxy until the
// single backend's circuit opens, and returns once the gauge reports open. The
// consecutive-failure threshold is an unexported compile-time constant this
// external package cannot name, so the test blasts a small burst and polls;
// requests sent after the circuit opens are denied 503 and never reach the
// breaker, so the opening line fires exactly once.
func tripCircuit(t *testing.T, a *assembly, x *flippableBackend) {
	t.Helper()
	require.Eventually(t, func() bool {
		doRequest(a.handler)
		open, ok := gaugeValueOK(a.collector, "lb_circuit_state", circuitStateLabels(x.id, "open"))
		return ok && open == 1
	}, eventuallyDeadline, eventuallyTick, "repeated 5xx must open the circuit")
}

// TestChaosCircuitTripCooldownAndTrialSuccess is S3.T9 arc (iv). Repeated 5xx
// trips the circuit; a request during the cooldown is denied 503 without
// touching active-connection accounting; and the first request after the
// cooldown is admitted as the half-open trial, whose single success closes the
// circuit.
func TestChaosCircuitTripCooldownAndTrialSuccess(t *testing.T) {
	fbs := newFlippableBackends(t, 1)
	a := assemble(t, circuitChaosConfig(fbs))
	x := fbs[0]

	assertGauge(t, a.collector, "lb_circuit_state", circuitStateLabels(x.id, "closed"), 1)
	require.Empty(t, a.logs.transitionRecords(), "no transition lines before any failure")

	x.Serve500()
	tripCircuit(t, a, x)

	// Immediately after tripping, the cooldown is still running: a request is
	// denied before dispatch, so it never claims an active-connection slot.
	status, _ := doRequest(a.handler)
	require.Equal(t, http.StatusServiceUnavailable, status, "a request during cooldown is denied")
	active, ok := gaugeValueOK(a.collector, "lb_active_connections", map[string]string{"backend": x.id})
	require.True(t, ok, "active-connections series must exist")
	require.Equal(t, 0.0, active, "a denied request must not touch active-connection accounting")

	assertGauge(t, a.collector, "lb_circuit_state", circuitStateLabels(x.id, "open"), 1)
	assertTransitionLogged(t, a.logs, logger.EventCircuitOpened, x.id, logger.ReasonConsecutiveFailures)
	require.Equal(t, 1, a.logs.eventCount(logger.EventCircuitOpened, x.id),
		"exactly one circuit-open line for the 5xx trip")

	// Flip the backend healthy and let the first post-cooldown request be the
	// trial. Polling the outcome (not a fixed sleep) absorbs the cooldown.
	x.Serve200()
	var trialStatus int
	require.Eventually(t, func() bool {
		trialStatus, _ = doRequest(a.handler)
		closed, ok := gaugeValueOK(a.collector, "lb_circuit_state", circuitStateLabels(x.id, "closed"))
		return ok && closed == 1
	}, eventuallyDeadline, eventuallyTick, "the half-open trial's success must close the circuit")

	require.Equal(t, http.StatusOK, trialStatus, "the admitted trial must reach the recovered backend")
	assertTransitionLogged(t, a.logs, logger.EventCircuitClosed, x.id, logger.ReasonTrialSuccess)

	// Pin the documented gap (ADR-0013 decision 13): the proxy path never
	// reports the Open→Half-Open promotion, because Registry.Selectable's
	// CircuitOpen read wins the promotion CAS before Allow runs.
	require.Equal(t, 0, a.logs.transitionCount(logger.EventCircuitHalfOpened, x.id, logger.ReasonCooldownElapsed),
		"the Selectable-won half-open promotion is never logged")
}

// TestChaosCircuitTrialFailureReopens is S3.T9 arc (v). The circuit trips as in
// arc (iv), but the backend keeps failing through the cooldown, so the half-open
// trial's single failure reopens the circuit. The reopen is observable only as
// the trial_failure line — the gauge is open before and after — which is exactly
// why the log carries the distinguishing reason.
func TestChaosCircuitTrialFailureReopens(t *testing.T) {
	fbs := newFlippableBackends(t, 1)
	a := assemble(t, circuitChaosConfig(fbs))
	x := fbs[0]

	x.Serve500()
	tripCircuit(t, a, x)
	assertTransitionLogged(t, a.logs, logger.EventCircuitOpened, x.id, logger.ReasonConsecutiveFailures)

	// Poll requests until the post-cooldown trial fails and reopens the circuit.
	require.Eventually(t, func() bool {
		doRequest(a.handler)
		return a.logs.transitionCount(logger.EventCircuitOpened, x.id, logger.ReasonTrialFailure) == 1
	}, eventuallyDeadline, eventuallyTick, "a failed half-open trial must reopen the circuit")

	assertGauge(t, a.collector, "lb_circuit_state", circuitStateLabels(x.id, "open"), 1)
	require.Equal(t, 0, a.logs.transitionCount(logger.EventCircuitClosed, x.id, logger.ReasonTrialSuccess),
		"a failed trial must not close the circuit")
	require.Equal(t, 0, a.logs.transitionCount(logger.EventCircuitHalfOpened, x.id, logger.ReasonCooldownElapsed),
		"the Selectable-won half-open promotion is never logged")
}

// TestChaosCircuitNoPromotionWithoutTraffic is S3.T9 arc (vi). With zero traffic
// through the cooldown window the circuit must stay strictly Open — no timer or
// background goroutine may promote it. The first request after the cooldown is
// the one that drives the promotion and takes the trial, mechanically proving
// the lazy, timer-free read-time check of ADR-0011 decision 6.
func TestChaosCircuitNoPromotionWithoutTraffic(t *testing.T) {
	fbs := newFlippableBackends(t, 1)
	a := assemble(t, circuitChaosConfig(fbs))
	x := fbs[0]

	x.Serve500()
	tripCircuit(t, a, x)

	// Wait through the cooldown sending no requests: the gauge must stay open
	// and no promotion may be observed. require.Never both asserts the
	// non-occurrence and consumes the window, so the next request is genuinely
	// post-cooldown.
	require.Never(t, func() bool {
		open, ok := gaugeValueOK(a.collector, "lb_circuit_state", circuitStateLabels(x.id, "open"))
		return !ok || open != 1 ||
			a.logs.transitionCount(logger.EventCircuitHalfOpened, x.id, logger.ReasonCooldownElapsed) != 0
	}, chaosCooldown+100*time.Millisecond, eventuallyTick,
		"no Half-Open promotion may fire without a request driving it")

	// The first request after the cooldown drives the promotion and the trial;
	// its failure reopens the circuit on that same request.
	status, _ := doRequest(a.handler)
	require.Equal(t, http.StatusInternalServerError, status, "the first post-cooldown request is the trial")
	require.Equal(t, 1, a.logs.transitionCount(logger.EventCircuitOpened, x.id, logger.ReasonTrialFailure),
		"the promotion and trial must have happened on that request")
	require.Equal(t, 0, a.logs.transitionCount(logger.EventCircuitHalfOpened, x.id, logger.ReasonCooldownElapsed),
		"the Selectable-won half-open promotion is never logged")
}
