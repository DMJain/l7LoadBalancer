package health

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/logger"
)

// logRecord is one parsed JSON slog line, mirroring the captureLogger seam in
// internal/proxy/proxy_test.go.
type logRecord map[string]any

func (r logRecord) str(key string) string {
	s, _ := r[key].(string)
	return s
}

// captureLogger returns a logger writing JSON to an in-memory buffer, plus a
// function that parses every line emitted so far.
func captureLogger(t *testing.T) (*slog.Logger, func() []logRecord) {
	t.Helper()
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, nil))

	dump := func() []logRecord {
		var out []logRecord
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if line == "" {
				continue
			}
			rec := logRecord{}
			require.NoError(t, json.Unmarshal([]byte(line), &rec))
			out = append(out, rec)
		}
		return out
	}
	return l, dump
}

// discardLogger drops all output, for tests that exercise logging code paths
// without asserting on their lines.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// recordsWithEvent returns the captured records whose transition event field
// equals event.
func recordsWithEvent(recs []logRecord, event string) []logRecord {
	var out []logRecord
	for _, r := range recs {
		if r.str("event") == event {
			out = append(out, r)
		}
	}
	return out
}

// TestCheckerLogsEjectionOncePerFailureStreak proves the active checker emits
// exactly one health_ejected line per genuine ejection — at the threshold
// crossing, not once per probe for the rest of the streak.
func TestCheckerLogsEjectionOncePerFailureStreak(t *testing.T) {
	l, dump := captureLogger(t)
	reg := newTestRegistry(t, statusServer(t, http.StatusInternalServerError).URL)
	c := New(reg, time.Second, time.Second, l)
	b := reg.All()[0]
	p := c.newProber(b)

	for i := 0; i < 2*probeFailuresBeforeUnhealthy; i++ {
		assert.False(t, p.probeOnce(context.Background()))
	}

	ejected := recordsWithEvent(dump(), logger.EventHealthEjected)
	require.Len(t, ejected, 1, "one log line per failure streak, not one per probe")
	assert.Equal(t, "WARN", ejected[0].str("level"))
	assert.Equal(t, logger.ReasonProbeFailures, ejected[0].str("reason"))
	assert.Equal(t, b.Name, ejected[0].str("backend"))
}

// TestCheckerLogsReinstatementOncePerSuccessStreak proves recovery after an
// ejection emits exactly one health_reinstated line, at the success-threshold
// crossing and not once per probe afterwards.
func TestCheckerLogsReinstatementOncePerSuccessStreak(t *testing.T) {
	l, dump := captureLogger(t)
	reg := newTestRegistry(t, statusServer(t, http.StatusOK).URL)
	c := New(reg, time.Second, time.Second, l)
	b := reg.All()[0]
	p := c.newProber(b)
	b.MarkUnhealthy()

	for i := 0; i < 2*probeSuccessesBeforeHealthy; i++ {
		assert.True(t, p.probeOnce(context.Background()))
	}

	reinstated := recordsWithEvent(dump(), logger.EventHealthReinstated)
	require.Len(t, reinstated, 1, "one log line per success streak, not one per probe")
	assert.Equal(t, "INFO", reinstated[0].str("level"))
	assert.Equal(t, logger.ReasonProbeRecovered, reinstated[0].str("reason"))
	assert.Equal(t, b.Name, reinstated[0].str("backend"))
}

// TestCheckerDoesNotLogReinstatementWhileAlreadyHealthy proves the log is
// gated on a genuine transition, not merely the success counter crossing its
// threshold: an always-healthy backend (as every backend starts) must not emit
// a spurious health_reinstated line.
func TestCheckerDoesNotLogReinstatementWhileAlreadyHealthy(t *testing.T) {
	l, dump := captureLogger(t)
	reg := newTestRegistry(t, statusServer(t, http.StatusOK).URL)
	c := New(reg, time.Second, time.Second, l)
	b := reg.All()[0]
	p := c.newProber(b)
	require.True(t, b.IsHealthy(), "NewRegistry starts every backend healthy")

	for i := 0; i < 2*probeSuccessesBeforeHealthy; i++ {
		p.probeOnce(context.Background())
	}

	assert.Empty(t, recordsWithEvent(dump(), logger.EventHealthReinstated),
		"a backend that never left the healthy state has not been reinstated")
}

// TestCheckerDoesNotLogEjectionWhileAlreadyUnhealthy proves a backend already
// ejected by passive outlier detection emits no second, misleading
// health_ejected/probe_failures line when active probes later cross their own
// failure threshold.
func TestCheckerDoesNotLogEjectionWhileAlreadyUnhealthy(t *testing.T) {
	l, dump := captureLogger(t)
	reg := newTestRegistry(t, statusServer(t, http.StatusInternalServerError).URL)
	c := New(reg, time.Second, time.Second, l)
	b := reg.All()[0]
	p := c.newProber(b)
	b.MarkUnhealthy() // as passive outlier detection would

	for i := 0; i < 2*probeFailuresBeforeUnhealthy; i++ {
		p.probeOnce(context.Background())
	}

	assert.Empty(t, recordsWithEvent(dump(), logger.EventHealthEjected),
		"an already-ejected backend has no genuine active-probe ejection to log")
}

// TestOutlierDetectorLogsEjectionOncePerEpisode proves the passive detector
// emits exactly one health_ejected line per ejection episode, not one per
// failure in the burst.
func TestOutlierDetectorLogsEjectionOncePerEpisode(t *testing.T) {
	l, dump := captureLogger(t)
	b := outlierBackends(t, 1)[0]
	d := NewOutlierDetector(l)

	for i := 0; i < outlierFailuresBeforeEject+3*outlierWindowSize; i++ {
		d.ObserveRoundTrip(b, time.Millisecond, false)
	}

	ejected := recordsWithEvent(dump(), logger.EventHealthEjected)
	require.Len(t, ejected, 1, "one log line per ejection episode, not one per failure")
	assert.Equal(t, "WARN", ejected[0].str("level"))
	assert.Equal(t, logger.ReasonOutlierWindow, ejected[0].str("reason"))
	assert.Equal(t, b.Name, ejected[0].str("backend"))
}

// TestOutlierDetectorLogsEjectionPerEpisode proves a backend reinstated by an
// active probe begins a fresh episode, and its next ejection logs its own
// line.
func TestOutlierDetectorLogsEjectionPerEpisode(t *testing.T) {
	l, dump := captureLogger(t)
	b := outlierBackends(t, 1)[0]
	d := NewOutlierDetector(l)

	for i := 0; i < outlierFailuresBeforeEject; i++ {
		d.ObserveRoundTrip(b, time.Millisecond, false)
	}
	require.Len(t, recordsWithEvent(dump(), logger.EventHealthEjected), 1)

	// Recovery is the active probe's job; the detector only observes it.
	b.MarkHealthy()
	for i := 0; i < outlierFailuresBeforeEject; i++ {
		d.ObserveRoundTrip(b, time.Millisecond, false)
	}
	assert.Len(t, recordsWithEvent(dump(), logger.EventHealthEjected), 2,
		"a fresh ejection episode logs its own line")
}
