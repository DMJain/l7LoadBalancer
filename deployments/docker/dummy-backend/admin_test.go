package main

import (
	"encoding/json"
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

// adminBody mirrors the admin endpoint's response contract: the full profile
// now in effect (S5.T16.1).
type adminBody struct {
	SleepMS  int     `json:"sleep_ms"`
	JitterMS int     `json:"jitter_ms"`
	FailRate float64 `json:"fail_rate"`
}

// newTestBackend builds a backend and returns its request and admin handlers,
// which share one profile pointer so a profile set through admin is observed by
// the request handler (S5.T16.1).
func newTestBackend(initial profile) (*backend, http.Handler, http.Handler) {
	b := newBackend("backend1", initial, true, discardLogger())
	return b, b.handler(), b.adminHandler()
}

// adminRequest drives a request through the admin handler without a *testing.T,
// so it is safe to call from a goroutine in the concurrency test.
func driveAdmin(admin http.Handler, method, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	admin.ServeHTTP(rec, req)
	return rec
}

// TestAdminEnabledEnvParsing locks ADMIN_ENABLED to the envBool strictness: unset
// or empty means off, only true/false are accepted, and anything else fails
// naming the variable (S5.T16.1).
func TestAdminEnabledEnvParsing(t *testing.T) {
	tests := []struct {
		name    string
		unset   bool
		raw     string
		want    bool
		wantErr bool
	}{
		{name: "unset defaults off", unset: true, want: false},
		{name: "empty defaults off", raw: "", want: false},
		{name: "true enables", raw: "true", want: true},
		{name: "false disables", raw: "false", want: false},
		{name: "arbitrary word is an error", raw: "yes", wantErr: true},
		{name: "numeric is an error", raw: "1", wantErr: true},
		{name: "uppercase is an error", raw: "TRUE", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.unset {
				t.Setenv("ADMIN_ENABLED", "sentinel")
				require.NoError(t, os.Unsetenv("ADMIN_ENABLED"))
			} else {
				t.Setenv("ADMIN_ENABLED", tc.raw)
			}

			got, err := envBool("ADMIN_ENABLED", false)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "ADMIN_ENABLED")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestAdminRejectsNonPost proves only POST is accepted, and the 405 advertises
// POST (S5.T16.1).
func TestAdminRejectsNonPost(t *testing.T) {
	_, _, admin := newTestBackend(profile{})

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			rec := driveAdmin(admin, method, "")
			assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
			assert.Equal(t, http.MethodPost, rec.Header().Get("Allow"))
		})
	}
}

