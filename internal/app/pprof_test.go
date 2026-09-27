package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/config"
)

// TestMetricsListenerServesPprofAndMetrics is S4.T9's listener test: pprof is
// mounted on the metrics listener's handler alongside /metrics, so profiling
// needs no separate port. The metrics listener is already always-on and
// unauthenticated, so pprof adds no attack surface the project has not already
// accepted (ADR-0005 puts security hardening out of scope).
//
// Each endpoint is asserted by its body, not just its status: the bare metrics
// handler answers every path with 200, so a status-only check could not tell a
// mounted pprof from an unmounted one. profile and trace are excluded because
// they block while capturing a sample, and the index sub-paths (goroutine,
// heap, …) all route through pprof.Index.
func TestMetricsListenerServesPprofAndMetrics(t *testing.T) {
	silenceDefault(t)

	cfg := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: "http://127.0.0.1:9001"},
	})
	require.NoError(t, cfg.Validate())

	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	h := application.metricsSrv.Handler
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/metrics", "# HELP lb_backend_healthy"},
		{"/debug/pprof/", "pprof"},
		{"/debug/pprof/goroutine?debug=1", "goroutine"},
		{"/debug/pprof/cmdline", os.Args[0]},
		{"/debug/pprof/symbol", "num_symbols"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		require.Equalf(t, http.StatusOK, rec.Code, "GET %s must be served on the metrics listener", tc.path)
		require.Truef(t, strings.Contains(rec.Body.String(), tc.want),
			"GET %s body must be the %q response, got %q", tc.path, tc.want, rec.Body.String())
	}
}
