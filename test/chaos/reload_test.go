package chaos_test

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
)

// ServeGated installs a handler that reports its backend's identity on entered
// (non-blocking) and then blocks until release is closed, so a test can hold a
// proxied request open across a reload. Non-blocking entry means health probes
// that happen to hit the handler never stall a probe goroutine.
func (fb *flippableBackend) ServeGated(entered chan<- string, release <-chan struct{}) {
	h := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case entered <- fb.id:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, fb.id)
	}))
	fb.handler.Store(&h)
}

// reloadChaosConfig is the gated in-flight test's config: the active checker's
// cadence is pushed past the test window so the only traffic to the gated
// backends is the requests the test deliberately holds open (mirroring
// circuitChaosConfig). A reload-added backend still probes once immediately, so
// admission is unaffected.
func reloadChaosConfig(fbs []*flippableBackend) *config.Config {
	probeInterval := time.Hour
	probeTimeout := time.Second
	cooldown := chaosCooldown
	drainWindow := config.DefaultDrainWindow
	return &config.Config{
		Listen:         ":0",
		Algorithm:      config.AlgorithmRoundRobin,
		Health:         config.HealthConfig{ProbeInterval: &probeInterval, ProbeTimeout: &probeTimeout},
		Circuit:        config.CircuitConfig{Cooldown: &cooldown},
		Metrics:        config.MetricsConfig{Listen: zeroListen()},
		HealthEndpoint: config.HealthEndpointConfig{Listen: zeroListen()},
		Reload:         config.ReloadConfig{DrainWindow: &drainWindow},
		Backends:       backendConfigs(fbs),
	}
}

// containsBackend reports whether bs holds a backend with the given name.
func containsBackend(bs []*backend.Backend, name string) bool {
	for _, b := range bs {
		if b.Name == name {
			return true
		}
	}
	return false
}

// backendByName returns the named backend from the registry, failing the test
// if it is absent.
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

// TestChaosReloadInFlightRequestsSurvive proves a reload that adds a backend
// while requests are in flight on unchanged backends drops zero of them, and
// that the unchanged backends keep their instance (ADR-0015 decisions 6 and 8).
func TestChaosReloadInFlightRequestsSurvive(t *testing.T) {
	fbs := newFlippableBackends(t, 2)
	a := assemble(t, reloadChaosConfig(fbs))
	assertAllHealthy(t, a, fbs)

	origByName := map[string]*backend.Backend{}
	for _, b := range a.reg.All() {
		origByName[b.Name] = b
	}

	release := make(chan struct{})
	entered := make(chan string, 2)
	fbs[0].ServeGated(entered, release)
	fbs[1].ServeGated(entered, release)

	type outcome struct {
		status int
		body   string
	}
	results := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			status, body := doRequest(a.handler)
			results <- outcome{status, body}
		}()
	}

	// Both requests must be held open, one per backend, before the reload.
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case id := <-entered:
			seen[id] = true
		case <-time.After(eventuallyDeadline):
			t.Fatal("in-flight requests did not reach both backends")
		}
	}
	require.True(t, seen["backend-a"] && seen["backend-b"])

	// Reload adds a backend and keeps the two holding requests' backends.
	c := newFlippableBackend(t, "backend-c")
	require.NoError(t, a.application.Reload(context.Background(),
		reloadChaosConfig([]*flippableBackend{fbs[0], fbs[1], c})))

	close(release)
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			require.Equal(t, http.StatusOK, r.status, "an in-flight request must survive the reload")
		case <-time.After(eventuallyDeadline):
			t.Fatal("an in-flight request did not complete")
		}
	}

	for name, orig := range origByName {
		require.Same(t, orig, backendByName(t, a.reg, name),
			"an unchanged backend must keep its instance across a reload")
	}
	require.Eventually(t, func() bool { return len(a.reg.Selectable()) == 3 },
		eventuallyDeadline, eventuallyTick, "the added backend must become selectable")
}

