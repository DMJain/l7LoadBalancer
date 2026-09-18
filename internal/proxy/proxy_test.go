package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/balancer"
	"github.com/DMJain/l7LoadBalancer/internal/config"
)

// TestMain silences the process default logger. Proxy reads slog.Default() at
// construction (ADR-0007), so tests that assert on logs swap the default in
// for the duration of one test.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// backendEntry pairs a backend's configured identity with the URL it should
// point at — usually a live httptest.Server.URL.
type backendEntry struct {
	name string
	url  string
}

// registryFrom builds a real backend.Registry from entries, preserving order
// (which LeastConnections' tie-break and RoundRobin's rotation depend on).
func registryFrom(t *testing.T, entries ...backendEntry) *backend.Registry {
	t.Helper()
	cfgs := make([]config.BackendConfig, len(entries))
	for i, e := range entries {
		cfgs[i] = config.BackendConfig{Name: e.name, URL: e.url}
	}
	reg, err := backend.NewRegistry(cfgs)
	require.NoError(t, err)
	return reg
}

// startBackend starts an httptest.Server that identifies itself in the
// response body, so a test can observe which backend served a request.
func startBackend(t *testing.T, name string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, name)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// deadBackendURL returns a URL that accepts TCP connections and immediately
// closes them, so a round trip fails deterministically with a transport error
// rather than depending on an unbound fixed port.
func deadBackendURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return "http://" + l.Addr().String()
}

// useLogger swaps the process default logger for the duration of one test, so
// a Proxy constructed afterwards picks it up via slog.Default().
func useLogger(t *testing.T, logger *slog.Logger) {
	t.Helper()
	prev := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prev) })
}

func TestProxyDistributionMatchesSelector(t *testing.T) {
	names := []string{"backend-a", "backend-b", "backend-c"}
	servers := make(map[string]*httptest.Server, len(names))
	for _, name := range names {
		servers[name] = startBackend(t, name)
	}

	reg := registryFrom(t,
		backendEntry{"backend-a", servers["backend-a"].URL},
		backendEntry{"backend-b", servers["backend-b"].URL},
		backendEntry{"backend-c", servers["backend-c"].URL},
	)
	front := httptest.NewServer(New(reg, balancer.NewRoundRobin(reg)))
	t.Cleanup(front.Close)

	got := make([]string, 0, 6)
	for i := 0; i < 6; i++ {
		resp, err := front.Client().Get(front.URL)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusOK, resp.StatusCode)
		got = append(got, string(body))
	}

	assert.Equal(t, []string{
		"backend-a", "backend-b", "backend-c",
		"backend-a", "backend-b", "backend-c",
	}, got)
}

func TestProxyNoHealthyBackendReturns503(t *testing.T) {
	tests := []struct {
		name string
		reg  func(t *testing.T) *backend.Registry
	}{
		{
			name: "registry with no backends",
			reg:  func(t *testing.T) *backend.Registry { return registryFrom(t) },
		},
		{
			name: "all backends unhealthy",
			reg: func(t *testing.T) *backend.Registry {
				reg := registryFrom(t, backendEntry{"backend-a", "http://127.0.0.1:1"})
				reg.All()[0].SetHealthy(false)
				return reg
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := tt.reg(t)
			p := New(reg, balancer.NewRoundRobin(reg))

			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		})
	}
}

