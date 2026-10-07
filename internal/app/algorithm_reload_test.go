package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
)

// withAlgorithm returns a shallow copy of cfg with its algorithm replaced. The
// copy shares cfg's validated pointer fields, so it is already valid.
func withAlgorithm(cfg *config.Config, algorithm string) *config.Config {
	c := *cfg
	c.Algorithm = algorithm
	return &c
}

// servedBy sends n requests from one client address through h and returns the
// distinct backend ids that answered.
func servedBy(t *testing.T, h http.Handler, n int) map[string]int {
	t.Helper()
	seen := map[string]int{}
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.0.2.10:40000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		seen[rec.Body.String()]++
	}
	return seen
}

func algorithmGauge(t *testing.T, application *App, want string) {
	t.Helper()
	expected := "\n# HELP lb_algorithm_info The selection algorithm currently in use (1).\n" +
		"# TYPE lb_algorithm_info gauge\n" +
		"lb_algorithm_info{algorithm=\"" + want + "\"} 1\n"
	require.NoError(t, testutil.GatherAndCompare(
		application.Collector().Registry(), strings.NewReader(expected), "lb_algorithm_info"))
}

// TestReloadSwapsAlgorithm is the core property: a reload that changes
// only the algorithm is applied, not rejected. Routing changes observably
// (round-robin spreads one client over both backends; consistent hashing pins
// it to one), the reload logs both names, the gauge moves, and the last reload
// reads success (ADR-0015 decisions 18–21).
func TestReloadSwapsAlgorithm(t *testing.T) {
	silenceDefault(t)

	a := serveFixed(t, "backend-a")
	b := serveFixed(t, "backend-b")
	cfg := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-b", URL: b.URL},
	})
	require.NoError(t, cfg.Validate())
	capture := &reloadLogCapture{}
	application, err := Build(cfg, slog.New(capture))
	require.NoError(t, err)

	algorithmGauge(t, application, config.AlgorithmRoundRobin)
	assert.Len(t, servedBy(t, application.Handler(), 8), 2, "round-robin spreads one client over both backends")

	next := withAlgorithm(cfg, config.AlgorithmConsistentHash)
	require.NoError(t, application.Reload(context.Background(), next))

	assert.Len(t, servedBy(t, application.Handler(), 8), 1, "consistent hashing pins one client to one backend")
	assert.Same(t, next, application.LoadedConfig())
	assert.Equal(t, ReloadResultSuccess, application.LastReload().Result)
	algorithmGauge(t, application, config.AlgorithmConsistentHash)

	reloaded := capture.withEvent(logger.EventConfigReloaded)
	require.Len(t, reloaded, 1)
	prev, ok := recordString(reloaded[0], "previous_algorithm")
	require.True(t, ok, "the reload line must name the previous algorithm")
	assert.Equal(t, config.AlgorithmRoundRobin, prev)
	cur, ok := recordString(reloaded[0], "algorithm")
	require.True(t, ok)
	assert.Equal(t, config.AlgorithmConsistentHash, cur)
	assert.Empty(t, capture.withEvent(logger.EventConfigReloadFailed))
}

// TestReloadSameAlgorithmLogsNoAlgorithmChange proves a backend-only reload
// keeps its existing log line: no algorithm fields when nothing changed.
func TestReloadSameAlgorithmLogsNoAlgorithmChange(t *testing.T) {
	silenceDefault(t)

	a := serveFixed(t, "backend-a")
	cfg := testConfig([]config.BackendConfig{{Name: "backend-a", URL: a.URL}})
	require.NoError(t, cfg.Validate())
	capture := &reloadLogCapture{}
	application, err := Build(cfg, slog.New(capture))
	require.NoError(t, err)

	require.NoError(t, application.Reload(context.Background(), withAlgorithm(cfg, cfg.Algorithm)))
	reloaded := capture.withEvent(logger.EventConfigReloaded)
	require.Len(t, reloaded, 1)
	_, ok := recordString(reloaded[0], "previous_algorithm")
	assert.False(t, ok)
	algorithmGauge(t, application, config.AlgorithmRoundRobin)
}

