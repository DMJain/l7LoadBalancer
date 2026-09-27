package chaos_test

import (
	"context"
	"flag"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/config"
)

// The soak is flag-gated so a normal `make test` run skips it. `make soak` and
// `make soak-race` pass -soak plus a -soak-duration. The flags are registered
// at package scope so the test binary's flag.Parse finds them.
var (
	soakEnabled = flag.Bool("soak", false,
		"run the S4.T10 one-hour soak test (skipped without this flag; see -soak-duration)")
	soakDuration = flag.Duration("soak-duration", time.Hour,
		"soak wall-clock budget; the four failure phases and the quiet period scale proportionally")
)

// Committed tolerances (S4.T10). The goroutine delta is the audit's own delta,
// reused so the number has one definition: it absorbs GC-finalizer queueing and
// timer jitter, while a real per-request or per-reload leak is thousands of
// goroutines and cannot hide inside it. The heap delta bounds post-GC live-heap
// growth across the whole soak.
const (
	soakGoroutineDelta = leakAuditDelta // +10
	soakHeapDelta      = 8 << 20        // +8 MB
)

// Phase plan. The quiet period is taken off the budget first (capped so a
// shortened iteration does not always pay a full minute); the four failure
// phases then divide what remains in the spec's 20:10:10:10 ratio, so the whole
// run is approximately -soak-duration. At the default one-hour budget that is
// ~24 min steady and ~12 min per failure phase.
const (
	soakSteadyFraction = 0.40
	soakCancelFraction = 0.20
	soakDeathFraction  = 0.20
	soakReloadFraction = 0.20
	soakQuietFraction  = 0.05
	soakQuietCap       = 60 * time.Second
)

// Load and failure-injection tuning. Backends are in-process, so every knob is
// milliseconds; the phases are what take minutes.
const (
	soakWarmup             = 3 * time.Second
	soakIdleConnTimeout    = 500 * time.Millisecond
	soakSettle             = 2 * soakIdleConnTimeout
	soakWorkers            = 8
	soakCancelOneIn        = 20 // ~5% of requests cancel mid-response
	soakHeaderDelay        = 40 * time.Millisecond
	soakCancelAfter        = 10 * time.Millisecond
	soakBodyDelay          = 50 * time.Millisecond
	soakProbeInterval      = 100 * time.Millisecond
	soakDrainWindow        = 80 * time.Millisecond
	soakPath               = "/soak"
	soakDeathKillInterval  = 3 * time.Second
	soakDeathRestartDelay  = 500 * time.Millisecond
	soakReloadMinInterval  = 200 * time.Millisecond
	soakReloadIntervalPart = 1.0 / 60.0 // 60s at the one-hour budget
)

// statusClientClosed is nginx's client-closed-request code, which the proxy
// writes when a client cancels before it could answer (S4.T5). The proxy's own
// constant is unexported, so the external chaos package restates the value.
const statusClientClosed = 499

