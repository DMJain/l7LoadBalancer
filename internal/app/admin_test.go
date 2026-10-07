package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
)

// adminTestApp builds and runs an app whose client and metrics listeners are
// on free loopback ports, with the given backends, logging into capture. It
// returns the app, its config, and the metrics listener's base URL. Run is torn
// down on cleanup.
func adminTestApp(t *testing.T, capture *reloadLogCapture, backends []config.BackendConfig) (*App, *config.Config, string) {
	t.Helper()
	cfg := testConfig(backends)
	cfg.Listen = freeAddr(t)
	metricsAddr := freeAddr(t)
	cfg.Metrics.Listen = &metricsAddr
	require.NoError(t, cfg.Validate())

	application, err := Build(cfg, slog.New(capture))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatal("Run did not return after cancellation")
		}
	})

	base := "http://" + metricsAddr
	waitForAdmin(t, base)
	return application, cfg, base
}

// waitForAdmin polls the last-reload route until the metrics listener answers.
func waitForAdmin(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(base + "/admin/reload/last")
		if err == nil {
			resp.Body.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("metrics listener %s never came up: %v", base, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// yamlBody marshals cfg to the YAML a caller would post. A copy of the loaded
// config with different backends is the natural "valid" body: every
// non-backend field matches, so only the backend list changes.
func yamlBody(t *testing.T, cfg *config.Config) []byte {
	t.Helper()
	data, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	return data
}

// withBackends returns a shallow copy of cfg whose backend list is replaced.
func withBackends(cfg *config.Config, backends []config.BackendConfig) *config.Config {
	c := *cfg
	c.Backends = backends
	return &c
}

type lastReloadBody struct {
	Result string `json:"result"`
	Time   string `json:"time"`
	Error  string `json:"error"`
}

func postReload(t *testing.T, base string, body []byte) (int, lastReloadBody) {
	t.Helper()
	resp, err := http.Post(base+"/admin/reload", "application/yaml", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	var out lastReloadBody
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	if len(data) > 0 && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		require.NoError(t, json.Unmarshal(data, &out), "body: %s", data)
	}
	return resp.StatusCode, out
}

func getLast(t *testing.T, base string) lastReloadBody {
	t.Helper()
	resp, err := http.Get(base + "/admin/reload/last")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json"))
	var out lastReloadBody
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

// TestAdminReloadRejectionsLeaveConfigUnchanged is the D2 end-to-end check from
// the demo-dashboard gap report: each kind of invalid body is
// rejected with 422 and a readable message, the rejection is recorded for
// GET /admin/reload/last, it is logged like a SIGHUP rejection, and the running
// config and registry are untouched (ADR-0015 decisions 13–16).
func TestAdminReloadRejectionsLeaveConfigUnchanged(t *testing.T) {
	silenceDefault(t)

	a := serveFixed(t, "backend-a")
	b := serveFixed(t, "backend-b")
	capture := &reloadLogCapture{}
	application, cfg, base := adminTestApp(t, capture, []config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-b", URL: b.URL},
	})

	assert.Equal(t, "none", getLast(t, base).Result, "before any attempt the last result is none")

	unknownAlgo := yamlBody(t, cfg)
	unknownAlgo = bytes.Replace(unknownAlgo, []byte("round_robin"), []byte("p3c_ewma"), 1)

	nonBackend := withBackends(cfg, cfg.Backends)
	nonBackend.Listen = "127.0.0.1:1"

	tests := []struct {
		name       string
		body       []byte
		wantSubstr string
		wantReason string
	}{
		{"unknown algorithm", unknownAlgo, `unsupported algorithm "p3c_ewma"`, logger.ReasonValidationError},
		{"duplicate backend name", yamlBody(t, withBackends(cfg, []config.BackendConfig{
			{Name: "backend-a", URL: a.URL},
			{Name: "backend-a", URL: b.URL},
		})), `duplicate backend name "backend-a"`, logger.ReasonValidationError},
		{"malformed backend url", yamlBody(t, withBackends(cfg, []config.BackendConfig{
			{Name: "backend-a", URL: "ftp://127.0.0.1:9"},
		})), `backend "backend-a" url must use http or https scheme`, logger.ReasonValidationError},
		{"unparseable yaml", []byte("listen: [\n"), "config: parse admin request body", logger.ReasonParseError},
		{"non-backend change", yamlBody(t, nonBackend), "non-backend fields changed: [listen]", logger.ReasonNonBackendChange},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := len(capture.withEvent(logger.EventConfigReloadFailed))

			code, out := postReload(t, base, tc.body)
			assert.Equal(t, http.StatusUnprocessableEntity, code)
			assert.Equal(t, "rejected", out.Result)
			assert.Contains(t, out.Error, tc.wantSubstr)

			last := getLast(t, base)
			assert.Equal(t, "rejected", last.Result)
			assert.Contains(t, last.Error, tc.wantSubstr)
			_, err := time.Parse(time.RFC3339, last.Time)
			assert.NoError(t, err, "time must be RFC 3339, got %q", last.Time)

			failed := capture.withEvent(logger.EventConfigReloadFailed)
			require.Len(t, failed, before+1, "exactly one config reload failed line per attempt")
			reason, ok := recordString(failed[len(failed)-1], "reason")
			require.True(t, ok)
			assert.Equal(t, tc.wantReason, reason)

			assert.Same(t, cfg, application.LoadedConfig(), "the loaded config must be untouched")
			assert.Equal(t, []string{"backend-a", "backend-b"}, backendNames(application.Registry().All()))
		})
	}

	// The previous config keeps serving.
	rec := httptest.NewRecorder()
	application.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestAdminReloadAppliesValidBody proves a valid body changes the backend set
// through the same reload operation SIGHUP uses, is recorded as success, and
// that an added backend is admitted afterwards — which only happens if its
// prober runs on the application's run context rather than the request's
// (ADR-0015 decision 17).
func TestAdminReloadAppliesValidBody(t *testing.T) {
	silenceDefault(t)

	a := serveFixed(t, "backend-a")
	b := serveFixed(t, "backend-b")
	c := serveFixed(t, "backend-c")
	capture := &reloadLogCapture{}
	application, cfg, base := adminTestApp(t, capture, []config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-b", URL: b.URL},
	})

	next := withBackends(cfg, []config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-c", URL: c.URL},
	})
	code, out := postReload(t, base, yamlBody(t, next))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "success", out.Result)
	assert.Empty(t, out.Error)

	assert.Equal(t, []string{"backend-a", "backend-c"}, backendNames(application.Registry().All()))
	assert.Equal(t, "success", getLast(t, base).Result)
	require.Len(t, capture.withEvent(logger.EventConfigReloaded), 1)

	// The request has long finished; the added backend's prober must still be
	// running to admit it (one successful probe, ADR-0015 decision 10).
	require.Eventually(t, func() bool {
		for _, be := range application.Registry().Selectable() {
			if be.Name == "backend-c" {
				return true
			}
		}
		return false
	}, 5*time.Second, 50*time.Millisecond, "backend-c must be admitted by its prober after the request returns")
}

