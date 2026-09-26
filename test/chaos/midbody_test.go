package chaos_test

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/logger"
)

// ServePartialThenClose installs a handler that sends 200 headers declaring
// more body bytes than it writes, then abruptly closes the connection. The
// proxy's body read then ends on a non-EOF error — S4.T6's mid-body-death
// signal, distinct from a clean io.EOF.
func (fb *flippableBackend) ServePartialThenClose(body string) {
	h := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			return
		}
		// Declare far more than is sent so the transport reports an
		// unexpected EOF rather than a clean end.
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 4096\r\n\r\n" + body)
		_ = buf.Flush()
		_ = conn.Close()
	}))
	fb.handler.Store(&h)
}

// TestChaosBackendDiesMidBody is S4.T6 arc (i). A backend sends response headers
// then dies before completing the body: the client keeps the bytes it received
// under the 200 the headers established, and the death leaves one WARN line with
// reason backend_died_mid_response carrying the backend, the path, and the bytes
// already copied. The headers-time success stands — no transition follows, and
// the backend stays healthy with a closed circuit (the outlier window is not
// fed a second event) — and the active-connection slot releases.
//
// The active checker's cadence is pushed past the test window (circuitChaosConfig)
// so the dying handler cannot also be probed into an ejection.
func TestChaosBackendDiesMidBody(t *testing.T) {
	fbs := newFlippableBackends(t, 1)
	a := assemble(t, circuitChaosConfig(fbs))

	x := fbs[0]
	const partial = "partial-body"
	x.ServePartialThenClose(partial)

	status, body := doRequestPath(a.handler, "/midbody")
	require.Equal(t, http.StatusOK, status,
		"the headers arrived, so the client sees the response the observer recorded")
	require.Equal(t, partial, body, "the bytes sent before the death must reach the client")

	deaths := a.logs.recordsWithField("reason", logger.ReasonBackendDiedMidResponse)
	require.Len(t, deaths, 1, "the mid-body death must emit exactly one line")
	require.Equal(t, "WARN", deaths[0].Level.String())
	fields := recordFields(deaths[0])
	require.Equal(t, x.id, fields["backend"])
	require.Equal(t, "/midbody", fields["path"])
	require.Equal(t, strconv.Itoa(len(partial)), fields["bytes_copied"])

	// The headers-time success stands: no circuit or health transition may have
	// followed, and the gauges still read the healthy/closed baseline.
	require.Empty(t, a.logs.transitionRecords(), "a mid-body death must feed no observer transition")
	assertGauge(t, a.collector, "lb_backend_healthy", healthGaugeLabels(x.id), 1)
	assertGauge(t, a.collector, "lb_circuit_state", circuitStateLabels(x.id, "closed"), 1)

	b := backendByName(t, a.reg, x.id)
	require.Equal(t, int64(0), b.ActiveConns(), "the slot must release after a truncated body")
	active, ok := gaugeValueOK(a.collector, "lb_active_connections", map[string]string{"backend": x.id})
	require.True(t, ok, "active-connections series must exist")
	require.Equal(t, 0.0, active, "the active-connections gauge must return to zero")
}

// TestChaosBackendKilledBeforeHeaders is S4.T6 arc (ii) and half of Sprint 4's
// exit criterion. A backend killed before it answers is a genuine transport
// failure: the client gets a clean 502, the failure reaches the observers (the
// fixed penalty is recorded), nothing crashes, and no active-connection slot or
// gauge series leaks.
func TestChaosBackendKilledBeforeHeaders(t *testing.T) {
	fbs := newFlippableBackends(t, 1)
	a := assemble(t, circuitChaosConfig(fbs))

	x := fbs[0]
	x.Kill()

	status, _ := doRequest(a.handler)
	require.Equal(t, http.StatusBadGateway, status,
		"a backend dead before headers must yield a clean 502")

	// A genuine transport failure is observed: the cold-start EWMA is set to the
	// fixed failure penalty (ADR-0010).
	b := backendByName(t, a.reg, x.id)
	require.Equal(t, 2*time.Second, b.EWMALatency(), "the transport failure must be observed")
	require.Equal(t, int64(0), b.ActiveConns(), "no active-connection slot may leak")
	active, ok := gaugeValueOK(a.collector, "lb_active_connections", map[string]string{"backend": x.id})
	require.True(t, ok, "active-connections series must exist")
	require.Equal(t, 0.0, active, "the active-connections gauge must return to zero")

	require.Empty(t, a.logs.recordsWithField("reason", logger.ReasonBackendDiedMidResponse),
		"a pre-headers death is a transport failure, not a mid-body death")
}