func TestProxySelectorErrorReturns502(t *testing.T) {
	reg := registryFrom(t, backendEntry{"backend-a", deadBackendURL(t)})
	p := New(reg, errorSelector{err: errors.New("selector exploded")})

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestProxyBackendFailureReturns502AndReleases(t *testing.T) {
	reg := registryFrom(t, backendEntry{"backend-a", deadBackendURL(t)})
	p := New(reg, balancer.NewRoundRobin(reg))

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Equal(t, int64(0), reg.All()[0].ActiveConns(),
		"a failed round trip must still release the active-connection slot")
}

func TestProxyActiveConnsReturnToZeroAfterConcurrentRequests(t *testing.T) {
	const requests = 100

	var inFlight atomic.Int64
	release := make(chan struct{})
	backendSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inFlight.Add(1)
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(backendSrv.Close)

	reg := registryFrom(t, backendEntry{"backend-a", backendSrv.URL})
	front := httptest.NewServer(New(reg, balancer.NewLeastConnections(reg)))
	t.Cleanup(front.Close)

	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := front.Client().Get(front.URL)
			if err != nil {
				t.Errorf("request failed: %v", err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()
	}

	require.Eventually(t, func() bool {
		return inFlight.Load() == int64(requests)
	}, 5*time.Second, 10*time.Millisecond, "all requests should reach the backend and block")

	assert.Equal(t, int64(requests), reg.All()[0].ActiveConns(),
		"all in-flight requests should be counted")

	close(release)
	wg.Wait()

	require.Eventually(t, func() bool {
		return reg.All()[0].ActiveConns() == 0
	}, 5*time.Second, 10*time.Millisecond, "ActiveConns should drain back to zero")
}

// errorSelector is a Selector stub for exercising the proxy's non-sentinel
// error branch. It returns an error that is not ErrNoHealthyBackends.
type errorSelector struct {
	err error
}

func (s errorSelector) Select(context.Context, *http.Request) (*backend.Backend, error) {
	return nil, s.err
}

// logRecord is one parsed JSON slog line.
type logRecord map[string]any

func (r logRecord) msg() string {
	s, _ := r["msg"].(string)
	return s
}

func (r logRecord) level() string {
	s, _ := r["level"].(string)
	return s
}

// captureLogger returns a logger writing JSON to an in-memory buffer, plus a
// function that parses every line emitted so far.
func captureLogger(t *testing.T) (*slog.Logger, func() []logRecord) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

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
	return logger, dump
}

func recordsWithMsg(recs []logRecord, msg string) []logRecord {
	var out []logRecord
	for _, r := range recs {
		if r.msg() == msg {
			out = append(out, r)
		}
	}
	return out
}

func TestProxyLogsRequestCompleteOnSuccess(t *testing.T) {
	logger, dump := captureLogger(t)
	useLogger(t, logger)

	backendSrv := startBackend(t, "backend-a")
	reg := registryFrom(t, backendEntry{"backend-a", backendSrv.URL})
	p := New(reg, balancer.NewRoundRobin(reg))

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	completes := recordsWithMsg(dump(), "request complete")
	require.Len(t, completes, 1)
	got := completes[0]

	assert.Equal(t, "INFO", got.level())
	assert.Equal(t, "backend-a", got["backend"])
	assert.Equal(t, "GET", got["method"])
	assert.Equal(t, float64(http.StatusOK), got["status"])
	assert.Equal(t, "/ok", got["path"])
	assert.NotEmpty(t, got["remote_addr"])
	assert.Contains(t, got, "latency_ms")
}

func TestProxyLogsRequestCompleteOn503(t *testing.T) {
	logger, dump := captureLogger(t)
	useLogger(t, logger)

	reg := registryFrom(t, backendEntry{"backend-a", "http://127.0.0.1:1"})
	reg.All()[0].SetHealthy(false)
	p := New(reg, balancer.NewRoundRobin(reg))

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/none", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	completes := recordsWithMsg(dump(), "request complete")
	require.Len(t, completes, 1)

	assert.Equal(t, "WARN", completes[0].level())
	assert.Equal(t, float64(http.StatusServiceUnavailable), completes[0]["status"])
	assert.Equal(t, "GET", completes[0]["method"])
	assert.Equal(t, "/none", completes[0]["path"])
}

func TestProxyLogsRequestCompleteOn502(t *testing.T) {
	logger, dump := captureLogger(t)
	useLogger(t, logger)

	reg := registryFrom(t, backendEntry{"backend-a", deadBackendURL(t)})
	p := New(reg, balancer.NewRoundRobin(reg))

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dead", nil))
	require.Equal(t, http.StatusBadGateway, rec.Code)

	recs := dump()
	completes := recordsWithMsg(recs, "request complete")
	require.Len(t, completes, 1)

	assert.Equal(t, "WARN", completes[0].level())
	assert.Equal(t, "backend-a", completes[0]["backend"])
	assert.Equal(t, float64(http.StatusBadGateway), completes[0]["status"])

	assert.NotEmpty(t, recordsWithMsg(recs, "backend round-trip failed"),
		"the transport error should be logged with its cause")
}
