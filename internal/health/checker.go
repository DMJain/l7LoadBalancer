// Package health implements the load balancer's health-checking subsystems.
// Active probing (this file) owns one goroutine per backend and drives
// Backend.MarkHealthy/MarkUnhealthy through a consecutive-outcome state
// machine. Passive outlier detection (outlier.go) observes live request
// outcomes through the proxy's RoundTripObserver fan-out and is what makes
// MarkUnhealthy reachable from traffic rather than probes; only active probes
// reinstate a backend it ejected.
package health

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// Consecutive-outcome thresholds. Both are Go constants, not config fields,
// per ADR-0011 decision 10 — matching the "constant, not config" posture
// ADR-0009 (ε) and ADR-0010 (α, the failure penalty) already set for
// algorithm-shaped tuning values.
const (
	// probeFailuresBeforeUnhealthy is N: the number of consecutive failed
	// probes after which a backend is marked unhealthy. At the default 5s
	// probe interval that ejects a dead backend in roughly 15s — quick enough
	// for the Sprint 3 exit criterion, slow enough not to flap on one lost
	// probe.
	probeFailuresBeforeUnhealthy = 3

	// probeSuccessesBeforeHealthy is M: the number of consecutive successful
	// probes after which a backend is marked healthy again. By convention
	// (ADR-0011 decision 2) this is the only path that recovers a backend
	// ejected by passive outlier detection, so it also paces recovery at
	// roughly 10s by default.
	probeSuccessesBeforeHealthy = 2

	// probeInitialSuccessesBeforeHealthy is the number of successful probes
	// that admit a backend a reload just added. One, not the recovery
	// threshold: an added backend starts unhealthy and its first successful
	// probe is the proof the operator's URL works (ADR-0015 decision 10).
	probeInitialSuccessesBeforeHealthy = 1
)

// Checker periodically probes every backend in a Registry and reflects the
// outcome into Backend health state.
//
// Concurrency: the Checker's immutable fields are set at New; the running
// probers are tracked in a mutex-guarded map so a reload can add and remove
// individual backends without touching the others, and the first-probe-round
// latch (probed/probeRoundComplete) is atomic. Each backend's mutable state
// lives in that backend's own prober, touched by exactly one goroutine (its
// run loop) or, in tests, one goroutine calling probeOnce directly. Backend
// health is read/written only through Backend's atomic-backed methods. See
// ADR-0011 decisions 2, 10, and 11, and ADR-0015 decision 10.
type Checker struct {
	reg      *backend.Registry
	interval time.Duration
	client   *http.Client
	log      *slog.Logger
	metrics  *metrics.Collector

	// backendCount is len(reg.All()) at construction and is fixed for the
	// checker's lifetime.
	backendCount int
	// probed counts how many distinct backends have completed at least one
	// probe. It is only compared against backendCount, never reset.
	probed atomic.Int32
	// probeRoundComplete latches true once probed reaches backendCount — the
	// first full sweep — and is never cleared, including by a reload's
	// add/remove (ADR-0014 (S3.T12) decision 4).
	probeRoundComplete atomic.Bool

	// mu guards probers, which holds each running prober's control block. Add
	// and Remove are called off the request path (Start once, then the reload
	// loop), so the lock exists only to keep a concurrent add and remove from
	// corrupting the map.
	mu      sync.Mutex
	probers map[*backend.Backend]*proberHandle
}

