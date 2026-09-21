package circuit

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/logger"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// logRecord is one parsed JSON slog line, mirroring captureLogger in
// internal/proxy/proxy_test.go and internal/health/transition_log_test.go.
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

// discardLogger drops all output, for tests that exercise a logging code path
// without asserting on its lines.
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

// TestBreakerLogsCircuitOpenedOnConsecutiveFailures proves the breaker logs
// exactly one circuit_opened/consecutive_failures line when the consecutive
// failure count reaches the threshold — and no further line for a sustained
// run of failures while the circuit is already Open.
func TestBreakerLogsCircuitOpenedOnConsecutiveFailures(t *testing.T) {
	l, dump := captureLogger(t)
	reg := testRegistry(t)
	br := New(time.Hour, l, metrics.NewCollector())
	b := reg.All()[0]

	for i := 0; i < circuitFailuresBeforeOpen-1; i++ {
		br.ObserveRoundTrip(b, 0, false)
	}
	assert.Empty(t, recordsWithEvent(dump(), logger.EventCircuitOpened),
		"below the threshold the circuit has not opened")

	br.ObserveRoundTrip(b, 0, false)
	opened := recordsWithEvent(dump(), logger.EventCircuitOpened)
	require.Len(t, opened, 1, "the threshold-crossing failure opens and logs once")
	assert.Equal(t, "WARN", opened[0].str("level"))
	assert.Equal(t, logger.ReasonConsecutiveFailures, opened[0].str("reason"))
	assert.Equal(t, b.Name, opened[0].str("backend"))

	for i := 0; i < 3*circuitFailuresBeforeOpen; i++ {
		br.ObserveRoundTrip(b, 0, false)
	}
	assert.Len(t, recordsWithEvent(dump(), logger.EventCircuitOpened), 1,
		"failures observed while already Open change nothing and log nothing")
}

// TestBreakerLogsCircuitHalfOpenedOnTrialAdmission proves the call that
// promotes Open→Half-Open and takes the trial logs exactly one
// circuit_half_opened/cooldown_elapsed line at INFO.
func TestBreakerLogsCircuitHalfOpenedOnTrialAdmission(t *testing.T) {
	const cooldown = 50 * time.Millisecond
	l, dump := captureLogger(t)
	reg := testRegistry(t)
	br := New(cooldown, l, metrics.NewCollector())
	b := reg.All()[0]
	for i := 0; i < circuitFailuresBeforeOpen; i++ {
		br.ObserveRoundTrip(b, 0, false)
	}
	time.Sleep(200 * time.Millisecond)

	require.True(t, br.Allow(b), "half-open must admit the trial")

	half := recordsWithEvent(dump(), logger.EventCircuitHalfOpened)
	require.Len(t, half, 1, "the promotion-admitting call logs exactly once")
	assert.Equal(t, "INFO", half[0].str("level"))
	assert.Equal(t, logger.ReasonCooldownElapsed, half[0].str("reason"))
	assert.Equal(t, b.Name, half[0].str("backend"))

	assert.False(t, br.Allow(b), "the trial slot is now taken")
	assert.Len(t, recordsWithEvent(dump(), logger.EventCircuitHalfOpened), 1,
		"a denied admission reaches no new transition")
}

// TestBreakerLogsCircuitClosedOnTrialSuccess proves a successful half-open
// trial logs exactly one circuit_closed/trial_success line at INFO.
func TestBreakerLogsCircuitClosedOnTrialSuccess(t *testing.T) {
	const cooldown = 50 * time.Millisecond
	l, dump := captureLogger(t)
	reg := testRegistry(t)
	br := New(cooldown, l, metrics.NewCollector())
	b := reg.All()[0]
	for i := 0; i < circuitFailuresBeforeOpen; i++ {
		br.ObserveRoundTrip(b, 0, false)
	}
	time.Sleep(200 * time.Millisecond)
	require.True(t, br.Allow(b), "half-open must admit the trial")

	br.ObserveRoundTrip(b, 0, true)

	closed := recordsWithEvent(dump(), logger.EventCircuitClosed)
	require.Len(t, closed, 1, "resolving the trial with a success closes and logs once")
	assert.Equal(t, "INFO", closed[0].str("level"))
	assert.Equal(t, logger.ReasonTrialSuccess, closed[0].str("reason"))
	assert.Equal(t, b.Name, closed[0].str("backend"))

	opened := recordsWithEvent(dump(), logger.EventCircuitOpened)
	require.Len(t, opened, 1, "only the initial opening is logged; a successful trial does not reopen")
	assert.Equal(t, logger.ReasonConsecutiveFailures, opened[0].str("reason"))
}

// TestBreakerLogsCircuitReopenedOnTrialFailure proves a failed half-open trial
// logs circuit_opened/trial_failure (not consecutive_failures), distinct from
// the initial Closed→Open opening.
func TestBreakerLogsCircuitReopenedOnTrialFailure(t *testing.T) {
	const cooldown = 50 * time.Millisecond
	l, dump := captureLogger(t)
	reg := testRegistry(t)
	br := New(cooldown, l, metrics.NewCollector())
	b := reg.All()[0]
	for i := 0; i < circuitFailuresBeforeOpen; i++ {
		br.ObserveRoundTrip(b, 0, false)
	}
	time.Sleep(200 * time.Millisecond)
	require.True(t, br.Allow(b), "half-open must admit the trial")

	br.ObserveRoundTrip(b, 0, false)

	opened := recordsWithEvent(dump(), logger.EventCircuitOpened)
	require.Len(t, opened, 2, "the initial opening and the failed trial each log once")
	assert.Equal(t, logger.ReasonConsecutiveFailures, opened[0].str("reason"))
	assert.Equal(t, logger.ReasonTrialFailure, opened[1].str("reason"),
		"a failed trial reopens with trial_failure, not consecutive_failures")
	assert.Equal(t, "WARN", opened[1].str("level"))
	assert.Equal(t, b.Name, opened[1].str("backend"))
}

// TestBreakerDoesNotLogScanWonHalfOpenPromotion pins the documented, permanent
// gap: a Half-Open promotion whose CAS is won by a Registry.Selectable() scan
// (here, Breaker.Open → Backend.CircuitOpen) is never logged, because the later
// trial admission did not itself perform the promotion.
func TestBreakerDoesNotLogScanWonHalfOpenPromotion(t *testing.T) {
	const cooldown = 50 * time.Millisecond
	l, dump := captureLogger(t)
	reg := testRegistry(t)
	br := New(cooldown, l, metrics.NewCollector())
	b := reg.All()[0]
	for i := 0; i < circuitFailuresBeforeOpen; i++ {
		br.ObserveRoundTrip(b, 0, false)
	}
	time.Sleep(200 * time.Millisecond)

	require.False(t, br.Open(b), "the scan reads the circuit and promotes Open→Half-Open")
	require.True(t, br.Allow(b), "the promoted circuit then admits the trial")

	assert.Empty(t, recordsWithEvent(dump(), logger.EventCircuitHalfOpened),
		"a promotion won by a Selectable scan has no logger path and is never logged")
}
