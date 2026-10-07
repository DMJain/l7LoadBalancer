package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lbAdminStub stands in for one LB's metrics listener: GET
// /admin/reload/last returns the configured record, POST /admin/reload records
// the body and answers 422 like a real LB rejecting an invalid config.
type lbAdminStub struct {
	srv *httptest.Server

	mu          sync.Mutex
	last        string
	failLast    bool
	algorithm   string
	failMetrics bool
	posts       []string
}

func newLBAdminStub(t *testing.T) *lbAdminStub {
	t.Helper()
	a := &lbAdminStub{last: `{"result":"none"}`, algorithm: "round_robin"}
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/reload/last":
			if a.failLast {
				http.Error(w, "stub down", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, a.last)
		case r.Method == http.MethodGet && r.URL.Path == "/metrics":
			if a.failMetrics {
				http.Error(w, "stub down", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			_, _ = io.WriteString(w, "# HELP lb_active_requests In-flight requests.\n"+
				"# TYPE lb_active_requests gauge\n"+
				"lb_active_requests{backend=\"backend1\"} 3\n"+
				"# HELP lb_algorithm_info The selection algorithm currently in use (1).\n"+
				"# TYPE lb_algorithm_info gauge\n"+
				"lb_algorithm_info{algorithm=\""+a.algorithm+"\"} 1\n")
		case r.Method == http.MethodPost && r.URL.Path == "/admin/reload":
			body, _ := io.ReadAll(r.Body)
			a.posts = append(a.posts, string(body))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"result":"rejected","time":"2026-10-07T13:34:48Z","error":"config: unsupported algorithm \"p3c_ewma\""}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *lbAdminStub) postBodies() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.posts...)
}

func (a *lbAdminStub) setAlgorithm(algorithm string, fail bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.algorithm = algorithm
	a.failMetrics = fail
}

func (a *lbAdminStub) setLast(body string, fail bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.last = body
	a.failLast = fail
}

// TestStateIncludesEachLBsLastReload proves /api/state reads every LB's
// GET /admin/reload/last and reports it on that LB's entry, and that a failed
// read is reported rather than fatal (ADR-0025 decision 12).
func TestStateIncludesEachLBsLastReload(t *testing.T) {
	r := newRig(t)
	r.lbAdmins[0].setLast(`{"result":"rejected","time":"2026-10-07T13:34:48Z","error":"config: unsupported algorithm \"p3c_ewma\""}`, false)
	r.lbAdmins[3].setLast("", true)

	rec := r.do(t, http.MethodGet, "/api/state", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var st stateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st))
	require.Len(t, st.LBs, 4)

	rr := st.LBs[0]
	assert.Equal(t, "lb-roundrobin", rr.Name)
	require.NotNil(t, rr.LastReload)
	assert.Equal(t, "rejected", rr.LastReload.Result)
	assert.Equal(t, "2026-10-07T13:34:48Z", rr.LastReload.Time)
	assert.Equal(t, `config: unsupported algorithm "p3c_ewma"`, rr.LastReload.Error)

	require.NotNil(t, st.LBs[1].LastReload)
	assert.Equal(t, "none", st.LBs[1].LastReload.Result)

	assert.Nil(t, st.LBs[3].LastReload)
	assert.NotEmpty(t, st.LBs[3].LastReloadError, "a failed read must be reported")
}

// TestReloadInvalidPostsFixedBodyToActiveLB proves the invalid-reload action
// reaches only the active LB, sends the fixed config whose only invalid field is
// the algorithm, and passes the LB's answer back (ADR-0025 decision 13).
func TestReloadInvalidPostsFixedBodyToActiveLB(t *testing.T) {
	r := newRig(t) // every generator targets lb-roundrobin

	rec := r.do(t, http.MethodPost, "/api/reload-invalid", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var out reloadInvalidResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "lb-roundrobin", out.LB)
	assert.Equal(t, "rejected", out.Result)
	assert.Contains(t, out.Error, `unsupported algorithm "p3c_ewma"`)

	bodies := r.lbAdmins[0].postBodies()
	require.Len(t, bodies, 1)
	assert.Equal(t, invalidReloadBody, bodies[0], "the body is the fixed constant")
	assert.Contains(t, bodies[0], `algorithm: "p3c_ewma"`)
	for i, a := range r.lbAdmins[1:] {
		assert.Empty(t, a.postBodies(), "LB %d is not active and must not be called", i+1)
	}
}

// TestReloadInvalidWithoutActiveLBIsConflict proves that with no agreed active
// LB the action answers 409 and calls no LB.
func TestReloadInvalidWithoutActiveLBIsConflict(t *testing.T) {
	r := newRig(t)
	r.gens[2].mu.Lock()
	r.gens[2].target = "http://lb-p2c-ewma:8080"
	r.gens[2].mu.Unlock()

	rec := r.do(t, http.MethodPost, "/api/reload-invalid", "")
	assert.Equal(t, http.StatusConflict, rec.Code)
	for _, a := range r.lbAdmins {
		assert.Empty(t, a.postBodies())
	}
}

