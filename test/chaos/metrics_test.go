package chaos_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// gaugeValueOK reads one series of one metric family from the collector's
// private registry, matching on the supplied labels. It returns false when no
// such series exists yet. It never touches *testing.T, so it is safe inside a
// require.Eventually condition.
//
// The collector's instruments are unexported (internal/metrics is a leaf
// package), so testutil.ToFloat64 cannot name the target GaugeVec from this
// external test package. testutil.GatherAndCompare does accept the exported
// Registry, but it compares a whole metric family — here three seeded backends
// by three circuit states — which makes a single-series assertion brittle;
// gathering the registry and selecting the exact series is the minimal read
// testutil wraps. Label pairs are handled through type inference so this
// package need not import the Prometheus client_model package directly.
func gaugeValueOK(c *metrics.Collector, name string, labels map[string]string) (float64, bool) {
	families, err := c.Registry().Gather()
	if err != nil {
		return 0, false
	}
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			match := true
			for k, want := range labels {
				found := false
				for _, lp := range m.GetLabel() {
					if lp.GetName() == k && lp.GetValue() == want {
						found = true
						break
					}
				}
				if !found {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			if g := m.GetGauge(); g != nil {
				return g.GetValue(), true
			}
		}
	}
	return 0, false
}

// assertGauge asserts one series of one gauge family reads want.
func assertGauge(t *testing.T, c *metrics.Collector, name string, labels map[string]string, want float64) {
	t.Helper()
	got, ok := gaugeValueOK(c, name, labels)
	require.True(t, ok, "series %s%v must exist", name, labels)
	require.Equal(t, want, got, "gauge %s%v", name, labels)
}

// requireGaugeEventually polls until one series of one gauge family reads
// want, with the chaos tests' -race-generous deadline.
func requireGaugeEventually(t *testing.T, c *metrics.Collector, name string, labels map[string]string, want float64, msg string, args ...any) {
	t.Helper()
	require.Eventually(t, func() bool {
		got, ok := gaugeValueOK(c, name, labels)
		return ok && got == want
	}, eventuallyDeadline, eventuallyTick, append([]any{msg}, args...)...)
}
