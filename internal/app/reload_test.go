package app

import (
	"context"
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

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
)

// reloadLogCapture retains every record the reload operation and its
// subsystems log, so app-level reload tests assert on the frozen event/reason
// vocabulary by field rather than by parsing text.
type reloadLogCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *reloadLogCapture) Enabled(context.Context, slog.Level) bool { return true }

func (h *reloadLogCapture) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, r.Clone())
	h.mu.Unlock()
	return nil
}

func (h *reloadLogCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *reloadLogCapture) WithGroup(string) slog.Handler      { return h }

func (h *reloadLogCapture) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]slog.Record(nil), h.records...)
}

// withEvent returns every captured record carrying the given event value.
func (h *reloadLogCapture) withEvent(event string) []slog.Record {
	var out []slog.Record
	for _, r := range h.snapshot() {
		if v, ok := recordString(r, "event"); ok && v == event {
			out = append(out, r)
		}
	}
	return out
}

// recordString returns one record attribute's string value.
func recordString(r slog.Record, key string) (string, bool) {
	var v string
	var ok bool
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v, ok = a.Value.String(), true
			return false
		}
		return true
	})
	return v, ok
}

// recordInt returns one record attribute's integer value.
func recordInt(r slog.Record, key string) (int64, bool) {
	var v int64
	var ok bool
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			switch a.Value.Kind() {
			case slog.KindInt64:
				v, ok = a.Value.Int64(), true
			case slog.KindUint64:
				v, ok = int64(a.Value.Uint64()), true
			}
			return false
		}
		return true
	})
	return v, ok
}

// recordStringSlice appends every value of a repeated/aggregated string
// attribute; slog renders a []string attr as a KindAny, so it is recovered via
// the resolved value when possible.
func recordHasStringValue(r slog.Record, key, want string) bool {
	found := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key != key {
			return true
		}
		if a.Value.Kind() == slog.KindAny {
			if s, ok := a.Value.Any().([]string); ok {
				for _, v := range s {
					if v == want {
						found = true
					}
				}
			}
		}
		if a.Value.String() == want {
			found = true
		}
		return true
	})
	return found
}

// serveFixed starts an httptest backend that always answers 200 with id.
func serveFixed(t *testing.T, id string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(id))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// backendNames maps a backend slice to its names in order.
func backendNames(bs []*backend.Backend) []string {
	names := make([]string, len(bs))
	for i, b := range bs {
		names[i] = b.Name
	}
	return names
}

// TestReloadRejectsNonBackendChange proves a reload whose file differs in any
// non-backend field is rejected whole: the reload returns an error, the
// previously loaded config keeps serving, and one config-reload-failed line
// names the offending field.
func TestReloadRejectsNonBackendChange(t *testing.T) {
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

	cfg2 := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-b", URL: b.URL},
	})
	cfg2.Listen = ":9999"
	require.NoError(t, cfg2.Validate())

	err = application.Reload(context.Background(), cfg2)
	require.Error(t, err, "a non-backend change must reject the reload")

	assert.Same(t, cfg, application.LoadedConfig(), "the loaded config must be untouched")
	assert.Equal(t, []string{"backend-a", "backend-b"}, backendNames(application.Registry().All()))

	failed := capture.withEvent(logger.EventConfigReloadFailed)
	require.Len(t, failed, 1, "exactly one config reload failed line")
	reason, ok := recordString(failed[0], "reason")
	require.True(t, ok)
	assert.Equal(t, logger.ReasonNonBackendChange, reason)
	assert.True(t, recordHasStringValue(failed[0], "fields", "listen"),
		"the failed line must name the changed field")

	// The previous config keeps serving.
	rec := httptest.NewRecorder()
	application.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestReloadRejectsServerTimeoutChange proves a reload that changes the
// server.read_timeout bound is rejected whole and names "server" (S4.T7).
func TestReloadRejectsServerTimeoutChange(t *testing.T) {
	silenceDefault(t)

	a := serveFixed(t, "backend-a")
	cfg := testConfig([]config.BackendConfig{{Name: "backend-a", URL: a.URL}})
	require.NoError(t, cfg.Validate())

	capture := &reloadLogCapture{}
	application, err := Build(cfg, slog.New(capture))
	require.NoError(t, err)

	cfg2 := testConfig([]config.BackendConfig{{Name: "backend-a", URL: a.URL}})
	longer := 90 * time.Second
	cfg2.Server.ReadTimeout = &longer
	require.NoError(t, cfg2.Validate())

	err = application.Reload(context.Background(), cfg2)
	require.Error(t, err, "a server change must reject the reload")
	assert.Same(t, cfg, application.LoadedConfig(), "the loaded config must be untouched")

	failed := capture.withEvent(logger.EventConfigReloadFailed)
	require.Len(t, failed, 1, "exactly one config reload failed line")
	assert.True(t, recordHasStringValue(failed[0], "fields", "server"),
		"the failed line must name the changed server section")
}

