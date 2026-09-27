package chaos_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// leakAuditDelta is the goroutine headroom the audit allows over its warmup
// baseline: GC-finalizer queueing and timer jitter land here, while a real
// per-request leak (one goroutine per cancellation or death) is dozens of
// goroutines and cannot hide inside it. This is S4.T9's "small delta"; the
// soak (S4.T10) generalizes the same assertion to a full hour.
const leakAuditDelta = 10

// Burst sizes: enough repeated failures that a per-request leaked goroutine is
// unmistakable above the delta, small enough to run in a normal test.
const (
	leakCancellationBurst = 25
	leakDeathBurst        = 15
)

// TestChaosGoroutineLeakAudit is S4.T9's automated leak audit through the
// application seam. It warms the system, captures a goroutine baseline, then
// drives a failure-laden burst — client cancellations against a gated backend
// (S4.T5's suppression path) followed by backend deaths (S4.T6's transport
// path) — and asserts the goroutine count settles back within a small delta of
// the baseline. circuitChaosConfig pushes the checker's cadence past the test
// window (one initial probe still fires), so the only traffic is the burst the
// test drives.
func TestChaosGoroutineLeakAudit(t *testing.T) {
	fbs := newFlippableBackends(t, 1)
	a := assemble(t, circuitChaosConfig(fbs))
	x := fbs[0]

	// Wait for the initial probe so the per-backend prober goroutine is in the
	// baseline rather than arriving later as apparent growth.
	requireGaugeEventually(t, a.collector, "lb_backend_healthy", healthGaugeLabels(x.id), 1,
		"backend must be healthy before the audit")

	// Warmup: successful traffic so the transport's idle-connection goroutines
	// are established in the baseline, not blamed on the burst.
	for i := 0; i < 20; i++ {
		status, _ := doRequest(a.handler)
		require.Equal(t, http.StatusOK, status)
	}
	baseline := settledGoroutines(t)

	// Phase 1 — client cancellations. The gated backend holds each request open
	// until the client leaves, so the cancellation lands on an in-flight round
	// trip deterministically; releasing the gate at the end lets every parked
	// backend handler finish.
	entered := make(chan string, leakCancellationBurst)
	release := make(chan struct{})
	x.ServeGated(entered, release)
	for i := 0; i < leakCancellationBurst; i++ {
		require.Equal(t, 499, driveCanceledRequest(t, a.handler, entered),
			"a cancelled client must be recorded as 499")
	}
	close(release)

	// Phase 2 — backend deaths. The process dies, so every request is a genuine
	// transport failure (502) until the circuit opens and the dead backend drops
	// out of the selectable set (503). Both are backend-failure outcomes; the
	// audit cares that neither path strands a goroutine.
	x.Kill()
	for i := 0; i < leakDeathBurst; i++ {
		status, _ := doRequest(a.handler)
		require.Truef(t, status == http.StatusBadGateway || status == http.StatusServiceUnavailable,
			"a dead backend must yield 502 (transport failure) or 503 (circuit open), got %d", status)
	}

	requireGoroutinesSettleToWithin(t, baseline, leakAuditDelta)
}

// driveCanceledRequest runs one request through h on its own goroutine, waits
// until the gated backend has received it, cancels the client context, and
// returns the recorded status once the handler returns. The gated backend must
// already be installed by the caller; entered receives the backend's identity.
func driveCanceledRequest(t *testing.T, h http.Handler, entered <-chan string) int {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/leak", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rec, req)
	}()

	select {
	case <-entered:
	case <-time.After(eventuallyDeadline):
		t.Fatal("the gated backend never received the request")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(eventuallyDeadline):
		t.Fatal("a cancelled request did not return")
	}
	return rec.Code
}

// settledGoroutines polls until the goroutine count has stopped changing across
// several ticks — the quiet period — and returns it. Requiring stability rather
// than a single read keeps the app's own startup goroutines (servers, the
// per-backend prober) from racing into the baseline as apparent growth.
func settledGoroutines(t *testing.T) int {
	t.Helper()

	prev := runtime.NumGoroutine()
	stable := 0
	count := prev
	require.Eventually(t, func() bool {
		count = runtime.NumGoroutine()
		if count == prev {
			stable++
		} else {
			stable = 0
			prev = count
		}
		return stable >= 3
	}, 5*time.Second, 25*time.Millisecond, "goroutine count never settled")
	return count
}

// requireGoroutinesSettleToWithin fails unless the goroutine count stays within
// delta of baseline for several consecutive ticks, so a transient dip does not
// mask a leak that has settled above the bound.
func requireGoroutinesSettleToWithin(t *testing.T, baseline, delta int) {
	t.Helper()

	stable := 0
	require.Eventually(t, func() bool {
		if runtime.NumGoroutine() <= baseline+delta {
			stable++
		} else {
			stable = 0
		}
		return stable >= 3
	}, 10*time.Second, 25*time.Millisecond,
		"goroutines must settle to within %d of the %d baseline; last read %d",
		delta, baseline, runtime.NumGoroutine())
}
