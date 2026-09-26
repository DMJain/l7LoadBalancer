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

// reloadChaosConfigWindow is reloadChaosConfig with an explicit drain window,
// so a test can make the window shorter than the hold it stages. Both the
// startup and the reloaded config must carry the same window: reload.drain_window
// is not reloadable (ADR-0016 decision 1), so a change would reject the reload.
func reloadChaosConfigWindow(fbs []*flippableBackend, window time.Duration) *config.Config {
	cfg := reloadChaosConfig(fbs)
	cfg.Reload.DrainWindow = &window
	return cfg
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

// TestChaosReloadRemovedIdleBackendDrains proves an idle removed backend drains
// immediately: it leaves All and Selectable, its healthy and circuit-state
// series are deleted at removal, and its active-connections series is deleted
// at drain completion with exactly one idle drained line (ADR-0016 decision 7).
func TestChaosReloadRemovedIdleBackendDrains(t *testing.T) {
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

	require.Eventually(t, func() bool {
		_, ok := gaugeValueOK(a.collector, "lb_active_connections", map[string]string{"backend": b.id})
		return !ok
	}, eventuallyDeadline, eventuallyTick, "the drained backend's active-connections series must be deleted")

	require.Eventually(t, func() bool {
		return a.logs.transitionCount(logger.EventBackendDrained, b.id, logger.ReasonIdle) == 1
	}, eventuallyDeadline, eventuallyTick, "the idle drain must be logged exactly once")
}

// TestChaosReloadDrainExitCriterion1000 is Sprint 4's first exit-criterion test:
// 1000 concurrent requests held open by gated backends — some on a backend that
// stays, some on one being removed — survive a reload that adds one backend and
// removes another with a drain window longer than the hold. All 1000 return
// 200; the removed backend drains idle; the unchanged backend keeps its
// instance and its EWMA state (ADR-0016 decision 7).
func TestChaosReloadDrainExitCriterion1000(t *testing.T) {
	const total = 1000

	fbs := newFlippableBackends(t, 2)
	a := assemble(t, reloadChaosConfig(fbs))

	// Warm backend-a so its EWMA state is non-zero before the reload: the reload
	// must carry that state across on the same instance. With two round-robin
	// backends the first request lands on backend-a.
	aBefore := backendByName(t, a.reg, "backend-a")
	status, _ := doRequest(a.handler)
	require.Equal(t, http.StatusOK, status)
	ewmaBefore := aBefore.EWMALatency()
	require.NotZero(t, ewmaBefore, "the warmup must have recorded a latency")

	release := make(chan struct{})
	entered := make(chan string, 4)
	fbs[0].ServeGated(entered, release)
	fbs[1].ServeGated(entered, release)

	results := make(chan int, total)
	for i := 0; i < total; i++ {
		go func() {
			status, _ := doRequest(a.handler)
			results <- status
		}()
	}

	// Every request must be in flight before the reload: the count is
	// incremented before dispatch, so all 1000 are then held by a gate.
	require.Eventually(t, func() bool {
		var held int64
		for _, b := range a.reg.All() {
			held += b.ActiveConns()
		}
		return held == total
	}, eventuallyDeadline, eventuallyTick, "all %d requests must be held open", total)

	// Reload adds backend-c and removes backend-b while all 1000 are in flight.
	// The default drain window is longer than the hold.
	c := newFlippableBackend(t, "backend-c")
	require.NoError(t, a.application.Reload(context.Background(),
		reloadChaosConfig([]*flippableBackend{fbs[0], c})))

	// The unchanged backend keeps its instance and its EWMA state: no request
	// on it has completed since the reload, since all are still gated.
	aAfter := backendByName(t, a.reg, "backend-a")
	require.Same(t, aBefore, aAfter, "the unchanged backend must keep its instance")
	require.Equal(t, ewmaBefore, aAfter.EWMALatency(), "the unchanged backend's EWMA state must survive")

	close(release)
	for i := 0; i < total; i++ {
		select {
		case status := <-results:
			require.Equal(t, http.StatusOK, status, "every in-flight request must survive the reload")
		case <-time.After(eventuallyDeadline):
			t.Fatalf("only %d/%d requests completed", i, total)
		}
	}

	require.False(t, containsBackend(a.reg.All(), "backend-b"), "the removed backend leaves All")
	require.Eventually(t, func() bool {
		var held int64
		for _, b := range a.reg.All() {
			held += b.ActiveConns()
		}
		return held == 0
	}, eventuallyDeadline, eventuallyTick, "active connections must return to zero")

	require.Eventually(t, func() bool {
		return a.logs.transitionCount(logger.EventBackendDrained, "backend-b", logger.ReasonIdle) == 1
	}, eventuallyDeadline, eventuallyTick, "the removed backend must log one idle drain")

	require.Eventually(t, func() bool {
		_, active := gaugeValueOK(a.collector, "lb_active_connections", map[string]string{"backend": "backend-b"})
		_, healthy := gaugeValueOK(a.collector, "lb_backend_healthy", healthGaugeLabels("backend-b"))
		_, circuit := gaugeValueOK(a.collector, "lb_circuit_state", circuitStateLabels("backend-b", "closed"))
		return !active && !healthy && !circuit
	}, eventuallyDeadline, eventuallyTick, "no gauge series may be left for the drained backend")
}

// TestChaosReloadDrainWindowExpired proves the bound is real: when the drain
// window elapses before the hold ends, the removed backend's in-flight request
// is cancelled with a 502, the drain logs window_expired with the cancelled
// count, and a fresh same-name backend re-added under a new URL keeps its own
// series and state (ADR-0016 decision 7).
func TestChaosReloadDrainWindowExpired(t *testing.T) {
	window := 50 * time.Millisecond
	fbs := newFlippableBackends(t, 2)
	a := assemble(t, reloadChaosConfigWindow(fbs, window))

	aBefore := backendByName(t, a.reg, "backend-a")
	bBefore := backendByName(t, a.reg, "backend-b")

	release := make(chan struct{})
	entered := make(chan string, 2)
	fbs[0].ServeGated(entered, release)
	fbs[1].ServeGated(entered, release)

	results := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			status, _ := doRequest(a.handler)
			results <- status
		}()
	}

	require.Eventually(t, func() bool {
		return aBefore.ActiveConns() == 1 && bBefore.ActiveConns() == 1
	}, eventuallyDeadline, eventuallyTick, "one request must be held on each backend")

	// Re-add backend-b under a new URL, removing the old instance. The drain
	// window is unchanged, so the reload is accepted.
	b2 := newFlippableBackend(t, "backend-b")
	require.NoError(t, a.application.Reload(context.Background(),
		reloadChaosConfigWindow([]*flippableBackend{fbs[0], b2}, window)))

	// The removed backend's held request is cut off at window expiry with 502.
	select {
	case status := <-results:
		require.Equal(t, http.StatusBadGateway, status, "the removed backend's request must be cut off with 502")
	case <-time.After(eventuallyDeadline):
		t.Fatal("the removed backend's request was not cancelled at window expiry")
	}

	close(release)
	select {
	case status := <-results:
		require.Equal(t, http.StatusOK, status, "the unchanged backend's request must complete")
	case <-time.After(eventuallyDeadline):
		t.Fatal("the unchanged backend's request did not complete")
	}

	// One drained line, reason window_expired, carrying the cancelled count.
	require.Eventually(t, func() bool {
		return a.logs.transitionCount(logger.EventBackendDrained, "backend-b", logger.ReasonWindowExpired) == 1
	}, eventuallyDeadline, eventuallyTick, "the removed backend must log one window_expired drain")

	rec, ok := a.logs.transitionRecord(logger.EventBackendDrained, "backend-b", logger.ReasonWindowExpired)
	require.True(t, ok)
	require.Equal(t, "1", recordFields(rec)["cancelled"], "the drain must report one cancelled request")

	// The fresh same-name backend is admitted by its first probe, and its state
	// reflects only its own traffic: healthy, circuit closed, and its
	// active-connections series survived the drain because the name is in use.
	requireGaugeEventually(t, a.collector, "lb_backend_healthy", healthGaugeLabels("backend-b"), 1,
		"the fresh same-name backend must be admitted")
	assertGauge(t, a.collector, "lb_circuit_state", circuitStateLabels("backend-b", "closed"), 1)
	_, active := gaugeValueOK(a.collector, "lb_active_connections", map[string]string{"backend": "backend-b"})
	require.True(t, active, "the drain must not delete a series whose name is in use")
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