// TestReloadAddsRemovesAndLogs proves the happy path: the registry follows the
// new file's order, the loaded-config record is replaced, and one INFO
// config-reloaded line carries the added/removed/unchanged counts.
func TestReloadAddsRemovesAndLogs(t *testing.T) {
	silenceDefault(t)

	a := serveFixed(t, "backend-a")
	b := serveFixed(t, "backend-b")
	c := serveFixed(t, "backend-c")
	cfg := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-b", URL: b.URL},
	})
	require.NoError(t, cfg.Validate())

	capture := &reloadLogCapture{}
	application, err := Build(cfg, slog.New(capture))
	require.NoError(t, err)

	cfg2 := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-c", URL: c.URL},
	})
	require.NoError(t, cfg2.Validate())

	require.NoError(t, application.Reload(context.Background(), cfg2))

	assert.Same(t, cfg2, application.LoadedConfig())
	assert.Equal(t, []string{"backend-a", "backend-c"}, backendNames(application.Registry().All()))

	reloaded := capture.withEvent(logger.EventConfigReloaded)
	require.Len(t, reloaded, 1, "exactly one config reloaded line")
	assert.Equal(t, slog.LevelInfo, reloaded[0].Level)
	for key, want := range map[string]int64{"added": 1, "removed": 1, "unchanged": 1} {
		got, ok := recordInt(reloaded[0], key)
		require.Truef(t, ok, "missing %q attribute", key)
		assert.Equalf(t, want, got, "%s count", key)
	}
}

// TestReloadBlueGreenLogsWarn proves a reload that replaces every backend is
// permitted and logged at WARN because unchanged is zero (ADR-0015 decision
// 11).
func TestReloadBlueGreenLogsWarn(t *testing.T) {
	silenceDefault(t)

	a := serveFixed(t, "backend-a")
	c := serveFixed(t, "backend-c")
	cfg := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-b", URL: "http://127.0.0.1:1"},
	})
	require.NoError(t, cfg.Validate())

	capture := &reloadLogCapture{}
	application, err := Build(cfg, slog.New(capture))
	require.NoError(t, err)

	cfg2 := testConfig([]config.BackendConfig{
		{Name: "backend-c", URL: c.URL},
	})
	require.NoError(t, cfg2.Validate())

	require.NoError(t, application.Reload(context.Background(), cfg2))

	reloaded := capture.withEvent(logger.EventConfigReloaded)
	require.Len(t, reloaded, 1)
	assert.Equal(t, slog.LevelWarn, reloaded[0].Level, "unchanged=0 must log at WARN")
	unchanged, ok := recordInt(reloaded[0], "unchanged")
	require.True(t, ok)
	assert.Zero(t, unchanged)
}

// TestReloadSeedsAddedAndDeletesRemovedSeries proves the collector hook-up: an
// added backend's series are seeded (active connections 0, healthy 0, circuit
// closed), a removed backend's healthy and circuit-state series are deleted,
// and its active-connections series is deliberately left in place (the T3 → T4
// hand-off).
func TestReloadSeedsAddedAndDeletesRemovedSeries(t *testing.T) {
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

	// A dead but valid URL keeps the added backend deterministically unhealthy,
	// so its seeded series are not raced by a first successful probe.
	cfg2 := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-c", URL: "http://127.0.0.1:1"},
	})
	require.NoError(t, cfg2.Validate())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, application.Reload(ctx, cfg2))

	const wantHealthy = `
# HELP lb_backend_healthy Whether a backend is healthy (1) or unhealthy (0).
# TYPE lb_backend_healthy gauge
lb_backend_healthy{backend="backend-a"} 1
lb_backend_healthy{backend="backend-c"} 0
`
	require.NoError(t, testutil.GatherAndCompare(
		application.Collector().Registry(), strings.NewReader(wantHealthy), "lb_backend_healthy"))

	const wantCircuit = `
# HELP lb_circuit_state Circuit breaker state per backend as a label enum; exactly one of closed/open/half_open is 1.
# TYPE lb_circuit_state gauge
lb_circuit_state{backend="backend-a",state="closed"} 1
lb_circuit_state{backend="backend-a",state="half_open"} 0
lb_circuit_state{backend="backend-a",state="open"} 0
lb_circuit_state{backend="backend-c",state="closed"} 1
lb_circuit_state{backend="backend-c",state="half_open"} 0
lb_circuit_state{backend="backend-c",state="open"} 0
`
	require.NoError(t, testutil.GatherAndCompare(
		application.Collector().Registry(), strings.NewReader(wantCircuit), "lb_circuit_state"))

	// S4.T4's drain deletes the removed backend's active-connections series at
	// drain completion; the unchanged backend's remains and the added one's is
	// seeded.
	const wantActive = `
# HELP lb_active_connections In-flight requests currently being served by a backend.
# TYPE lb_active_connections gauge
lb_active_connections{backend="backend-a"} 0
lb_active_connections{backend="backend-c"} 0
`
	require.Eventually(t, func() bool {
		return testutil.GatherAndCompare(
			application.Collector().Registry(), strings.NewReader(wantActive), "lb_active_connections") == nil
	}, 3*time.Second, 10*time.Millisecond, "the drained backend's active series must be deleted")
}

