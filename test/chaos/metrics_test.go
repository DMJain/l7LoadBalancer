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
// external test package. Gathering the registry directly is the same
// S3.T6.3-family path testutil wraps, and is the only read the collector
// exposes. Label pairs are handled through type inference so this package
// need not import the Prometheus client_model package directly.
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
