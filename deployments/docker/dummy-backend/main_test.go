package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statsBody is the /stats response contract: the backend's name and how many
// benchmark requests have arrived at it (S5.T5.5.2).
type statsBody struct {
	Backend  string `json:"backend"`
	Requests int64  `json:"requests"`
}

// getStats drives GET /stats through the handler seam and decodes the counter.
func getStats(t *testing.T, h http.Handler) statsBody {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stats", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var got statsBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	return got
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestHandlerEndpoints locks the response contract for the benchmark payload
// endpoints and the /health probe (S5.T4-infra). Sizes are asserted exactly so
// a benchmark never silently measures the wrong payload profile.
func TestHandlerEndpoints(t *testing.T) {
	h := newHandler("backend1", 0, 0, true, discardLogger())

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantLen    int
	}{
		{name: "root identifies the backend", path: "/", wantStatus: http.StatusOK, wantLen: -1},
		{name: "probe is minimal and ok", path: "/health", wantStatus: http.StatusOK, wantLen: len("ok\n")},
		{name: "200 byte payload", path: "/200b", wantStatus: http.StatusOK, wantLen: 200},
		{name: "10 kib payload", path: "/10kb", wantStatus: http.StatusOK, wantLen: 10 * 1024},
		{name: "1 mib payload", path: "/1mb", wantStatus: http.StatusOK, wantLen: 1 << 20},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

			assert.Equal(t, tc.wantStatus, rec.Code)
			if tc.wantLen >= 0 {
				assert.Len(t, rec.Body.Bytes(), tc.wantLen)
			}
			if tc.path == "/" {
				assert.Contains(t, rec.Body.String(), `"backend":"backend1"`)
			}
		})
	}
}

// TestHandlerMethodContract locks the allowed methods per path: GET everywhere;
// POST additionally on the payload paths, where the body is read and discarded so
// the demo traffic generator can send uploads (S5.T16.1); every other method or
// path keeps the pre-existing 405 with Allow: GET.
func TestHandlerMethodContract(t *testing.T) {
	h := newHandler("backend1", 0, 0, true, discardLogger())

	t.Run("post allowed on payload paths", func(t *testing.T) {
		for _, path := range []string{"/200b", "/10kb", "/1mb"} {
			t.Run(path, func(t *testing.T) {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader("body")))
				assert.Equal(t, http.StatusOK, rec.Code)
			})
		}
	})

	t.Run("other methods and paths rejected", func(t *testing.T) {
		cases := []struct {
			method string
			path   string
		}{
			{method: http.MethodPost, path: "/"},
			{method: http.MethodPost, path: "/health"},
			{method: http.MethodPost, path: "/stats"},
			{method: http.MethodPut, path: "/200b"},
			{method: http.MethodDelete, path: "/10kb"},
			{method: http.MethodPatch, path: "/1mb"},
		}
		for _, tc := range cases {
			t.Run(tc.method+" "+tc.path, func(t *testing.T) {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
				assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
				assert.Equal(t, http.MethodGet, rec.Header().Get("Allow"))
			})
		}
	})
}

// TestHealthBypassesChaos proves the probe endpoint is never subject to the
// chaos knobs, so an injected FAIL_RATE cannot make the LB health-checker flap.
func TestHealthBypassesChaos(t *testing.T) {
	h := newHandler("backend1", 0, 1.0, true, discardLogger())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, http.StatusOK, rec.Code)

	// The same failRate must still hit a benchmark payload endpoint.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/200b", nil))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Len(t, rec.Body.Bytes(), 200)
}

// TestEnvBool locks the LOG_REQUESTS parsing contract (S5.T5.5.1): unset or
// empty means the default, only the exact strings "true" and "false" are
// accepted, and anything else is an error naming the variable — the same
// loud-failure strictness as SLEEP_MS/FAIL_RATE.
func TestEnvBool(t *testing.T) {
	tests := []struct {
		name    string
		unset   bool
		raw     string
		want    bool
		wantErr bool
	}{
		{name: "unset falls back to default", unset: true, want: true},
		{name: "empty falls back to default", raw: "", want: true},
		{name: "true enables", raw: "true", want: true},
		{name: "false disables", raw: "false", want: false},
		{name: "arbitrary word is an error", raw: "yes", wantErr: true},
		{name: "numeric is an error", raw: "1", wantErr: true},
		{name: "uppercase is an error", raw: "TRUE", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.unset {
				t.Setenv("LOG_REQUESTS", "sentinel")
				require.NoError(t, os.Unsetenv("LOG_REQUESTS"))
			} else {
				t.Setenv("LOG_REQUESTS", tc.raw)
			}

			got, err := envBool("LOG_REQUESTS", true)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "LOG_REQUESTS")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestHandlerLoggingSwitch proves the per-request log line can be silenced on
// every path — payload, default, /health and the 405 branch — and that it is
// written when logging is on (S5.T5.5.1). The logger is captured, so the
// assertion is on the exact bytes emitted, not on a discard.
func TestHandlerLoggingSwitch(t *testing.T) {
	paths := []struct {
		name   string
		method string
		path   string
	}{
		{name: "payload path", method: http.MethodGet, path: "/200b"},
		{name: "default path", method: http.MethodGet, path: "/"},
		{name: "health path", method: http.MethodGet, path: "/health"},
		{name: "stats path", method: http.MethodGet, path: "/stats"},
		{name: "non-GET branch", method: http.MethodPost, path: "/"},
	}

	t.Run("off writes nothing", func(t *testing.T) {
		for _, tc := range paths {
			t.Run(tc.name, func(t *testing.T) {
				var buf bytes.Buffer
				h := newHandler("backend1", 0, 0, false, slog.New(slog.NewJSONHandler(&buf, nil)))

				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))

				assert.Empty(t, buf.String())
			})
		}
	})

	t.Run("on writes exactly one line per request", func(t *testing.T) {
		for _, tc := range paths {
			t.Run(tc.name, func(t *testing.T) {
				var buf bytes.Buffer
				h := newHandler("backend1", 0, 0, true, slog.New(slog.NewJSONHandler(&buf, nil)))

				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))

				assert.Contains(t, buf.String(), "request complete")
				assert.Equal(t, 1, strings.Count(buf.String(), "\n"))
			})
		}
	})
}