// TestReloadInvalidTakesNoInput proves a caller cannot supply the config: any
// field in the body is rejected before an LB is called.
func TestReloadInvalidTakesNoInput(t *testing.T) {
	r := newRig(t)

	rec := r.do(t, http.MethodPost, "/api/reload-invalid", `{"yaml":"algorithm: round_robin"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	for _, a := range r.lbAdmins {
		assert.Empty(t, a.postBodies())
	}
	assert.Equal(t, http.StatusOK, r.do(t, http.MethodPost, "/api/reload-invalid", "{}").Code)
}

// TestStateReportsEachLBsAlgorithm proves /api/state reports the algorithm
// each LB itself reports through lb_algorithm_info, so a hot reload shows on
// the page without any name mapping (ADR-0025 decision 16).
func TestStateReportsEachLBsAlgorithm(t *testing.T) {
	r := newRig(t)
	r.lbAdmins[0].setAlgorithm("p2c_ewma", false)
	r.lbAdmins[1].setAlgorithm("", true)

	rec := r.do(t, http.MethodGet, "/api/state", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var st stateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st))

	assert.Equal(t, "p2c_ewma", st.LBs[0].Algorithm)
	assert.Empty(t, st.LBs[1].Algorithm)
	assert.NotEmpty(t, st.LBs[1].AlgorithmError, "a failed read must be reported")
	assert.Equal(t, "round_robin", st.LBs[2].Algorithm)
}

// TestParseAlgorithm covers the exposition parsing on its own: the series whose
// value is 1 wins, other metrics and comments are ignored, and no series is an
// error.
func TestParseAlgorithm(t *testing.T) {
	got, err := parseAlgorithm(strings.NewReader("# TYPE lb_algorithm_info gauge\n" +
		"lb_requests_total{backend=\"a\"} 7\n" +
		"lb_algorithm_info{algorithm=\"least_conn\"} 1\n"))
	require.NoError(t, err)
	assert.Equal(t, "least_conn", got)

	_, err = parseAlgorithm(strings.NewReader("lb_requests_total 1\n"))
	assert.Error(t, err)
}

// TestNoActiveLBMetric proves the withdrawn demo_active_lb series stays gone
// (ADR-0025 decision 17) now that /metrics serves container stats.
func TestNoActiveLBMetric(t *testing.T) {
	r := newRig(t)
	assert.NotContains(t, r.do(t, http.MethodGet, "/metrics", "").Body.String(), "demo_active_lb")
}

// TestMetricsReportsContainerCPUAndMemory proves /metrics serves each
// allowlisted container's CPU time and memory from Docker's one-shot stats:
// the four backends and every LB, nothing else (ADR-0025 decision 18).
func TestMetricsReportsContainerCPUAndMemory(t *testing.T) {
	r := newRig(t)

	rec := r.do(t, http.MethodGet, "/metrics", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/plain")
	body := rec.Body.String()

	for _, c := range []string{"backend1", "backend4", "lb-roundrobin", "lb-p2c-ewma"} {
		assert.Contains(t, body, `demo_container_cpu_seconds_total{container="`+c+`"} 2.5`)
		assert.Contains(t, body, `demo_container_memory_bytes{container="`+c+`"} 100663296`)
		assert.Contains(t, body, `demo_container_stats_up{container="`+c+`"} 1`)
	}
	assert.Contains(t, body, "# TYPE demo_container_cpu_seconds_total counter")
	assert.Contains(t, body, "# TYPE demo_container_memory_bytes gauge")

	calls := r.engine.snapshot()
	assert.Contains(t, calls, "GET /containers/l7loadbalancer-demo-lb-roundrobin/stats")
	assert.Contains(t, calls, "GET /containers/l7loadbalancer-demo-backend2/stats")
	stats := 0
	for _, c := range calls {
		if strings.HasSuffix(c, "/stats") {
			stats++
		}
	}
	assert.Equal(t, 8, stats, "exactly the 4 backends and 4 LBs, nothing else")
	for _, q := range r.engine.statsQueries() {
		assert.Contains(t, q, "stream=false")
		assert.Contains(t, q, "one-shot=true")
	}
}

// TestMetricsReportsFailedStatsRead proves a failed Engine read is shown as
// up 0 with no value series, never as a zero value.
func TestMetricsReportsFailedStatsRead(t *testing.T) {
	r := newRig(t)
	r.engine.setFail(true)

	body := r.do(t, http.MethodGet, "/metrics", "").Body.String()
	assert.Contains(t, body, `demo_container_stats_up{container="backend1"} 0`)
	assert.NotContains(t, body, `demo_container_cpu_seconds_total{container="backend1"}`)
	assert.NotContains(t, body, `demo_container_memory_bytes{container="backend1"}`)
}

// TestNewServerRejectsLBAdminCountMismatch proves LB_ADMINS must pair with LBS.
func TestNewServerRejectsLBAdminCountMismatch(t *testing.T) {
	cfg := config{
		generators: []peer{{name: "gen1", url: "http://gen1:9090"}},
		backends:   []peer{{name: "backend1", url: "http://backend1:9091", container: "c1"}},
		lbs:        []peer{{name: "lb-roundrobin", url: "http://lb-roundrobin:8080"}},
		lbAdmins:   []peer{},
		grafanaURL: "http://127.0.0.1:3000",
		dockerHost: "http://127.0.0.1:1",
	}
	_, err := newServer(cfg, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LB_ADMINS")
}

// TestPageCarriesTheNewControls proves the page has the header, the
// invalid-reload action, Clear/Stop/Start, the reload instructions and the demo
// dashboard embed, and no algorithm buttons (ADR-0025 decision 17).
func TestPageCarriesTheNewControls(t *testing.T) {
	r := newRig(t)
	body := r.do(t, http.MethodGet, "/", "").Body.String()
	for _, want := range []string{
		"/api/reload-invalid", "Last reload", "Algorithm",
		`"Clear"`, `"Stop"`, `"Start"`, "/d/l7lb-demo/",
		"kill -s HUP lb", "demo/configs/lb.yaml",
	} {
		assert.True(t, strings.Contains(body, want), "page must contain %q", want)
	}
	assert.NotContains(t, body, `"Kill"`)
	assert.NotContains(t, body, `"Revive"`)
	assert.NotContains(t, body, `"/api/lb"`, "the page offers no algorithm switch; the algorithm changes by reload")
}