// TestAdminRejectsUnknownField proves a typo fails loudly instead of being
// silently ignored (S5.T16.1).
func TestAdminRejectsUnknownField(t *testing.T) {
	_, _, admin := newTestBackend(profile{})

	rec := driveAdmin(admin, http.MethodPost, `{"sleep_ms":1,"unknown":2}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestAdminRejectsMalformedJSON proves an undecodable body is a 400 (S5.T16.1).
func TestAdminRejectsMalformedJSON(t *testing.T) {
	_, _, admin := newTestBackend(profile{})

	rec := driveAdmin(admin, http.MethodPost, `{`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestAdminRejectsOutOfRange proves negative milliseconds and a failure rate
// outside [0, 1] are rejected 400 naming the field (S5.T16.1).
func TestAdminRejectsOutOfRange(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		field string
	}{
		{name: "negative sleep", body: `{"sleep_ms":-1}`, field: "sleep_ms"},
		{name: "negative jitter", body: `{"jitter_ms":-5}`, field: "jitter_ms"},
		{name: "fail rate below zero", body: `{"fail_rate":-0.1}`, field: "fail_rate"},
		{name: "fail rate above one", body: `{"fail_rate":1.5}`, field: "fail_rate"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, admin := newTestBackend(profile{})

			rec := driveAdmin(admin, http.MethodPost, tc.body)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.field)
		})
	}
}

// TestAdminOmittedFieldKeepsValue proves an omitted field keeps its current
// value, so the page can set one knob at a time (S5.T16.1).
func TestAdminOmittedFieldKeepsValue(t *testing.T) {
	_, _, admin := newTestBackend(profile{sleepMS: 7, jitterMS: 3, failRate: 0.2})

	rec := driveAdmin(admin, http.MethodPost, `{"fail_rate":0.9}`)
	require.Equal(t, http.StatusOK, rec.Code)

	var got adminBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, 7, got.SleepMS)
	assert.Equal(t, 3, got.JitterMS)
	assert.InDelta(t, 0.9, got.FailRate, 1e-9)

	// An empty body changes nothing and echoes the same profile.
	rec = driveAdmin(admin, http.MethodPost, `{}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, 7, got.SleepMS)
	assert.Equal(t, 3, got.JitterMS)
	assert.InDelta(t, 0.9, got.FailRate, 1e-9)
}

// TestAdminResponseIsFullProfile proves the response is the full profile now in
// effect, which the control page displays (S5.T16.1).
func TestAdminResponseIsFullProfile(t *testing.T) {
	_, _, admin := newTestBackend(profile{})

	rec := driveAdmin(admin, http.MethodPost, `{"sleep_ms":5,"jitter_ms":2,"fail_rate":0.5}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var got adminBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, 5, got.SleepMS)
	assert.Equal(t, 2, got.JitterMS)
	assert.InDelta(t, 0.5, got.FailRate, 1e-9)
}

// TestAdminSetChangesRequestBehaviour proves a runtime profile reaches the very
// next proxied request: failure on status, sleep on latency (S5.T16.1).
func TestAdminSetChangesRequestBehaviour(t *testing.T) {
	_, req, admin := newTestBackend(profile{})

	require.Equal(t, http.StatusOK, driveAdmin(admin, http.MethodPost, `{"fail_rate":1}`).Code)
	rec := httptest.NewRecorder()
	req.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/200b", nil))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	require.Equal(t, http.StatusOK, driveAdmin(admin, http.MethodPost, `{"fail_rate":0}`).Code)
	rec = httptest.NewRecorder()
	req.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/200b", nil))
	assert.Equal(t, http.StatusOK, rec.Code)

	require.Equal(t, http.StatusOK, driveAdmin(admin, http.MethodPost, `{"sleep_ms":120}`).Code)
	rec = httptest.NewRecorder()
	start := time.Now()
	req.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/200b", nil))
	assert.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond)
}

// TestJitterBounds proves the effective sleep stays within the injected band and
// is never negative (S5.T16.1).
func TestJitterBounds(t *testing.T) {
	_, req, admin := newTestBackend(profile{})
	require.Equal(t, http.StatusOK, driveAdmin(admin, http.MethodPost, `{"sleep_ms":20,"jitter_ms":20}`).Code)

	var maxD time.Duration
	for i := 0; i < 30; i++ {
		rec := httptest.NewRecorder()
		start := time.Now()
		req.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/200b", nil))
		if d := time.Since(start); d > maxD {
			maxD = d
		}
	}

	assert.GreaterOrEqual(t, maxD, 10*time.Millisecond, "jitter should push some requests above the base")
	assert.LessOrEqual(t, maxD, 200*time.Millisecond, "jitter must not exceed sleep_ms + jitter_ms by much")
}

// TestJitterClampedAtZero proves a jitter larger than the base sleep never
// yields a negative delay (S5.T16.1).
func TestJitterClampedAtZero(t *testing.T) {
	_, req, admin := newTestBackend(profile{})
	require.Equal(t, http.StatusOK, driveAdmin(admin, http.MethodPost, `{"sleep_ms":0,"jitter_ms":50}`).Code)

	for i := 0; i < 20; i++ {
		rec := httptest.NewRecorder()
		start := time.Now()
		req.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/200b", nil))
		d := time.Since(start)
		assert.GreaterOrEqual(t, d, time.Duration(0))
		assert.Less(t, d, 500*time.Millisecond)
	}
}

// TestControlPathsBypassRuntimeChaos proves /health and /stats stay chaos-free
// even when a profile is set at runtime, so a flaky backend is not ejected by
// its own probe (S5.T16.1).
func TestControlPathsBypassRuntimeChaos(t *testing.T) {
	_, req, admin := newTestBackend(profile{})
	require.Equal(t, http.StatusOK, driveAdmin(admin, http.MethodPost, `{"sleep_ms":5000,"fail_rate":1}`).Code)

	for _, path := range []string{"/health", "/stats"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			start := time.Now()
			req.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Less(t, time.Since(start), time.Second)
		})
	}
}

// TestPayloadPathsAcceptPostBody proves the payload paths accept a POST whose
// body is read and discarded, returning the same body as GET (S5.T16.1).
func TestPayloadPathsAcceptPostBody(t *testing.T) {
	_, req, _ := newTestBackend(profile{})

	tests := []struct {
		path    string
		wantLen int
	}{
		{path: "/200b", wantLen: 200},
		{path: "/10kb", wantLen: 10 * 1024},
		{path: "/1mb", wantLen: 1 << 20},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader("upload body"))
			req.ServeHTTP(rec, r)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Len(t, rec.Body.Bytes(), tc.wantLen)
		})
	}
}

// TestConcurrentSetWhileServing drives requests and admin writes at once; the
// -race gate is the real assertion (S5.T16.1).
func TestConcurrentSetWhileServing(t *testing.T) {
	_, req, admin := newTestBackend(profile{})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/200b", nil))
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			driveAdmin(admin, http.MethodPost, `{"sleep_ms":1,"jitter_ms":2,"fail_rate":0.5}`)
		}()
	}
	wg.Wait()
}