// TestStatsEndpointShape locks the /stats response contract (S5.T5.5.2): 200
// with a JSON body naming the backend and its arrival count. A fresh handler
// reports zero.
func TestStatsEndpointShape(t *testing.T) {
	h := newHandler("backend1", 0, 0, true, discardLogger())

	got := getStats(t, h)
	assert.Equal(t, "backend1", got.Backend)
	assert.EqualValues(t, 0, got.Requests)
}

// TestStatsCountsArrivalsExcludesControl proves the counter counts benchmark
// traffic — the payload and default paths — and not the control endpoints
// /health and /stats themselves (S5.T5.5.2).
func TestStatsCountsArrivalsExcludesControl(t *testing.T) {
	h := newHandler("backend1", 0, 0, true, discardLogger())

	for _, path := range []string{"/200b", "/"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	}

	// Control endpoints must not move the counter.
	for _, path := range []string{"/health", "/stats"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	}

	assert.EqualValues(t, 2, getStats(t, h).Requests)
}

// TestStatsCountsOnArrival proves the increment happens on arrival, before any
// injected latency: a request still sleeping is already reflected by /stats
// while its own response has not returned (S5.T5.5.2).
func TestStatsCountsOnArrival(t *testing.T) {
	h := newHandler("backend1", 300, 0, true, discardLogger())

	done := make(chan struct{})
	go func() {
		defer close(done)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/10kb", nil))
	}()

	deadline := time.Now().Add(2 * time.Second)
	for getStats(t, h).Requests == 0 {
		if time.Now().After(deadline) {
			t.Fatal("arrival was never counted while the request was in flight")
		}
		time.Sleep(time.Millisecond)
	}

	select {
	case <-done:
		t.Fatal("request completed before its arrival was observed")
	default:
	}
}

// TestStatsBypassesChaos proves /stats is a control endpoint like /health: an
// injected failure rate and delay cannot make the count endpoint fail or stall
// (S5.T5.5.2).
func TestStatsBypassesChaos(t *testing.T) {
	h := newHandler("backend1", 5000, 1.0, true, discardLogger())

	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stats", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Less(t, time.Since(start), time.Second, "stats must not sleep")
}

// TestStatsConcurrentArrivals proves concurrent requests are counted exactly,
// which the -race gate checks for correctness at the same time.
func TestStatsConcurrentArrivals(t *testing.T) {
	const n = 100
	h := newHandler("backend1", 0, 0, true, discardLogger())

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/200b", nil))
		}()
	}
	wg.Wait()

	assert.EqualValues(t, n, getStats(t, h).Requests)
}

// TestTLSFilesFromEnv locks the env-var contract: both set enables TLS, neither
// set leaves the backend plain, and a half-configured pair fails loudly.
func TestTLSFilesFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		certEnv  string
		keyEnv   string
		certSet  bool
		keySet   bool
		wantErr  bool
		wantCert string
		wantKey  string
	}{
		{name: "neither set is plaintext", wantCert: "", wantKey: ""},
		{name: "both set enables TLS", certEnv: "/certs/server.crt", keyEnv: "/certs/server.key", certSet: true, keySet: true, wantCert: "/certs/server.crt", wantKey: "/certs/server.key"},
		{name: "cert without key is an error", certEnv: "/certs/server.crt", certSet: true, wantErr: true},
		{name: "key without cert is an error", keyEnv: "/certs/server.key", keySet: true, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.certSet {
				t.Setenv("TLS_CERT_FILE", tc.certEnv)
			} else {
				t.Setenv("TLS_CERT_FILE", "")
			}
			if tc.keySet {
				t.Setenv("TLS_KEY_FILE", tc.keyEnv)
			} else {
				t.Setenv("TLS_KEY_FILE", "")
			}

			cert, key, err := tlsFilesFromEnv()
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantCert, cert)
			assert.Equal(t, tc.wantKey, key)
		})
	}
}
