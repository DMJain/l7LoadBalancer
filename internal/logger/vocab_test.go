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

func TestEventVocabulary(t *testing.T) {
	cases := []struct {
		name    string
		got     string
		want    string
		subsyst string
	}{
		{"health ejected", EventHealthEjected, "health_ejected", "active health / passive outlier"},
		{"health reinstated", EventHealthReinstated, "health_reinstated", "active health"},
		{"circuit opened", EventCircuitOpened, "circuit_opened", "circuit"},
		{"circuit closed", EventCircuitClosed, "circuit_closed", "circuit"},
		{"circuit half opened", EventCircuitHalfOpened, "circuit_half_opened", "circuit"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.got, "event value for %s", tc.subsyst)
			assert.Regexp(t, snakeCasePattern, tc.got)
		})
	}
}

func TestReasonVocabulary(t *testing.T) {
	cases := []struct {
		name    string
		got     string
		want    string
		subsyst string
	}{
		{"probe failures", ReasonProbeFailures, "probe_failures", "active health"},
		{"probe recovered", ReasonProbeRecovered, "probe_recovered", "active health"},
		{"outlier window", ReasonOutlierWindow, "outlier_window", "passive outlier"},
		{"consecutive failures", ReasonConsecutiveFailures, "consecutive_failures", "circuit"},
		{"trial success", ReasonTrialSuccess, "trial_success", "circuit"},
		{"trial failure", ReasonTrialFailure, "trial_failure", "circuit"},
		{"cooldown elapsed", ReasonCooldownElapsed, "cooldown_elapsed", "circuit"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.got, "reason value for %s", tc.subsyst)
			assert.Regexp(t, snakeCasePattern, tc.got)
		})
	}
}

// TestVocabulariesAreClosedAndDuplicateFree guards the "closed vocabulary"
// property: two values that collide, or a value that gains an accidental
// near-duplicate, would silently split grep results. Each vocabulary's length
// is asserted explicitly so adding a constant is a deliberate edit here too.
func TestVocabulariesAreClosedAndDuplicateFree(t *testing.T) {
	events := []string{
		EventHealthEjected,
		EventHealthReinstated,
		EventCircuitOpened,
		EventCircuitClosed,
		EventCircuitHalfOpened,
	}
	reasons := []string{
		ReasonProbeFailures,
		ReasonProbeRecovered,
		ReasonOutlierWindow,
		ReasonConsecutiveFailures,
		ReasonTrialSuccess,
		ReasonTrialFailure,
		ReasonCooldownElapsed,
	}

	require.Len(t, events, 5, "event vocabulary is exactly five values")
	require.Len(t, reasons, 7, "reason vocabulary is exactly seven values")

	assert.Len(t, unique(events), len(events), "event values must be duplicate-free")
	assert.Len(t, unique(reasons), len(reasons), "reason values must be duplicate-free")
}

func unique(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		set[v] = struct{}{}
	}
	return set
}
