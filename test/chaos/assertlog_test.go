package chaos_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// captureHandler is a slog.Handler that retains every record it receives, so
// the chaos tests can assert on transition lines by structured field rather
// than by parsing JSON text. The tests build a logger over it and hand that
// logger to health.Checker, health.OutlierDetector, and circuit.Breaker — the
// only producers of the transition vocabulary.
//
// Concurrency: Handle is called from probe goroutines and request goroutines
// while the test goroutine reads, so the record slice is mutex-guarded.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, r.Clone())
	h.mu.Unlock()
	return nil
}

// WithAttrs/WithGroup return the handler unchanged: the chaos assertions care
// only about top-level transition fields, and no producer uses groups.
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

// snapshot returns a copy of every record captured so far.
func (h *captureHandler) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]slog.Record(nil), h.records...)
}

// recordFields flattens a record's attributes into a string map.
func recordFields(r slog.Record) map[string]string {
	fields := make(map[string]string, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		fields[a.Key] = a.Value.String()
		return true
	})
	return fields
}

// transitionCount returns how many captured records match all of event,
// backend, and reason exactly. It is a field-allowlist match: timestamps,
// levels, messages, and any other volatile fields are ignored, and there is no
// substring or regex matching anywhere.
func (h *captureHandler) transitionCount(event, backend, reason string) int {
	n := 0
	for _, r := range h.snapshot() {
		f := recordFields(r)
		if f["event"] == event && f["backend"] == backend && f["reason"] == reason {
			n++
		}
	}
	return n
}

// eventCount returns how many captured records carry exactly this event for
// this backend, regardless of reason. It backs the "exactly one transition of
// this kind" assertions that must not be satisfiable by a second line with a
// different reason.
func (h *captureHandler) eventCount(event, backend string) int {
	n := 0
	for _, r := range h.snapshot() {
		f := recordFields(r)
		if f["event"] == event && f["backend"] == backend {
			n++
		}
	}
	return n
}

// transitionRecords returns every captured record carrying an `event` field —
// i.e. every health/circuit transition line, excluding the proxy's per-request
// lines — for baseline "no transitions yet" assertions.
func (h *captureHandler) transitionRecords() []slog.Record {
	var out []slog.Record
	for _, r := range h.snapshot() {
		if recordFields(r)["event"] != "" {
			out = append(out, r)
		}
	}
	return out
}

// transitionRecord returns the single captured record matching all of event,
// backend, and reason, and whether exactly one such record exists. It backs the
// assertions that read a field (such as the drain's cancelled count) off the
// line rather than only counting it.
func (h *captureHandler) transitionRecord(event, backend, reason string) (slog.Record, bool) {
	var out slog.Record
	n := 0
	for _, r := range h.snapshot() {
		f := recordFields(r)
		if f["event"] == event && f["backend"] == backend && f["reason"] == reason {
			out = r
			n++
		}
	}
	return out, n == 1
}

// assertTransitionLogged asserts exactly one transition line matching the
// frozen event/backend/reason triple was captured.
func assertTransitionLogged(t *testing.T, h *captureHandler, event, backend, reason string) {
	t.Helper()
	require.Equal(t, 1, h.transitionCount(event, backend, reason),
		"exactly one %s/%s transition line for backend %s", event, reason, backend)
}

// assertTransitionNotLogged asserts no captured record matches the frozen
// event/backend/reason triple. It is the negative twin of
// assertTransitionLogged, used to pin transitions that must not occur (e.g. a
// Selectable-won Half-Open promotion, ADR-0013 decision 13).
func assertTransitionNotLogged(t *testing.T, h *captureHandler, event, backend, reason string) {
	t.Helper()
	require.Equal(t, 0, h.transitionCount(event, backend, reason),
		"no %s/%s transition line for backend %s", event, reason, backend)
}