// proberHandle is one running prober's control block: its cancel function and
// a channel closed when its goroutine exits. Remove waits on done so that once
// it returns, a removed backend's probe can no longer write a transition log
// or gauge after the reload has deleted its series.
type proberHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// New builds a Checker over reg that probes each backend every interval,
// bounding a single probe by timeout, and logs one structured line per genuine
// health transition (ejection/reinstatement) through log.
//
// The probe client is dedicated to health checking. It carries its own
// transport — cloned from http.DefaultTransport so it keeps sensible stdlib
// defaults without sharing the global connection pool that
// httputil.ReverseProxy falls back to — plus its own timeout, and a
// CheckRedirect returning http.ErrUseLastResponse so a 3xx is observed as a
// failure rather than silently followed to a 2xx target. Sharing no transport
// with internal/proxy (ADR-0011 decision 11) means probe tuning never couples
// to live-request transport settings.
//
// interval and timeout are expected positive; config.Validate guarantees it.
// It returns a Checker that does nothing until Start is called.
//
// collector receives one lb_backend_healthy update per genuine health
// transition, from the same edge-triggered site as the log line, so the gauge
// and the log can never disagree about whether the transition happened
// (ADR-0013 decision 6).
func New(reg *backend.Registry, interval, timeout time.Duration, log *slog.Logger, collector *metrics.Collector) *Checker {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &Checker{
		reg:          reg,
		interval:     interval,
		log:          log,
		metrics:      collector,
		backendCount: len(reg.All()),
		probers:      make(map[*backend.Backend]*proberHandle),
		client: &http.Client{
			Transport: transport,
			Timeout:   timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Start launches one probe goroutine per backend in the registry's current
// snapshot. It returns immediately; the goroutines run until ctx is cancelled,
// which is why main passes its existing SIGINT/SIGTERM signal context rather
// than introducing a second shutdown primitive (ADR-0011 decision 13).
//
// Start registers every backend with the ordinary (non-added) admission rule;
// a reload registers a backend it introduces with Add.
func (c *Checker) Start(ctx context.Context) {
	for _, b := range c.reg.All() {
		c.add(ctx, b, false)
	}
}

// Add starts probing a backend a reload has just introduced and returns
// immediately. It is the reload counterpart to Start: b is marked unhealthy and
// admitted by one successful probe, logged with reason initial_probe, because
// an added URL is unproven and must not produce client-visible 502s before it
// has answered once (ADR-0015 decision 10). After admission the prober reverts
// to the normal consecutive-success recovery threshold. Adding a backend that
// is already being probed is a no-op.
func (c *Checker) Add(ctx context.Context, b *backend.Backend) {
	c.add(ctx, b, true)
}

// add is the shared registration behind Start and Add. added selects the
// admission rule: true for a reload-added backend (starts unhealthy, admitted
// on one success), false for a startup backend (starts healthy, recovered only
// after the normal threshold). The prober is registered in the checker's
// mutex-guarded map so Remove can stop it without touching any other backend's
// prober.
func (c *Checker) add(ctx context.Context, b *backend.Backend, added bool) {
	c.mu.Lock()
	if _, exists := c.probers[b]; exists {
		c.mu.Unlock()
		return
	}
	pctx, cancel := context.WithCancel(ctx)
	p := c.newProber(b)
	p.added = added
	h := &proberHandle{cancel: cancel, done: make(chan struct{})}
	c.probers[b] = h
	c.mu.Unlock()

	if added {
		b.MarkUnhealthy()
	}
	go func() {
		defer close(h.done)
		p.run(pctx)
	}()
}

// Remove stops probing b and waits until its prober goroutine has exited, so a
// probe in flight at removal cannot apply its outcome after Remove returns and
// recreate a series a reload has just deleted. Removing a backend that is not
// being probed is a no-op.
func (c *Checker) Remove(b *backend.Backend) {
	c.mu.Lock()
	h := c.probers[b]
	delete(c.probers, b)
	c.mu.Unlock()
	if h == nil {
		return
	}
	h.cancel()
	<-h.done
}

// ProbeRoundComplete reports whether every configured backend has answered at
// least one active probe. It is false until the first full sweep finishes, then
// latches true for the checker's lifetime — the one-shot condition behind the
// /startupz gate (ADR-0014 (S3.T12)).
func (c *Checker) ProbeRoundComplete() bool {
	return c.probeRoundComplete.Load()
}

// probeOnce issues one probe against b and folds the outcome into the state
// machine, marking the backend healthy/unhealthy once a consecutive run
// reaches its threshold. It is intentionally separate from run's ticker loop so
// tests can drive probe cycles directly, with no real ticker or context
// cancellation. It returns whether this probe succeeded.
//
// Transition logging is edge-triggered twice over. The threshold checks fire
// only on the probe that crosses the threshold (failures == N, successes >= M),
// so a sustained streak logs once rather than on every subsequent probe; the
// health-state guards (IsHealthy/!IsHealthy, read before the Mark* call mutates
// it) make the line describe a *genuine* transition, so an already-healthy
// backend's success streak — every backend's first M probes at startup — cannot
// emit a spurious health_reinstated. The success gate is >= rather than ==
// (S3.T6.5): passive outlier detection can eject a backend while active probes
// keep succeeding, so its accumulator is already past M at ejection time; an
// equality would then skip the line and gauge write forever, leaving
// IsHealthy() true while lb_backend_healthy stayed 0. See ADR-0011's 2026-09-22
// amendment. The guard reads the backend's existing atomic health rather than
// adding a prober field (ADR-0013 decision 11). lb_backend_healthy is written
// inside those same two guarded blocks, so it shares this exact edge-triggered
// signal rather than re-deriving health independently (ADR-0013 decision 6).
//
// A prober for a reload-added backend (p.added) uses a one-success admission
// threshold with reason initial_probe for its first successful probe only, then
// reverts to the normal recovery threshold (ADR-0015 decision 10); its first
// failure logs nothing because the backend was never healthy.
func (p *prober) probeOnce(ctx context.Context) bool {
	ok := p.checker.probe(ctx, p.target)
	// A prober removed while its probe was in flight must not apply the
	// outcome: no health mark, no log line, no gauge write. Remove cancels the
	// prober's context and then waits for this goroutine to exit, so an outcome
	// that gets past this check still finishes before Remove returns and a
	// reload deletes the backend's series (S4.T3.0).
	if ctx.Err() != nil {
		return false
	}
	if ok {
		p.failures = 0
		p.successes++
		threshold, reason := probeSuccessesBeforeHealthy, logger.ReasonProbeRecovered
		if p.added {
			threshold, reason = probeInitialSuccessesBeforeHealthy, logger.ReasonInitialProbe
		}
		if p.successes >= threshold && !p.target.IsHealthy() {
			p.checker.log.Info("backend reinstated",
				"backend", p.target.Name,
				"event", logger.EventHealthReinstated,
				"reason", reason,
			)
			p.checker.metrics.SetBackendHealthy(p.target.Name, true)
		}
		if p.successes >= threshold {
			p.target.MarkHealthy()
		}
		// First admission is consumed here: a later ejection recovers on the
		// normal consecutive-success threshold, not one probe, and the streak
		// starts fresh so that threshold is measured from admission.
		if p.added {
			p.added = false
			p.successes = 0
		}
	} else {
		p.successes = 0
		p.failures++
		if p.failures == probeFailuresBeforeUnhealthy && p.target.IsHealthy() {
			p.checker.log.Warn("backend ejected",
				"backend", p.target.Name,
				"event", logger.EventHealthEjected,
				"reason", logger.ReasonProbeFailures,
			)
			p.checker.metrics.SetBackendHealthy(p.target.Name, false)
		}
		if p.failures >= probeFailuresBeforeUnhealthy {
			p.target.MarkUnhealthy()
		}
	}

	// Every probe — success or failure — counts as this backend having been
	// reached, which is what closes the first probe round.
	p.markProbed()
	return ok
}

// markProbed folds this prober's backend into the checker's first-round count,
// exactly once. The latch is set by whichever prober's first probe is the last
// of the set, so a subsequent round can never "re-complete" it
// (ADR-0014 (S3.T12)).
func (p *prober) markProbed() {
	if p.counted {
		return
	}
	p.counted = true
	if int(p.checker.probed.Add(1)) == p.checker.backendCount {
		p.checker.probeRoundComplete.Store(true)
	}
}

// prober carries one backend's consecutive-outcome state. It is created and
// owned by a single goroutine (run) or by a test calling probeOnce directly;
// nothing else may touch a prober's counters.
type prober struct {
	checker *Checker
	target  *backend.Backend

	// added marks a prober for a backend a reload just introduced. While set,
	// a single successful probe admits the backend (reason initial_probe);
	// admission consumes the flag, so later recovery uses the normal
	// consecutive-success threshold.
	added bool

	successes int
	failures  int

	// counted records whether this prober's first probe has already been folded
	// into the checker's first-round count. Owned by the same single goroutine
	// as the counters.
	counted bool
}

func (c *Checker) newProber(b *backend.Backend) *prober {
	return &prober{checker: c, target: b}
}

// run is the per-backend goroutine wiring: a ticker selecting against ctx.
// All probe logic lives in probeOnce. A reload-added prober probes once
// immediately so it can be admitted without waiting a full interval; startup
// probers keep the original first-probe-after-one-interval schedule
// (ADR-0015 decision 10).
func (p *prober) run(ctx context.Context) {
	if p.added {
		p.probeOnce(ctx)
	}
	ticker := time.NewTicker(p.checker.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.probeOnce(ctx)
		}
	}
}

// probe issues a single GET to b's configured URL and reports whether it is a
// success. No separate health path is configured (ADR-0011 decision 11): the
// backend's own URL is the probe target. Only a 2xx response counts; a 3xx,
// 4xx, or 5xx response and any transport error are all failures, because
// httputil.ReverseProxy forwards redirects verbatim rather than following
// them, so a redirecting backend is unusable even though it answered.
//
// The response body is drained so the dedicated client can reuse the
// connection; the client's timeout bounds the whole exchange either way.
func (c *Checker) probe(ctx context.Context, b *backend.Backend) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.URL.String(), nil)
	if err != nil {
		return false
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
}
