package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/DMJain/l7LoadBalancer/internal/app"
	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
)

// mainLogCapture retains every record the reload loop and the application's
// reload operation log, so the loop tests assert on the frozen event/reason
// vocabulary by field.
type mainLogCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *mainLogCapture) Enabled(context.Context, slog.Level) bool { return true }

func (h *mainLogCapture) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, r.Clone())
	h.mu.Unlock()
	return nil
}

func (h *mainLogCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *mainLogCapture) WithGroup(string) slog.Handler      { return h }

func (h *mainLogCapture) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]slog.Record(nil), h.records...)
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

// countEventReason returns how many captured records match event and reason.
func (h *mainLogCapture) countEventReason(event, reason string) int {
	n := 0
	for _, r := range h.snapshot() {
		e, okE := recordString(r, "event")
		reasonVal, okR := recordString(r, "reason")
		if okE && okR && e == event && reasonVal == reason {
			n++
		}
	}
	return n
}

// countEvent returns how many captured records carry the event value.
func (h *mainLogCapture) countEvent(event string) int {
	n := 0
	for _, r := range h.snapshot() {
		if e, ok := recordString(r, "event"); ok && e == event {
			n++
		}
	}
	return n
}

// recordFieldContains reports whether any captured record matching event
// carries want as a value of key (an aggregated []string or a string).
func (h *mainLogCapture) recordFieldContains(event, key, want string) bool {
	for _, r := range h.snapshot() {
		if e, ok := recordString(r, "event"); !ok || e != event {
			continue
		}
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
		if found {
			return true
		}
	}
	return false
}

// loopTestConfig builds a validated loop-test config: round-robin, every
// listener on :0, and the given backends.
func loopTestConfig(backends []config.BackendConfig) *config.Config {
	probeInterval := time.Second
	probeTimeout := time.Second
	cooldown := time.Minute
	metricsListen := ":0"
	healthListen := ":0"
	return &config.Config{
		Listen:         ":0",
		Algorithm:      config.AlgorithmRoundRobin,
		Health:         config.HealthConfig{ProbeInterval: &probeInterval, ProbeTimeout: &probeTimeout},
		Circuit:        config.CircuitConfig{Cooldown: &cooldown},
		Metrics:        config.MetricsConfig{Listen: &metricsListen},
		HealthEndpoint: config.HealthEndpointConfig{Listen: &healthListen},
		Backends:       backends,
	}
}

// writeConfigFile marshals cfg to YAML at path.
func writeConfigFile(t *testing.T, path string, cfg *config.Config) {
	t.Helper()
	data, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))
}

// serveBackend starts an httptest backend answering 200 with id.
func serveBackend(t *testing.T, id string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(id))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// startReloadLoop runs reloadLoop on a cancelable context and returns the
// context's cancel function; the loop exits when it is called.
func startReloadLoop(t *testing.T, configPath string, sigCh <-chan os.Signal, application *app.App, log *slog.Logger) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		reloadLoop(ctx, configPath, sigCh, application, log)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("reload loop did not exit after context cancellation")
		}
	})
	return cancel
}

// TestReloadLoopSuccess proves one SIGHUP re-reads the file, parses, validates,
// and applies a backend-list change.
func TestReloadLoopSuccess(t *testing.T) {
	a := serveBackend(t, "backend-a")
	b := serveBackend(t, "backend-b")
	c := serveBackend(t, "backend-c")

	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := loopTestConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-b", URL: b.URL},
	})
	require.NoError(t, cfg.Validate())
	writeConfigFile(t, path, cfg)

	capture := &mainLogCapture{}
	log := slog.New(capture)
	application, err := app.Build(cfg, log)
	require.NoError(t, err)

	cfg2 := loopTestConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-c", URL: c.URL},
	})
	require.NoError(t, cfg2.Validate())
	writeConfigFile(t, path, cfg2)

	sigCh := make(chan os.Signal, 1)
	startReloadLoop(t, path, sigCh, application, log)

	sigCh <- syscall.SIGHUP

	require.Eventually(t, func() bool {
		return capture.countEvent(logger.EventConfigReloaded) == 1
	}, 3*time.Second, 10*time.Millisecond, "one SIGHUP must reload once")

	names := make([]string, 0, 2)
	for _, b := range application.Registry().All() {
		names = append(names, b.Name)
	}
	assert.Equal(t, []string{"backend-a", "backend-c"}, names)
}

// TestReloadLoopParseFailureKeepsServing proves malformed YAML is rejected with
// one config-reload-failed/parse_error line and the previous config keeps
// serving.
func TestReloadLoopParseFailureKeepsServing(t *testing.T) {
	a := serveBackend(t, "backend-a")
	b := serveBackend(t, "backend-b")

	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := loopTestConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-b", URL: b.URL},
	})
	require.NoError(t, cfg.Validate())
	writeConfigFile(t, path, cfg)

	capture := &mainLogCapture{}
	log := slog.New(capture)
	application, err := app.Build(cfg, log)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(path, []byte("listen: [not: valid"), 0o644))

	sigCh := make(chan os.Signal, 1)
	startReloadLoop(t, path, sigCh, application, log)
	sigCh <- syscall.SIGHUP

	require.Eventually(t, func() bool {
		return capture.countEventReason(logger.EventConfigReloadFailed, logger.ReasonParseError) == 1
	}, 3*time.Second, 10*time.Millisecond, "malformed YAML must log a parse_error reload failure")

	assert.Equal(t, []string{"backend-a", "backend-b"}, registryNames(application))
	assertServing(t, application)
}