// TestChaosSoakConnectionLifecycle is S4.T10: a flag-gated soak through the
// application seam. It cycles steady proxying, client cancellations (S4.T5),
// backend deaths (S4.T6), and reloads (S4.T3/T4) under sustained load, then
// asserts after a quiet period that the goroutine count and post-GC heap have
// not grown past the committed tolerances. It exercises the failure paths that
// allocate resources, not just the happy path.
//
// The reload phase invokes App.Reload directly — the operation main's SIGHUP
// loop calls. Whether the OS delivers SIGHUP to main's channel is the one line
// of the reload path this bundle deliberately leaves untested (see the bundle
// spec's Out of Scope); the seam under test is the application, not the signal
// registration.
func TestChaosSoakConnectionLifecycle(t *testing.T) {
	if !*soakEnabled {
		t.Skip("soak test is flag-gated; run `make soak` (or `make soak-race`); shorten with SOAK_DURATION=2m")
	}
	budget := *soakDuration
	require.Positive(t, budget, "soak-duration must be positive")

	ctx := t.Context()

	// The quiet period is reserved off the budget; the four failure phases
	// split the rest. Keeping them out of the quiet window is what makes the
	// run approximately the requested budget rather than budget + quiet.
	quiet := quietDuration(budget)
	phaseBudget := budget - quiet
	if phaseBudget < 0 {
		phaseBudget = 0
	}

	// Four backends run from the start, so every backend's transport connection
	// pool is in the baseline. The reload phase alternates 3-subsets of them
	// (removing one, adding another, S4.T3/T4); if the reserve entered only at
	// reload time its fresh pool would be miscounted as leak growth. Four names
	// is the harness's limit.
	fbs := newFlippableBackends(t, 4)

	a := assembleWith(t, soakConfig(fbs), discardLogger(), nil)

	load := startSoakLoad(a.handler)
	defer load.stopAndWait()

	// --- Warmup: steady load until the initial probes and the transport's
	// connection goroutines exist, then pause so the baseline is captured in
	// the same idle state the final measurement uses (idle connection pools,
	// no in-flight request goroutines). ---
	load.traffic.Store(true)
	for _, fb := range fbs {
		requireGaugeEventually(t, a.collector, "lb_backend_healthy", healthGaugeLabels(fb.id), 1,
			"every backend must be healthy before the baseline")
	}
	sleepCtx(ctx, soakWarmup)
	load.traffic.Store(false)
	sleepCtx(ctx, soakSettle)
	baselineGoroutines := settledGoroutines(t)
	var baselineHeap uint64
	if !raceDetectorEnabled {
		baselineHeap = postGCHeap()
	}
	load.traffic.Store(true)
	t.Logf("soak: budget=%s baseline goroutines=%d baseline heap=%d KB", budget, baselineGoroutines, baselineHeap>>10)

	// --- Phase 1: steady-state proxying. ---
	sleepCtx(ctx, phaseDuration(phaseBudget, soakSteadyFraction))

	// --- Phase 2: client cancellations. The slow-headers handler keeps a
	// response from starting, so a cancelled client lands in S4.T5's
	// client-gone branch (499) rather than after the headers. ---
	serveAll(fbs, func(fb *flippableBackend) { fb.ServeSlowHeaders(soakHeaderDelay) })
	load.cancel.Store(true)
	sleepCtx(ctx, phaseDuration(phaseBudget, soakCancelFraction))
	load.cancel.Store(false)
	serveAll(fbs, func(fb *flippableBackend) { fb.Serve200() })

	// --- Phase 3: backend death. The victim streams its body slowly, so a kill
	// truncates a response whose headers already reached the proxy (S4.T6) and
	// the active checker ejects then reinstates it. ---
	runDeathPhase(t, ctx, a, fbs[0], phaseDuration(phaseBudget, soakDeathFraction))

	// --- Phase 4: reloads under load. The slow-headers handler keeps requests
	// in flight across each swap, so the drains have work to do. ---
	serveAll(fbs, func(fb *flippableBackend) { fb.ServeSlowHeaders(soakHeaderDelay) })
	setA := soakConfig([]*flippableBackend{fbs[0], fbs[1], fbs[2]})
	setB := soakConfig([]*flippableBackend{fbs[0], fbs[1], fbs[3]})
	reloadInterval := time.Duration(float64(budget) * soakReloadIntervalPart)
	if reloadInterval < soakReloadMinInterval {
		reloadInterval = soakReloadMinInterval
	}
	reloads := runReloadPhase(t, ctx, a, []*config.Config{setB, setA}, phaseDuration(phaseBudget, soakReloadFraction), reloadInterval)
	serveAll(fbs, func(fb *flippableBackend) { fb.Serve200() })

	// --- Phase 5: quiet. No client traffic; drains finish. ---
	load.traffic.Store(false)
	sleepCtx(ctx, quiet)

	// --- Assert. ---
	t.Logf("soak: end goroutines=%d ok=%d canceled=%d failed=%d reloads=%d",
		runtime.NumGoroutine(), load.ok.Load(), load.canceled.Load(), load.failed.Load(), reloads)
	requireGoroutinesSettleToWithin(t, baselineGoroutines, soakGoroutineDelta)

	if !raceDetectorEnabled {
		endHeap := postGCHeap()
		t.Logf("soak: end heap=%d KB", endHeap>>10)
		require.LessOrEqualf(t, endHeap, baselineHeap+soakHeapDelta,
			"post-GC heap must not grow more than %d MB: baseline %d KB, end %d KB",
			soakHeapDelta>>20, baselineHeap>>10, endHeap>>10)
	}

	// Phase-machinery checks: a run long enough for a phase must have actually
	// driven that failure path. Guarded on duration so a short iteration that
	// legitimately fits zero cycles is not a failure.
	if phaseDuration(phaseBudget, soakCancelFraction) >= time.Second {
		require.Positivef(t, load.canceled.Load(), "the cancellation phase must have cancelled at least one client request")
	}
	if phaseDuration(phaseBudget, soakDeathFraction) >= soakDeathKillInterval {
		require.Positivef(t, load.failed.Load(), "the backend-death phase must have produced at least one failure")
	}
	if phaseDuration(phaseBudget, soakReloadFraction) > 0 {
		require.Positivef(t, reloads, "the reload phase must have reloaded at least once")
	}
}

