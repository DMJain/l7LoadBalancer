package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
)

// This file's concurrency: one goroutine per drain, started by Reload. A drain
// owns no shared mutable state of its own — it reads Backend's atomics
// (ActiveConns) and calls its methods (Retire), and writes the collector's
// gauges and the logger, all of which are themselves goroutine-safe. Drains are
// independent of each other and of later reloads; the context passed in (the
// process context in production) is the only shutdown signal they observe.
//
// drainPollInterval is how often a drain goroutine checks a removed backend's
// active-connection count. Polling is chosen over a completion channel so the
// request path pays nothing for drain bookkeeping — DecActive stays an atomic
// add and nothing else (ADR-0016 decision 7).
const drainPollInterval = 100 * time.Millisecond

// drainBackend finishes a removed backend's in-flight requests and then forgets
// it. Reload starts one goroutine per removed backend, after that backend is
// already out of the registry and marked removed, so no new request can reach
// it.
//
// Phase one lets the backend finish normally: it waits for ActiveConns to reach
// zero, bounded by window. If it does, the drain ends idle. Otherwise the
// window elapsed, so phase two calls Retire — firing the proxy's context joins
// and cancelling the remaining requests with a 502 — and waits for ActiveConns
// to reach zero before finishing.
//
// Cancelling ctx (process shutdown) aborts either wait immediately and without
// cleanup: the process is ending and the servers' graceful shutdown covers
// in-flight requests (ADR-0016 decision 8). Deleting the series and logging are
// the only cleanup, and both are skipped on shutdown.
//
// The drain is keyed to b, not its name, so a later reload re-adding the same
// identity neither revives nor ends it (ADR-0016 decision 9).
func (a *App) drainBackend(ctx context.Context, b *backend.Backend, window time.Duration) {
	if a.waitActiveZero(ctx, b, window) {
		a.completeDrain(ctx, b, logger.ReasonIdle, 0)
		return
	}
	if ctx.Err() != nil {
		return
	}

	// The window elapsed with requests still in flight: cancel them and wait
	// for their slots to release. cancelled is the count still in flight at
	// expiry — the set the cancellation reaches; a request that slips out
	// between this read and Retire is at worst counted once more than it was
	// actually cut off.
	cancelled := b.ActiveConns()
	b.Retire()
	if !a.waitActiveZero(ctx, b, 0) {
		return
	}
	a.completeDrain(ctx, b, logger.ReasonWindowExpired, cancelled)
}

// waitActiveZero blocks until b's active-connection count is zero, the timeout
// elapses, or ctx is cancelled. A timeout of zero means no deadline (phase two,
// after the requests have already been cancelled). It reports true only when
// the count actually reached zero.
func (a *App) waitActiveZero(ctx context.Context, b *backend.Backend, timeout time.Duration) bool {
	if b.ActiveConns() == 0 {
		return true
	}
	ticker := time.NewTicker(drainPollInterval)
	defer ticker.Stop()

	var timeoutCh <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timeoutCh:
			// Shutdown wins a tie: if ctx is being cancelled as the window
			// elapses, report "not idle" so the caller returns without cleanup
			// rather than retiring during shutdown (ADR-0016 decision 8).
			if ctx.Err() != nil {
				return false
			}
			return b.ActiveConns() == 0
		case <-ticker.C:
			if b.ActiveConns() == 0 {
				return true
			}
		}
	}
}

// completeDrain forgets a finished drain: it deletes b's active-connections
// series when no current backend shares the name, and logs one backend drained
// line carrying the reason and the number of requests cancelled. The name check
// keeps a fresh same-name backend's own series from being wiped (ADR-0016
// decision 9). A window_expired drain logs at WARN because requests were cut
// off; an idle drain logs at INFO.
func (a *App) completeDrain(ctx context.Context, b *backend.Backend, reason string, cancelled int64) {
	if !a.backendNameInUse(b.Name) {
		a.collector.DeleteActiveConnections(b.Name)
	}
	level := slog.LevelInfo
	if reason == logger.ReasonWindowExpired {
		level = slog.LevelWarn
	}
	a.log.Log(ctx, level, "backend drained",
		"event", logger.EventBackendDrained,
		"backend", b.Name,
		"reason", reason,
		"cancelled", cancelled,
	)
}

// backendNameInUse reports whether any backend in the current snapshot shares
// name, which is the condition under which a finished drain must not delete the
// active-connections series.
func (a *App) backendNameInUse(name string) bool {
	for _, b := range a.reg.All() {
		if b.Name == name {
			return true
		}
	}
	return false
}
