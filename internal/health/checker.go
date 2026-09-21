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
)

// Checker periodically probes every backend in a Registry and reflects the
// outcome into Backend health state.
//
// Concurrency: the Checker itself is immutable after New; each backend's
// mutable state lives in that backend's own prober, touched by exactly one
// goroutine (see Start), so the consecutive counters need no synchronization.
// Backend health is read/written only through Backend's atomic-backed methods.
// See ADR-0011 decisions 2, 10, and 11.
type Checker struct {
	reg      *backend.Registry
	interval time.Duration
	client   *http.Client
	log      *slog.Logger
	metrics  *metrics.Collector
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
		reg:      reg,
		interval: interval,
		log:      log,
		metrics:  collector,
		client: &http.Client{
			Transport: transport,
			Timeout:   timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Start launches one probe goroutine per backend. It returns immediately; the
// goroutines run until ctx is cancelled, which is why main passes its existing
// SIGINT/SIGTERM signal context rather than introducing a second shutdown
// primitive (ADR-0011 decision 13).
func (c *Checker) Start(ctx context.Context) {
	for _, b := range c.reg.All() {
		go c.newProber(b).run(ctx)
	}
}

// probeOnce issues one probe against b and folds the outcome into the state
// machine, marking the backend healthy/unhealthy once a consecutive run
// reaches M or N. It is intentionally separate from run's ticker loop so tests
// can drive probe cycles directly, with no real ticker or context
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
func (p *prober) probeOnce(ctx context.Context) bool {
	if p.checker.probe(ctx, p.target) {
		p.failures = 0
		p.successes++
		if p.successes >= probeSuccessesBeforeHealthy && !p.target.IsHealthy() {
			p.checker.log.Info("backend reinstated",
				"backend", p.target.Name,
				"event", logger.EventHealthReinstated,
				"reason", logger.ReasonProbeRecovered,
			)
			p.checker.metrics.SetBackendHealthy(p.target.Name, true)
		}
		if p.successes >= probeSuccessesBeforeHealthy {
			p.target.MarkHealthy()
		}
		return true
	}

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
	return false
}

// prober carries one backend's consecutive-outcome state. It is created and
// owned by a single goroutine (run) or by a test calling probeOnce directly;
// nothing else may touch a prober's counters.
type prober struct {
	checker *Checker
	target  *backend.Backend

	successes int
	failures  int
}

func (c *Checker) newProber(b *backend.Backend) *prober {
	return &prober{checker: c, target: b}
}

// run is the per-backend goroutine wiring: a ticker selecting against ctx.
// All probe logic lives in probeOnce.
func (p *prober) run(ctx context.Context) {
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