// soakConfig is the soak's config: the shared chaos fast path with a short
// drain window, a slower probe cadence so the health checker's per-probe churn
// does not swamp the goroutine count, and a short idle-connection timeout so
// the proxy's pooled connections to every backend are closed during the settling
// pauses that precede both measurements. Without that drain, the number of
// pooled connections is a noisy function of peak concurrency and a live
// connection's three goroutines can masquerade as growth. The probe interval,
// drain window, and idle timeout are all non-reloadable fields, so every reload
// config carries the same values.
func soakConfig(fbs []*flippableBackend) *config.Config {
	cfg := chaosConfig(fbs)
	probeInterval := soakProbeInterval
	drainWindow := soakDrainWindow
	idleConnTimeout := soakIdleConnTimeout
	cfg.Health.ProbeInterval = &probeInterval
	cfg.Reload.DrainWindow = &drainWindow
	cfg.Transport.IdleConnTimeout = &idleConnTimeout
	return cfg
}

// phaseDuration returns fraction of span, the time a phase may run.
func phaseDuration(span time.Duration, fraction float64) time.Duration {
	return time.Duration(float64(span) * fraction)
}

// quietDuration caps the quiet fraction so a short iteration stays short, but
// keeps it at least long enough for the proxy's idle connections to close, so
// the final measurement sees no connection goroutines.
func quietDuration(budget time.Duration) time.Duration {
	q := phaseDuration(budget, soakQuietFraction)
	if q > soakQuietCap {
		return soakQuietCap
	}
	if q < soakSettle {
		return soakSettle
	}
	return q
}

// sleepCtx sleeps for d or until ctx is done, whichever is first. It reports
// false when interrupted.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// postGCHeap forces a collection and returns the live heap after it — the
// honest leak signal: memory that survives a GC is retained, not noise.
func postGCHeap() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// discardLogger drops every record. The soak runs for an hour at high request
// volume, so retaining one slog.Record per request (as the capture handler
// does) would itself grow the heap and fail the soak's own assertion.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// serveAll installs apply on every backend's handler.
func serveAll(fbs []*flippableBackend, apply func(*flippableBackend)) {
	for _, fb := range fbs {
		apply(fb)
	}
}

// ServeSlowHeaders answers 200 after an initial delay, before any header is
// written. A client that cancels during the delay exercises S4.T5's
// client-gone branch; a client that waits sees a normal 200.
func (fb *flippableBackend) ServeSlowHeaders(delay time.Duration) {
	h := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(delay)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, fb.id)
	}))
	fb.handler.Store(&h)
}

// ServeSlowBody writes and flushes 200 headers, then waits delay before writing
// the body. A backend killed during the wait truncates a response whose headers
// already reached the proxy — S4.T6's mid-body-death path.
func (fb *flippableBackend) ServeSlowBody(delay time.Duration) {
	h := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(delay)
		_, _ = io.WriteString(w, fb.id)
	}))
	fb.handler.Store(&h)
}

