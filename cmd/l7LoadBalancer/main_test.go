package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// stubArgs replaces os.Args for the duration of a test and returns a restore
// function, so the probe branch can be exercised exactly as the real binary
// sees it — through os.Args, not a parameter.
func stubArgs(args []string) func() {
	orig := os.Args
	os.Args = args
	return func() { os.Args = orig }
}

// TestProbeCommandStatusMatrix pins the exit-code contract of `l7lb probe
// <url>`: any 2xx response is success (0), every other status is failure
// (non-zero). 301 is included deliberately — the probe must not follow the
// redirect, mirroring the health checker's own CheckRedirect policy.
func TestProbeCommandStatusMatrix(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   int
	}{
		{name: "200 OK", status: http.StatusOK, want: 0},
		{name: "204 No Content", status: http.StatusNoContent, want: 0},
		{name: "301 redirect", status: http.StatusMovedPermanently, want: 1},
		{name: "404 not found", status: http.StatusNotFound, want: 1},
		{name: "500 server error", status: http.StatusInternalServerError, want: 1},
		{name: "503 unavailable", status: http.StatusServiceUnavailable, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			defer stubArgs([]string{"l7lb", "probe", srv.URL})()

			code, handled := probeCommand(os.Args)
			require.True(t, handled, "a probe invocation must be handled")
			assert.Equal(t, tt.want, code)
		})
	}
}

// TestProbeCommandConnectionRefused proves a transport error (connection
// refused) exits non-zero and does so promptly, well inside the client
// timeout, so a Docker HEALTHCHECK cannot hang on a dead listener.
func TestProbeCommandConnectionRefused(t *testing.T) {
	defer stubArgs([]string{"l7lb", "probe", "http://127.0.0.1:0"})()

	start := time.Now()
	code, handled := probeCommand(os.Args)
	elapsed := time.Since(start)

	require.True(t, handled)
	assert.NotZero(t, code, "a refused connection must exit non-zero")
	assert.Less(t, elapsed, probeTimeout, "refusal must return before the probe timeout")
}

// TestProbeCommandRejectsMissingURL pins the usage-error exit code: `l7lb
// probe` with no URL is handled (it is a probe invocation) but reports the
// malformed invocation as a distinct non-2xx-style failure.
func TestProbeCommandRejectsMissingURL(t *testing.T) {
	code, handled := probeCommand([]string{"l7lb", "probe"})
	require.True(t, handled, "`l7lb probe` is a probe invocation")
	assert.Equal(t, 2, code)
}

// TestProbeCommandDoesNotInterceptLoadBalancer proves the branch is a
// subcommand, not a catch-all: an ordinary invocation (no args, or flag-led)
// falls through to the load-balancer startup path untouched.
func TestProbeCommandDoesNotInterceptLoadBalancer(t *testing.T) {
	for _, args := range [][]string{
		{"l7lb"},
		{"l7lb", "-config", "configs/example.yaml"},
		{"l7lb", "some-other-arg"},
	} {
		code, handled := probeCommand(args)
		assert.Falsef(t, handled, "args %v must fall through to the load balancer", args)
		assert.Zerof(t, code, "args %v must not produce an exit code", args)
	}
}

// TestBinaryLogsInjectedVersionAndCommit covers the two build-time facts the
// image relies on (spec D16): -ldflags -X main.version/main.commit reach the
// JSON startup log line, and the probe subcommand runs without ever touching
// flag parsing — the binary is executed from an empty directory with no
// configs/example.yaml, so a fall-through to config.Load would fail the probe
// for the wrong reason.
func TestBinaryLogsInjectedVersionAndCommit(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "l7lb")
	build := exec.Command("go", "build",
		"-ldflags=-X main.version=9.9.9 -X main.commit=deadbeef",
		"-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cmd := exec.Command(bin, "probe", srv.URL)
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "probe against 2xx should exit 0; output: %s", out)

	assert.Contains(t, string(out), `"version":"9.9.9"`)
	assert.Contains(t, string(out), `"commit":"deadbeef"`)
}