// TestAdminReloadMethodAndSize pins the endpoint's HTTP contract: other methods
// get 405, an oversized body gets 413, and neither is recorded as an attempt.
func TestAdminReloadMethodAndSize(t *testing.T) {
	silenceDefault(t)

	a := serveFixed(t, "backend-a")
	_, _, base := adminTestApp(t, &reloadLogCapture{}, []config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
	})

	resp, err := http.Get(base + "/admin/reload")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)

	big := bytes.Repeat([]byte("#"), adminReloadMaxBody+1)
	code, _ := postReload(t, base, big)
	assert.Equal(t, http.StatusRequestEntityTooLarge, code)

	assert.Equal(t, "none", getLast(t, base).Result, "neither a 405 nor a 413 is a reload attempt")
}

// TestAdminReloadBeforeRunIsUnavailable proves the endpoint refuses to reload
// before Run has supplied the run context: there is no context the added
// probers and drains could live on.
func TestAdminReloadBeforeRunIsUnavailable(t *testing.T) {
	silenceDefault(t)

	cfg := testConfig([]config.BackendConfig{{Name: "backend-a", URL: "http://127.0.0.1:9001"}})
	require.NoError(t, cfg.Validate())
	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	application.metricsSrv.Handler.ServeHTTP(rec,
		httptest.NewRequest(http.MethodPost, "/admin/reload", bytes.NewReader(yamlBody(t, cfg))))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, ReloadResultNone, application.LastReload().Result)
}