// soakLoad is the soak's steady client load: a fixed pool of worker goroutines
// driving requests through the proxy handler. The pool starts once and lives
// for the whole soak — idling, not exiting, through the quiet phase — so the
// warmup baseline and the final measurement both include the same worker
// goroutines. Traffic and mid-response cancellation are toggled per phase.
type soakLoad struct {
	handler http.Handler

	traffic atomic.Bool
	cancel  atomic.Bool
	seq     atomic.Uint64

	ok       atomic.Int64
	canceled atomic.Int64
	failed   atomic.Int64

	stop chan struct{}
	wg   sync.WaitGroup
}

func startSoakLoad(h http.Handler) *soakLoad {
	l := &soakLoad{handler: h, stop: make(chan struct{})}
	for i := 0; i < soakWorkers; i++ {
		l.wg.Add(1)
		go l.worker()
	}
	return l
}

func (l *soakLoad) worker() {
	defer l.wg.Done()
	for {
		select {
		case <-l.stop:
			return
		default:
		}
		if !l.traffic.Load() {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		var cancelAfter time.Duration
		if l.cancel.Load() && l.seq.Add(1)%soakCancelOneIn == 0 {
			cancelAfter = soakCancelAfter
		}
		switch status := soakRequest(l.handler, cancelAfter); {
		case status == statusClientClosed:
			l.canceled.Add(1)
		case status >= 200 && status < 300:
			l.ok.Add(1)
		default:
			l.failed.Add(1)
		}
	}
}

func (l *soakLoad) stopAndWait() {
	close(l.stop)
	l.wg.Wait()
}

// soakRequest drives one request through h. With cancelAfter > 0 it runs the
// handler on its own goroutine and cancels the client context after the delay,
// so the round trip is genuinely in flight when the cancellation lands.
func soakRequest(h http.Handler, cancelAfter time.Duration) int {
	if cancelAfter <= 0 {
		status, _ := doRequestPath(h, soakPath)
		return status
	}

	req := httptest.NewRequest(http.MethodGet, soakPath, nil)
	rec := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rec, req)
	}()

	timer := time.NewTimer(cancelAfter)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		cancel()
		<-done
	}
	return rec.Code
}

// runDeathPhase kills and restarts the victim on a fixed cadence for dur. Every
// kill lands while the victim is streaming a body, so requests in flight on it
// are truncated (S4.T6) and its active probes fail, ejecting it; the restart
// lets the checker reinstate it. It leaves the victim running, serving 200, and
// healthy.
func runDeathPhase(t *testing.T, ctx context.Context, a *assembly, victim *flippableBackend, dur time.Duration) {
	t.Helper()
	if dur <= 0 {
		return
	}
	victim.ServeSlowBody(soakBodyDelay)
	deadline := time.Now().Add(dur)
	for {
		victim.Kill()
		if !sleepCtx(ctx, soakDeathRestartDelay) {
			return
		}
		victim.Restart()
		if !sleepCtx(ctx, soakDeathKillInterval-soakDeathRestartDelay) {
			return
		}
		if time.Now().After(deadline) {
			break
		}
	}
	victim.Restart()
	victim.Serve200()
	requireGaugeEventually(t, a.collector, "lb_backend_healthy", healthGaugeLabels(victim.id), 1,
		"the death phase must end with the victim reinstated")
}

// runReloadPhase alternates cfgs on interval for dur and returns the number of
// reloads applied. It guarantees at least one reload when dur is positive.
func runReloadPhase(t *testing.T, ctx context.Context, a *assembly, cfgs []*config.Config, dur, interval time.Duration) int {
	t.Helper()
	if dur <= 0 || len(cfgs) == 0 {
		return 0
	}
	deadline := time.Now().Add(dur)
	n := 0
	for {
		require.NoError(t, a.application.Reload(ctx, cfgs[n%len(cfgs)]))
		n++
		if !sleepCtx(ctx, interval) {
			return n
		}
		if time.Now().After(deadline) {
			return n
		}
	}
}