// TestSeedMetricsBackendHealthy proves main's startup seeding materializes
// every backend's lb_backend_healthy series at 1 through the collector's own
// setter — the same method real health transitions use — so a freshly started,
// never-degraded system renders a complete healthy dashboard on its first
// scrape instead of a blank panel (ADR-0013 decision 9).
func TestSeedMetricsBackendHealthy(t *testing.T) {
	reg, err := backend.NewRegistry([]config.BackendConfig{
		{Name: "backend-a", URL: "http://127.0.0.1:9001"},
		{Name: "backend-b", URL: "http://127.0.0.1:9002"},
	})
	require.NoError(t, err)

	c := metrics.NewCollector()
	seedMetrics(c, reg)

	const want = `
# HELP lb_backend_healthy Whether a backend is healthy (1) or unhealthy (0).
# TYPE lb_backend_healthy gauge
lb_backend_healthy{backend="backend-a"} 1
lb_backend_healthy{backend="backend-b"} 1
`
	require.NoError(t, testutil.GatherAndCompare(
		c.Registry(), strings.NewReader(want), "lb_backend_healthy"))
}

// TestSeedMetricsCircuitState proves main's startup seeding materializes every
// backend's lb_circuit_state label enum with closed=1 and the other two states
// at 0, through the collector's own setter — the same method real circuit
// transitions use — so a freshly started, never-degraded system renders a
// complete circuit panel on its first scrape instead of a blank one.
// Prometheus Vec metrics materialize no series until first written
// (ADR-0013 decision 9).
func TestSeedMetricsCircuitState(t *testing.T) {
	reg, err := backend.NewRegistry([]config.BackendConfig{
		{Name: "backend-a", URL: "http://127.0.0.1:9001"},
		{Name: "backend-b", URL: "http://127.0.0.1:9002"},
	})
	require.NoError(t, err)

	c := metrics.NewCollector()
	seedMetrics(c, reg)

	const want = `
# HELP lb_circuit_state Circuit breaker state per backend as a label enum; exactly one of closed/open/half_open is 1.
# TYPE lb_circuit_state gauge
lb_circuit_state{backend="backend-a",state="closed"} 1
lb_circuit_state{backend="backend-a",state="half_open"} 0
lb_circuit_state{backend="backend-a",state="open"} 0
lb_circuit_state{backend="backend-b",state="closed"} 1
lb_circuit_state{backend="backend-b",state="half_open"} 0
lb_circuit_state{backend="backend-b",state="open"} 0
`
	require.NoError(t, testutil.GatherAndCompare(
		c.Registry(), strings.NewReader(want), "lb_circuit_state"))
}

// TestSeedMetricsActiveConnections proves main's startup seeding materializes
// every backend's lb_active_connections series at 0 through the collector's own
// setter, so a freshly started, never-trafficked system renders a complete
// dashboard on its first scrape instead of a blank panel. Prometheus Vec
// metrics materialize no series until first written (ADR-0013 decision 9).
func TestSeedMetricsActiveConnections(t *testing.T) {
	reg, err := backend.NewRegistry([]config.BackendConfig{
		{Name: "backend-a", URL: "http://127.0.0.1:9001"},
		{Name: "backend-b", URL: "http://127.0.0.1:9002"},
	})
	require.NoError(t, err)

	c := metrics.NewCollector()
	seedMetrics(c, reg)

	const want = `
# HELP lb_active_connections In-flight requests currently being served by a backend.
# TYPE lb_active_connections gauge
lb_active_connections{backend="backend-a"} 0
lb_active_connections{backend="backend-b"} 0
`
	require.NoError(t, testutil.GatherAndCompare(
		c.Registry(), strings.NewReader(want), "lb_active_connections"))
}
