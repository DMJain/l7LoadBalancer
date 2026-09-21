package circuit

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// wantCircuitState renders the expected lb_circuit_state family for a single
// backend: wantState at 1 and the other two at 0, so a caller asserting it also
// pins the exactly-one-state-is-1 invariant.
func wantCircuitState(backendName string, wantState metrics.CircuitState) string {
	var b strings.Builder
	b.WriteString("# HELP lb_circuit_state Circuit breaker state per backend as a label enum; exactly one of closed/open/half_open is 1.\n")
	b.WriteString("# TYPE lb_circuit_state gauge\n")
	for _, s := range []metrics.CircuitState{
		metrics.CircuitStateClosed,
		metrics.CircuitStateOpen,
		metrics.CircuitStateHalfOpen,
	} {
		value := 0
		if s == wantState {
			value = 1
		}
		fmt.Fprintf(&b, "lb_circuit_state{backend=%q,state=%q} %d\n", backendName, string(s), value)
	}
	return b.String()
}

// assertCircuitState asserts one backend's full lb_circuit_state label enum
// reads wantState at 1 with the other two states at 0, read back from the
// collector's private registry.
func assertCircuitState(t *testing.T, c *metrics.Collector, backendName string, wantState metrics.CircuitState) {
	t.Helper()
	require.NoError(t, testutil.GatherAndCompare(
		c.Registry(), strings.NewReader(wantCircuitState(backendName, wantState)), "lb_circuit_state"))
}

// TestBreakerDrivesCircuitStateGaugeThroughFullCycle proves lb_circuit_state is
// written from the same CircuitTransition signal the ticket-05 log lines fire
// from, tracking closed -> open -> half-open -> closed -> open with exactly one
// series at 1 and the other two at 0 at every step (ADR-0013 decision 15).
func TestBreakerDrivesCircuitStateGaugeThroughFullCycle(t *testing.T) {
	const cooldown = 50 * time.Millisecond
	reg := testRegistry(t)
	c := metrics.NewCollector()
	br := New(cooldown, discardLogger(), c)
	b := reg.All()[0]
	c.SetCircuitState(b.Name, metrics.CircuitStateClosed) // as main seeds at startup

	assertCircuitState(t, c, b.Name, metrics.CircuitStateClosed)

	for i := 0; i < circuitFailuresBeforeOpen; i++ {
		br.ObserveRoundTrip(b, 0, false)
	}
	assertCircuitState(t, c, b.Name, metrics.CircuitStateOpen)

	time.Sleep(200 * time.Millisecond)
	require.True(t, br.Allow(b), "half-open must admit the trial")
	assertCircuitState(t, c, b.Name, metrics.CircuitStateHalfOpen)

	br.ObserveRoundTrip(b, 0, true)
	assertCircuitState(t, c, b.Name, metrics.CircuitStateClosed)

	for i := 0; i < circuitFailuresBeforeOpen; i++ {
		br.ObserveRoundTrip(b, 0, false)
	}
	assertCircuitState(t, c, b.Name, metrics.CircuitStateOpen)
}

// TestBreakerSetsCircuitStateOpenOnTrialFailure covers the CircuitReopened
// path the full-cycle test does not: a failed half-open trial drives the gauge
// from half_open back to open, the metric counterpart of the ticket-05
// circuit_opened/trial_failure log line.
func TestBreakerSetsCircuitStateOpenOnTrialFailure(t *testing.T) {
	const cooldown = 50 * time.Millisecond
	reg := testRegistry(t)
	c := metrics.NewCollector()
	br := New(cooldown, discardLogger(), c)
	b := reg.All()[0]
	c.SetCircuitState(b.Name, metrics.CircuitStateClosed)

	for i := 0; i < circuitFailuresBeforeOpen; i++ {
		br.ObserveRoundTrip(b, 0, false)
	}
	time.Sleep(200 * time.Millisecond)
	require.True(t, br.Allow(b), "half-open must admit the trial")
	assertCircuitState(t, c, b.Name, metrics.CircuitStateHalfOpen)

	br.ObserveRoundTrip(b, 0, false)
	assertCircuitState(t, c, b.Name, metrics.CircuitStateOpen)
}

// TestBreakerLeavesCircuitStateGaugeOpenOnScanWonPromotion pins the metric
// consequence of the documented, permanent gap (ADR-0013 decision 13): a
// Half-Open promotion whose CAS is won by a Registry.Selectable scan (here,
// Breaker.Open) is never reported, so the gauge stays at open even though the
// circuit is now half-open — the same gap the log line has.
func TestBreakerLeavesCircuitStateGaugeOpenOnScanWonPromotion(t *testing.T) {
	const cooldown = 50 * time.Millisecond
	reg := testRegistry(t)
	c := metrics.NewCollector()
	br := New(cooldown, discardLogger(), c)
	b := reg.All()[0]
	c.SetCircuitState(b.Name, metrics.CircuitStateClosed)

	for i := 0; i < circuitFailuresBeforeOpen; i++ {
		br.ObserveRoundTrip(b, 0, false)
	}
	assertCircuitState(t, c, b.Name, metrics.CircuitStateOpen)

	time.Sleep(200 * time.Millisecond)
	require.False(t, br.Open(b), "the scan reads the circuit and promotes Open->Half-Open")

	// The promotion ran through CircuitOpen, which has no collector path, so the
	// gauge is not advanced to half_open here.
	assertCircuitState(t, c, b.Name, metrics.CircuitStateOpen)
}
