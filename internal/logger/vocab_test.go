package logger

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snakeCasePattern is the shape every transition vocabulary value must have,
// matching the algorithm-identifier convention in internal/config
// (round_robin, least_conn, ...).
var snakeCasePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// vocabCase pins one constant to the exact string it must hold.
type vocabCase struct {
	name      string
	value     string
	want      string
	subsystem string
}

// eventCases and reasonCases are each the single listing of a vocabulary in
// this package: every constant appears once, pinned to its exact string. The
// per-vocabulary tests both iterate the table and derive the duplicate/
// cardinality check from it, so a value cannot be written down in two places
// and drift.
var eventCases = []vocabCase{
	{"health ejected", EventHealthEjected, "health_ejected", "active health / passive outlier"},
	{"health reinstated", EventHealthReinstated, "health_reinstated", "active health"},
	{"circuit opened", EventCircuitOpened, "circuit_opened", "circuit"},
	{"circuit closed", EventCircuitClosed, "circuit_closed", "circuit"},
	{"circuit half opened", EventCircuitHalfOpened, "circuit_half_opened", "circuit"},
}

var reasonCases = []vocabCase{
	{"probe failures", ReasonProbeFailures, "probe_failures", "active health"},
	{"probe recovered", ReasonProbeRecovered, "probe_recovered", "active health"},
	{"outlier window", ReasonOutlierWindow, "outlier_window", "passive outlier"},
	{"consecutive failures", ReasonConsecutiveFailures, "consecutive_failures", "circuit"},
	{"trial success", ReasonTrialSuccess, "trial_success", "circuit"},
	{"trial failure", ReasonTrialFailure, "trial_failure", "circuit"},
	{"cooldown elapsed", ReasonCooldownElapsed, "cooldown_elapsed", "circuit"},
}

func TestEventVocabulary(t *testing.T) {
	for _, tc := range eventCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.value, "event value for %s", tc.subsystem)
			assert.Regexp(t, snakeCasePattern, tc.value)
		})
	}
	assertClosed(t, "event", eventCases, 5)
}

func TestReasonVocabulary(t *testing.T) {
	for _, tc := range reasonCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.value, "reason value for %s", tc.subsystem)
			assert.Regexp(t, snakeCasePattern, tc.value)
		})
	}
	assertClosed(t, "reason", reasonCases, 7)
}

// assertClosed guards the "closed vocabulary" property: a value colliding with
// another would silently split grep results. The explicit want is the intended
// vocabulary size, restated here so adding a value fails the test until the
// size is deliberately updated rather than slipping in unnoticed.
func assertClosed(t *testing.T, kind string, cases []vocabCase, want int) {
	t.Helper()

	values := make([]string, 0, len(cases))
	for _, tc := range cases {
		values = append(values, tc.value)
	}
	require.Len(t, values, want, "%s vocabulary is exactly %d values", kind, want)

	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		seen[v] = struct{}{}
	}
	assert.Len(t, seen, len(values), "%s values must be duplicate-free", kind)
}