// TestReloadFromRecordsAndLogsLikeSIGHUP covers the shared operation directly:
// a load failure logs parse_error, a validation failure logs validation_error,
// a success replaces the loaded config, and each outcome is the last result.
func TestReloadFromRecordsAndLogsLikeSIGHUP(t *testing.T) {
	silenceDefault(t)

	a := serveFixed(t, "backend-a")
	b := serveFixed(t, "backend-b")
	cfg := testConfig([]config.BackendConfig{{Name: "backend-a", URL: a.URL}})
	require.NoError(t, cfg.Validate())
	capture := &reloadLogCapture{}
	application, err := Build(cfg, slog.New(capture))
	require.NoError(t, err)
	ctx := context.Background()

	st, err := application.ReloadFrom(ctx, func() (*config.Config, error) {
		return nil, fmt.Errorf("config: load /nope: boom")
	})
	require.Error(t, err)
	assert.Equal(t, ReloadResultRejected, st.Result)
	assert.Equal(t, "config: load /nope: boom", st.Error)
	assert.Equal(t, 1, countReason(capture, logger.ReasonParseError))

	invalid := testConfig(nil)
	st, err = application.ReloadFrom(ctx, func() (*config.Config, error) { return invalid, nil })
	require.Error(t, err)
	assert.Equal(t, ReloadResultRejected, st.Result)
	assert.Equal(t, 1, countReason(capture, logger.ReasonValidationError))

	valid := testConfig([]config.BackendConfig{{Name: "backend-b", URL: b.URL}})
	st, err = application.ReloadFrom(ctx, func() (*config.Config, error) { return valid, nil })
	require.NoError(t, err)
	assert.Equal(t, ReloadResultSuccess, st.Result)
	assert.Same(t, valid, application.LoadedConfig())
	assert.Equal(t, st, application.LastReload())
	assert.False(t, st.Time.IsZero())
}

// TestReloadsAreSerialized drives SIGHUP-style ReloadFrom calls, direct Reload
// calls and endpoint posts concurrently. Run under -race, it proves the one
// reload mutex makes two triggers safe (ADR-0015 decision 14): every attempt
// completes, and the registry always matches the loaded config at the end.
func TestReloadsAreSerialized(t *testing.T) {
	silenceDefault(t)

	a := serveFixed(t, "backend-a")
	b := serveFixed(t, "backend-b")
	application, cfg, base := adminTestApp(t, &reloadLogCapture{}, []config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
	})
	sets := [][]config.BackendConfig{
		{{Name: "backend-a", URL: a.URL}},
		{{Name: "backend-b", URL: b.URL}},
		{{Name: "backend-a", URL: a.URL}, {Name: "backend-b", URL: b.URL}},
	}

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		next := withBackends(cfg, sets[i%len(sets)])
		wg.Add(1)
		go func(i int, next *config.Config) {
			defer wg.Done()
			switch i % 3 {
			case 0:
				_, _ = application.ReloadFrom(context.Background(), func() (*config.Config, error) {
					c := *next
					return &c, nil
				})
			case 1:
				c := *next
				assert.NoError(t, c.Validate())
				_ = application.Reload(context.Background(), &c)
			default:
				code, _ := postReload(t, base, yamlBody(t, next))
				assert.Equal(t, http.StatusOK, code)
			}
		}(i, next)
	}
	wg.Wait()

	want := make([]string, 0, 2)
	for _, be := range application.LoadedConfig().Backends {
		want = append(want, be.Name)
	}
	assert.Equal(t, want, backendNames(application.Registry().All()))
	assert.Equal(t, ReloadResultSuccess, application.LastReload().Result)
}

func countReason(capture *reloadLogCapture, reason string) int {
	n := 0
	for _, r := range capture.withEvent(logger.EventConfigReloadFailed) {
		if got, ok := recordString(r, "reason"); ok && got == reason {
			n++
		}
	}
	return n
}
