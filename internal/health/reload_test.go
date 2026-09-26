package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// TestCheckerAddStartsProbingAndRemoveStopsIt covers the whole add/remove
// lifecycle for an added backend: Add starts a prober (immediately, per
// ADR-0015 decision 10), and Remove stops it for good.
func TestCheckerAddStartsProbingAndRemoveStopsIt(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	reg := newTestRegistry(t, srv.URL)
	c := New(reg, 5*time.Millisecond, time.Second, discardLogger(), metrics.NewCollector())
	b := reg.All()[0]

	c.Add(context.Background(), b)
	require.Eventually(t, func() bool { return hits.Load() >= 1 }, time.Second, time.Millisecond,
		"Add must start probing the backend")

	c.Remove(b)
	require.True(t, b.IsHealthy(), "a successful probe must have admitted the added backend")

	time.Sleep(20 * time.Millisecond) // let any in-flight probe settle
	settled := hits.Load()
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, settled, hits.Load(), "no probes may run after Remove")
}

// TestCheckerAddProbesImmediately proves an added prober does not wait a full
// interval before its first probe: with an hour-long interval, admission can
// only come from the immediate probe.
func TestCheckerAddProbesImmediately(t *testing.T) {
	reg := newTestRegistry(t, statusServer(t, http.StatusOK).URL)
	c := New(reg, time.Hour, time.Second, discardLogger(), metrics.NewCollector())
	b := reg.All()[0]

	c.Add(context.Background(), b)

	require.Eventually(t, b.IsHealthy, time.Second, time.Millisecond,
		"an added prober must probe immediately, not after one interval")
	c.Remove(b)
}

// TestCheckerRemoveCancelsProbeInFlight proves a probe blocked mid-flight when
// its backend is removed never applies its outcome: Remove returns only after
// the prober has observed cancellation, and no health transition is logged or
// written to the gauge.
func TestCheckerRemoveCancelsProbeInFlight(t *testing.T) {
	l, dump := captureLogger(t)
	c := metrics.NewCollector()
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	reg := newTestRegistry(t, srv.URL)
	chk := New(reg, time.Hour, time.Hour, l, c)
	b := reg.All()[0]
	c.SetBackendHealthy(b.Name, false) // as reload seeds a fresh added backend

	chk.Add(context.Background(), b)
	<-entered // the immediate probe is now blocked inside the handler

	chk.Remove(b) // cancels the prober's context and waits for it to exit
	close(release)

	require.False(t, b.IsHealthy(), "a cancelled probe must not admit the backend")
	assertHealthyGauge(t, c, b.Name, 0)
	assert.Empty(t, recordsWithEvent(dump(), logger.EventHealthReinstated),
		"a probe in flight at removal emits no transition log")
}

// TestCheckerAddedBackendAdmittedOnFirstSuccess pins the added-backend
// admission rule directly: one successful probe marks it healthy with exactly
// one health_reinstated line carrying reason initial_probe (not
// probe_recovered), and drives the healthy gauge 0 -> 1.
func TestCheckerAddedBackendAdmittedOnFirstSuccess(t *testing.T) {
	l, dump := captureLogger(t)
	c := metrics.NewCollector()
	reg := newTestRegistry(t, statusServer(t, http.StatusOK).URL)
	chk := New(reg, time.Hour, time.Hour, l, c)
	b := reg.All()[0]
	b.MarkUnhealthy()
	c.SetBackendHealthy(b.Name, false)

	p := chk.newProber(b)
	p.added = true
	require.True(t, p.probeOnce(context.Background()))

	assert.True(t, b.IsHealthy(), "one successful probe must admit an added backend")
	assertHealthyGauge(t, c, b.Name, 1)
	reinstated := recordsWithEvent(dump(), logger.EventHealthReinstated)
	require.Len(t, reinstated, 1, "exactly one line at first admission")
	assert.Equal(t, logger.ReasonInitialProbe, reinstated[0].str("reason"))
	assert.Equal(t, b.Name, reinstated[0].str("backend"))
}

// TestCheckerAddedBackendFailingFirstProbeEmitsNothing proves a backend that
// was never healthy produces no false transition: its first probe failing
// leaves it unhealthy, writes no gauge, and logs nothing.
func TestCheckerAddedBackendFailingFirstProbeEmitsNothing(t *testing.T) {
	l, dump := captureLogger(t)
	c := metrics.NewCollector()
	reg := newTestRegistry(t, statusServer(t, http.StatusInternalServerError).URL)
	chk := New(reg, time.Hour, time.Hour, l, c)
	b := reg.All()[0]
	b.MarkUnhealthy()
	c.SetBackendHealthy(b.Name, false)

	p := chk.newProber(b)
	p.added = true
	require.False(t, p.probeOnce(context.Background()))

	assert.False(t, b.IsHealthy())
	assertHealthyGauge(t, c, b.Name, 0)
	assert.Empty(t, dump(), "a never-healthy backend's first failure is not a transition")
}

// TestCheckerAddedBackendRecoveryNeedsTwoSuccesses proves the one-probe
// admission applies only to first admission: once an added backend has been
// admitted, a later ejection is recovered by the normal consecutive-success
// threshold, not a single success.
func TestCheckerAddedBackendRecoveryNeedsTwoSuccesses(t *testing.T) {
	reg := newTestRegistry(t, statusServer(t, http.StatusOK).URL)
	chk := New(reg, time.Hour, time.Hour, discardLogger(), metrics.NewCollector())
	b := reg.All()[0]
	b.MarkUnhealthy()

	p := chk.newProber(b)
	p.added = true
	require.True(t, p.probeOnce(context.Background()))
	require.True(t, b.IsHealthy(), "the added backend is admitted on its first success")

	b.MarkUnhealthy() // as passive outlier detection would

	require.True(t, p.probeOnce(context.Background()))
	assert.False(t, b.IsHealthy(), "recovery after ejection must not happen on one success")

	require.True(t, p.probeOnce(context.Background()))
	assert.True(t, b.IsHealthy(), "two consecutive successes must recover it")
}

// TestCheckerProbeRoundCompleteSurvivesAddRemove pins ADR-0014 decision 4: the
// one-shot startup latch is never cleared by a reload's add/remove.
func TestCheckerProbeRoundCompleteSurvivesAddRemove(t *testing.T) {
	srv := statusServer(t, http.StatusOK)
	reg := newTestRegistry(t, srv.URL)
	c := New(reg, time.Hour, time.Hour, discardLogger(), metrics.NewCollector())

	require.True(t, c.newProber(reg.All()[0]).probeOnce(context.Background()))
	require.True(t, c.ProbeRoundComplete())

	addedReg, err := backend.NewRegistry([]config.BackendConfig{{Name: "late", URL: srv.URL}})
	require.NoError(t, err)
	late := addedReg.All()[0]

	c.Add(context.Background(), late)
	c.Remove(late)

	assert.True(t, c.ProbeRoundComplete(), "add/remove must never clear the startup latch")
}