// TestReloadLoopValidationFailureKeepsServing proves a parseable but invalid
// config is rejected with one config-reload-failed/validation_error line.
func TestReloadLoopValidationFailureKeepsServing(t *testing.T) {
	a := serveBackend(t, "backend-a")
	b := serveBackend(t, "backend-b")

	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := loopTestConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-b", URL: b.URL},
	})
	require.NoError(t, cfg.Validate())
	writeConfigFile(t, path, cfg)

	capture := &mainLogCapture{}
	log := slog.New(capture)
	application, err := app.Build(cfg, log)
	require.NoError(t, err)

	dup := loopTestConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-a", URL: b.URL},
	})
	writeConfigFile(t, path, dup)

	sigCh := make(chan os.Signal, 1)
	startReloadLoop(t, path, sigCh, application, log)
	sigCh <- syscall.SIGHUP

	require.Eventually(t, func() bool {
		return capture.countEventReason(logger.EventConfigReloadFailed, logger.ReasonValidationError) == 1
	}, 3*time.Second, 10*time.Millisecond, "an invalid config must log a validation_error reload failure")

	assert.Equal(t, []string{"backend-a", "backend-b"}, registryNames(application))
	assertServing(t, application)
}

// TestReloadLoopNonBackendChangeNamingField proves a change to a non-backend
// field is rejected whole, with one config-reload-failed line naming the field,
// and the previous config keeps serving.
func TestReloadLoopNonBackendChangeNamingField(t *testing.T) {
	a := serveBackend(t, "backend-a")
	b := serveBackend(t, "backend-b")

	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := loopTestConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-b", URL: b.URL},
	})
	require.NoError(t, cfg.Validate())
	writeConfigFile(t, path, cfg)

	capture := &mainLogCapture{}
	log := slog.New(capture)
	application, err := app.Build(cfg, log)
	require.NoError(t, err)

	cfg2 := loopTestConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-b", URL: b.URL},
	})
	cfg2.Listen = ":9999"
	require.NoError(t, cfg2.Validate())
	writeConfigFile(t, path, cfg2)

	sigCh := make(chan os.Signal, 1)
	startReloadLoop(t, path, sigCh, application, log)
	sigCh <- syscall.SIGHUP

	require.Eventually(t, func() bool {
		return capture.countEventReason(logger.EventConfigReloadFailed, logger.ReasonNonBackendChange) == 1
	}, 3*time.Second, 10*time.Millisecond, "a non-backend change must log a reload failure")
	assert.True(t, capture.recordFieldContains(logger.EventConfigReloadFailed, "fields", "listen"),
		"the failure must name the changed field")

	assert.Equal(t, []string{"backend-a", "backend-b"}, registryNames(application))
	assertServing(t, application)
}

// TestReloadLoopSignalBurstCollapses proves several SIGHUPs already pending
// collapse into a single reload.
func TestReloadLoopSignalBurstCollapses(t *testing.T) {
	a := serveBackend(t, "backend-a")
	b := serveBackend(t, "backend-b")
	c := serveBackend(t, "backend-c")

	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := loopTestConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-b", URL: b.URL},
	})
	require.NoError(t, cfg.Validate())
	writeConfigFile(t, path, cfg)

	capture := &mainLogCapture{}
	log := slog.New(capture)
	application, err := app.Build(cfg, log)
	require.NoError(t, err)

	cfg2 := loopTestConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-c", URL: c.URL},
	})
	require.NoError(t, cfg2.Validate())
	writeConfigFile(t, path, cfg2)

	sigCh := make(chan os.Signal, 5)
	for i := 0; i < 5; i++ {
		sigCh <- syscall.SIGHUP
	}

	startReloadLoop(t, path, sigCh, application, log)

	require.Eventually(t, func() bool {
		return capture.countEvent(logger.EventConfigReloaded) >= 1
	}, 3*time.Second, 10*time.Millisecond, "a burst must trigger at least one reload")

	// The loop drains every pending signal before reloading, so a burst is one
	// reload; no second reload is ever queued behind it.
	require.Never(t, func() bool {
		return capture.countEvent(logger.EventConfigReloaded) > 1
	}, 200*time.Millisecond, 10*time.Millisecond, "a burst must collapse to one reload")
}

// registryNames returns the registry's backend names in order.
func registryNames(application *app.App) []string {
	all := application.Registry().All()
	names := make([]string, len(all))
	for i, b := range all {
		names[i] = b.Name
	}
	return names
}

// assertServing drives one request through the handler and requires a 200.
func assertServing(t *testing.T, application *app.App) {
	t.Helper()
	rec := httptest.NewRecorder()
	application.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, rec.Code, "the previous config must keep serving")
}
