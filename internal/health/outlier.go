package health

import (
	"log/slog"
	"sync"
	"time"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
)

// Passive-outlier tuning. Both are Go constants, not config fields, per
// ADR-0011 decision 10 — matching the "constant, not config" posture already
// set for the active checker's thresholds, ε (ADR-0009) and α (ADR-0010).
const (
	// outlierWindowSize is the number of most recent round-trip outcomes
	// retained per backend. A count-based window, not a time-based one
	// (ADR-0011 decision 12), consistent with this project's preference for
	// count/consecutive state machines over clock-based ones.
	outlierWindowSize = 10

	// outlierFailuresBeforeEject is N: the number of failures within the
	// window that ejects a backend. Half a window of failures is a backend
	// that is broken for real, not merely unlucky — deliberately more
	// tolerant than the active checker's three consecutive probe failures,
	// giving passive detection the "tolerant and slow" posture ADR-0011
	// decision 8 assigns it.
	outlierFailuresBeforeEject = 5
)

// OutlierDetector is the passive half of health checking: it watches the live
// request path rather than probing, ejecting a backend that is up but returning
// errors (a burst of 5xx responses) or refusing connections (a burst of
// transport failures) without waiting for the next scheduled active probe.
//
// It implements proxy.RoundTripObserver structurally — it does not import
// internal/proxy, only implements its method — and main registers it on the
// Proxy alongside the latency observer (ADR-0011 decision 9). The failure
// signal is the observer contract's success flag: false covers both a 5xx
// response and an errorHandler transport failure, the union ADR-0011 decision
// 12 specifies. Both mix freely within one window.
//
// Recovery is deliberately absent here: a passively-ejected backend is
// reinstated only by a successful active probe (ADR-0011 decision 3), so this
// detector has no timer and never calls MarkHealthy. It notices recovery
// observationally — an ejected backend that next reads healthy has been
// reinstated out of band — and starts a fresh ejection episode.
//
// Concurrency: ObserveRoundTrip is called from every request goroutine, so the
// per-backend windows are guarded by one mutex. The lock is held only for the
// window update and one atomic store, never across I/O, so it does not
// serialize the request path beyond a few instructions. windows grows only for
// backends actually observed, all of which come from the registry.
type OutlierDetector struct {
	// eject is the action taken on a threshold breach. It is a field rather
	// than a direct b.MarkUnhealthy() call purely as a test seam:
	// MarkUnhealthy is an idempotent atomic store, so its invocation count —
	// the ticket's "exactly once, not once per failure" property — is not
	// otherwise observable. Production always starts from NewOutlierDetector's
	// default. Called with d.mu held; must not block.
	eject func(*backend.Backend)

	log *slog.Logger

	mu      sync.Mutex
	windows map[*backend.Backend]*outlierWindow
}

// outlierWindow is one backend's ring of recent outcomes. failures is the
// number of false entries currently in the ring, maintained incrementally so a
// threshold check never scans the window.
type outlierWindow struct {
	outcomes []bool // ring buffer; true = success
	next     int    // slot the next outcome overwrites
	filled   int    // populated slots, capped at outlierWindowSize
	failures int    // false entries currently in the window
	ejected  bool   // already ejected for the current episode
}

// NewOutlierDetector returns a ready detector logging one structured line per
// ejection episode through log. It owns no goroutines and takes no
// configuration beyond the logger: every tunable here is a Go constant
// (ADR-0011 decision 10), and the detector is a passive observer of traffic
// rather than a scheduled subsystem.
func NewOutlierDetector(log *slog.Logger) *OutlierDetector {
	return &OutlierDetector{
		eject:   (*backend.Backend).MarkUnhealthy,
		log:     log,
		windows: make(map[*backend.Backend]*outlierWindow),
	}
}

// ObserveRoundTrip folds one backend round trip into that backend's window and
// ejects it once the failure count reaches the threshold. It is called
// unconditionally for every round trip, success and failure alike; the duration
// is unused here (latency estimation is the latency observer's job).
func (d *OutlierDetector) ObserveRoundTrip(b *backend.Backend, _ time.Duration, success bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	w := d.windows[b]
	if w == nil {
		w = &outlierWindow{outcomes: make([]bool, outlierWindowSize)}
		d.windows[b] = w
	}

	// Recovery is only ever the active checker's doing, so the detector never
	// calls MarkHealthy. When a backend this detector ejected reads healthy
	// again, a probe has reinstated it: start a clean episode rather than
	// letting the stale failure burst re-eject it on the very next failure.
	if w.ejected && b.IsHealthy() {
		w.reset()
	}

	w.observe(success)
	if !success && !w.ejected && w.failures >= outlierFailuresBeforeEject {
		w.ejected = true
		d.eject(b)
		// The ejected flag is this detector's edge trigger: it is set once per
		// episode (and cleared when an active probe reinstates the backend), so
		// the line lands exactly at the ejection, not on every later failure
		// (ADR-0013 decision 11). It shares the flag MarkUnhealthy is guarded
		// by, so the log line and the state change can never disagree.
		d.log.Warn("backend ejected",
			"backend", b.Name,
			"event", logger.EventHealthEjected,
			"reason", logger.ReasonOutlierWindow,
		)
	}
}

// observe records one outcome in the ring, evicting the oldest once full.
func (w *outlierWindow) observe(success bool) {
	if w.filled == outlierWindowSize {
		if !w.outcomes[w.next] {
			w.failures--
		}
	} else {
		w.filled++
	}
	w.outcomes[w.next] = success
	if !success {
		w.failures++
	}
	w.next = (w.next + 1) % outlierWindowSize
}

// reset clears the window and the episode flag. The next outcome after a
// reinstate starts a fresh count.
func (w *outlierWindow) reset() {
	clear(w.outcomes)
	w.next = 0
	w.filled = 0
	w.failures = 0
	w.ejected = false
}