// TestChaosReloadAddedBackendAdmittedAfterFirstProbe proves an added backend
// starts unhealthy and becomes selectable only after exactly one successful
// probe, logged with reason initial_probe (ADR-0015 decision 10).
func TestChaosReloadAddedBackendAdmittedAfterFirstProbe(t *testing.T) {
	fbs := newFlippableBackends(t, 2)
	a := assemble(t, chaosConfig(fbs))

	d := newFlippableBackend(t, "backend-d")
	d.Serve500()
	require.NoError(t, a.application.Reload(context.Background(),
		chaosConfig([]*flippableBackend{fbs[0], fbs[1], d})))

	require.Never(t, func() bool { return containsBackend(a.reg.Selectable(), d.id) },
		300*time.Millisecond, eventuallyTick, "a failing added backend must stay unselectable")
	assertTransitionNotLogged(t, a.logs, logger.EventHealthReinstated, d.id, logger.ReasonInitialProbe)

	d.Serve200()
	require.Eventually(t, func() bool { return containsBackend(a.reg.Selectable(), d.id) },
		eventuallyDeadline, eventuallyTick, "one successful probe must admit the added backend")

	assertTransitionLogged(t, a.logs, logger.EventHealthReinstated, d.id, logger.ReasonInitialProbe)
	require.Equal(t, 1, a.logs.eventCount(logger.EventHealthReinstated, d.id))
	assertGauge(t, a.collector, "lb_backend_healthy", healthGaugeLabels(d.id), 1)
}

// TestChaosReloadRemovedIdleBackendLeavesSeries proves an idle removed backend
// leaves All and Selectable and its healthy and circuit-state series are
// deleted, while its active-connections series is deliberately left in place
// (the T3 → T4 hand-off).
func TestChaosReloadRemovedIdleBackendLeavesSeries(t *testing.T) {
	fbs := newFlippableBackends(t, 2)
	a := assemble(t, chaosConfig(fbs))

	b := fbs[1]
	require.NoError(t, a.application.Reload(context.Background(), chaosConfig(fbs[:1])))

	require.False(t, containsBackend(a.reg.All(), b.id), "a removed backend leaves All")
	require.False(t, containsBackend(a.reg.Selectable(), b.id), "a removed backend leaves Selectable")

	_, healthy := gaugeValueOK(a.collector, "lb_backend_healthy", healthGaugeLabels(b.id))
	require.False(t, healthy, "the removed backend's healthy series must be deleted")
	_, circuit := gaugeValueOK(a.collector, "lb_circuit_state", circuitStateLabels(b.id, "closed"))
	require.False(t, circuit, "the removed backend's circuit series must be deleted")

	active, ok := gaugeValueOK(a.collector, "lb_active_connections", map[string]string{"backend": b.id})
	require.True(t, ok, "the removed backend's active-connections series must remain until drain (T4)")
	require.Equal(t, 0.0, active)
}

// TestChaosReloadSuccessiveDiffReAddsFresh proves each reload is diffed against
// the currently loaded config: a backend removed by one reload and re-added by
// the next returns as a fresh instance (ADR-0015 decision 12).
func TestChaosReloadSuccessiveDiffReAddsFresh(t *testing.T) {
	fbs := newFlippableBackends(t, 2)
	a := assemble(t, chaosConfig(fbs))
	origB := backendByName(t, a.reg, "backend-b")

	c := newFlippableBackend(t, "backend-c")
	require.NoError(t, a.application.Reload(context.Background(),
		chaosConfig([]*flippableBackend{fbs[0], c})))
	require.NoError(t, a.application.Reload(context.Background(),
		chaosConfig([]*flippableBackend{fbs[0], fbs[1]})))

	freshB := backendByName(t, a.reg, "backend-b")
	require.NotSame(t, origB, freshB, "re-adding a removed identity yields a fresh instance")
	require.Len(t, a.reg.All(), 2)
}