// TestReloadSuccessiveDiffComposes proves each reload is diffed against the
// currently loaded config, not the startup config: B, removed by the first
// reload, is re-added by the second and comes back as a fresh instance.
func TestReloadSuccessiveDiffComposes(t *testing.T) {
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

	origB := backendByName(t, application.Registry(), "backend-b")

	cfg2 := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-c", URL: c.URL},
	})
	require.NoError(t, cfg2.Validate())
	require.NoError(t, application.Reload(context.Background(), cfg2))

	cfg3 := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-b", URL: b.URL},
	})
	require.NoError(t, cfg3.Validate())
	require.NoError(t, application.Reload(context.Background(), cfg3))

	assert.Equal(t, []string{"backend-a", "backend-b"}, backendNames(application.Registry().All()))
	freshB := backendByName(t, application.Registry(), "backend-b")
	assert.NotSame(t, origB, freshB, "a re-added identity must be a fresh instance")
}

// TestReloadReadyzLiveAndStartupzLatched proves /readyz reflects the live
// selectable set across a blue/green reload (503 while every added backend is
// unproven, 200 once one probe succeeds) while /startupz stays latched at 200
// (ADR-0015 decision 10, ADR-0014 decision 4).
func TestReloadReadyzLiveAndStartupzLatched(t *testing.T) {
	silenceDefault(t)

	var dHealthy atomic.Bool // false: the added backend starts broken
	d := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if dHealthy.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(d.Close)

	a := serveFixed(t, "backend-a")
	fast := 10 * time.Millisecond
	cfg := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
	})
	cfg.Health.ProbeInterval = &fast
	require.NoError(t, cfg.Validate())

	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	application.checker.Start(ctx)

	require.Eventually(t, func() bool {
		return probeStatus(application.healthSrv.Handler, "/readyz") == http.StatusOK
	}, 3*time.Second, 10*time.Millisecond, "the startup fleet must be ready")
	require.Equal(t, http.StatusOK, probeStatus(application.healthSrv.Handler, "/startupz"))

	cfg2 := testConfig([]config.BackendConfig{{Name: "backend-d", URL: d.URL}})
	cfg2.Health.ProbeInterval = &fast
	require.NoError(t, cfg2.Validate())
	require.NoError(t, application.Reload(ctx, cfg2))

	assert.Equal(t, http.StatusServiceUnavailable, probeStatus(application.healthSrv.Handler, "/readyz"),
		"the blue/green window has nothing selectable")
	assert.Equal(t, http.StatusOK, probeStatus(application.healthSrv.Handler, "/startupz"),
		"startupz must stay latched across a reload")

	dHealthy.Store(true)
	require.Eventually(t, func() bool {
		return probeStatus(application.healthSrv.Handler, "/readyz") == http.StatusOK
	}, 3*time.Second, 10*time.Millisecond, "one successful probe must make the added backend selectable")
	assert.Equal(t, http.StatusOK, probeStatus(application.healthSrv.Handler, "/startupz"))
}

// backendByName returns the named backend from the registry's current
// snapshot, failing the test if it is absent.
func backendByName(t *testing.T, reg *backend.Registry, name string) *backend.Backend {
	t.Helper()
	for _, b := range reg.All() {
		if b.Name == name {
			return b
		}
	}
	t.Fatalf("backend %q not in registry", name)
	return nil
}

// probeStatus drives one request through an http.Handler and returns its
// status code.
func probeStatus(h http.Handler, path string) int {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code
}