// TestReloadChangesAlgorithmAndBackendsTogether proves one reload can change
// both: the backend set is applied and the new algorithm runs over it.
func TestReloadChangesAlgorithmAndBackendsTogether(t *testing.T) {
	silenceDefault(t)

	a := serveFixed(t, "backend-a")
	b := serveFixed(t, "backend-b")
	c := serveFixed(t, "backend-c")
	cfg := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-b", URL: b.URL},
	})
	require.NoError(t, cfg.Validate())
	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	next := withBackends(cfg, []config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-c", URL: c.URL},
	})
	next.Algorithm = config.AlgorithmLeastConn
	require.NoError(t, application.Reload(context.Background(), next))

	assert.Equal(t, []string{"backend-a", "backend-c"}, backendNames(application.Registry().All()))
	algorithmGauge(t, application, config.AlgorithmLeastConn)
	// backend-c starts unhealthy until probed (ADR-0015 decision 10), so the
	// new selector serves from backend-a meanwhile; every request succeeds.
	served := servedBy(t, application.Handler(), 4)
	assert.Equal(t, 4, served["backend-a"]+served["backend-c"])
}

// TestReloadUnknownAlgorithmStillRejected proves the swap did not loosen
// validation: an unknown algorithm is rejected and routing is unchanged.
func TestReloadUnknownAlgorithmStillRejected(t *testing.T) {
	silenceDefault(t)

	a := serveFixed(t, "backend-a")
	b := serveFixed(t, "backend-b")
	cfg := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-b", URL: b.URL},
	})
	require.NoError(t, cfg.Validate())
	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	bad := withAlgorithm(cfg, "p3c_ewma")
	_, err = application.ReloadFrom(context.Background(), func() (*config.Config, error) { return bad, nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported algorithm "p3c_ewma"`)
	assert.Same(t, cfg, application.LoadedConfig())
	algorithmGauge(t, application, config.AlgorithmRoundRobin)
	assert.Len(t, servedBy(t, application.Handler(), 8), 2, "still round-robin")
}

// TestAlgorithmReloadUnderTrafficDropsNothing drives continuous traffic
// through the real client listener from several clients while the algorithm is
// reloaded through every value many times, half by Reload and half through the
// admin endpoint. Every request must succeed (ADR-0015 decision 19). The
// backends hold each request briefly so swaps land while requests are in
// flight.
func TestAlgorithmReloadUnderTrafficDropsNothing(t *testing.T) {
	silenceDefault(t)

	slow := func(id string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(2 * time.Millisecond)
			_, _ = io.WriteString(w, id)
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	backends := []config.BackendConfig{
		{Name: "backend-a", URL: slow("backend-a").URL},
		{Name: "backend-b", URL: slow("backend-b").URL},
		{Name: "backend-c", URL: slow("backend-c").URL},
	}
	application, cfg, base := adminTestApp(t, &reloadLogCapture{}, backends)
	client := &http.Client{Timeout: 5 * time.Second}

	stop := make(chan struct{})
	var ok, failed atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < 12; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				resp, err := client.Get("http://" + cfg.Listen + "/")
				if err != nil {
					failed.Add(1)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					ok.Add(1)
				} else {
					failed.Add(1)
				}
			}
		}()
	}

	algorithms := []string{
		config.AlgorithmP2CEWMA, config.AlgorithmConsistentHash,
		config.AlgorithmLeastConn, config.AlgorithmRoundRobin,
	}
	for i := 0; i < 60; i++ {
		next := withAlgorithm(cfg, algorithms[i%len(algorithms)])
		if i%2 == 0 {
			require.NoError(t, application.Reload(context.Background(), next))
		} else {
			code, out := postReload(t, base, yamlBody(t, next))
			require.Equal(t, http.StatusOK, code, "admin reload failed: %s", out.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(stop)
	wg.Wait()

	assert.Zero(t, failed.Load(), "no request may fail while the algorithm is swapped")
	assert.Greater(t, ok.Load(), int64(500), "traffic must have flowed throughout")
	algorithmGauge(t, application, config.AlgorithmRoundRobin)
}
