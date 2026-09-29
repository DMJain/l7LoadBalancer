package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestHandlerEndpoints locks the response contract for the benchmark payload
// endpoints and the /health probe (S5.T4-infra). Sizes are asserted exactly so
// a benchmark never silently measures the wrong payload profile.
func TestHandlerEndpoints(t *testing.T) {
	h := newHandler("backend1", 0, 0, discardLogger())

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

// TestHandlerNonGETRejected keeps the pre-existing 405 behavior for every path.
func TestHandlerNonGETRejected(t *testing.T) {
	h := newHandler("backend1", 0, 0, discardLogger())

	for _, path := range []string{"/", "/health", "/200b", "/10kb", "/1mb"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
			assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		})
	}
}

// TestHealthBypassesChaos proves the probe endpoint is never subject to the
// chaos knobs, so an injected FAIL_RATE cannot make the LB health-checker flap.
func TestHealthBypassesChaos(t *testing.T) {
	h := newHandler("backend1", 0, 1.0, discardLogger())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, http.StatusOK, rec.Code)

	// The same failRate must still hit a benchmark payload endpoint.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/200b", nil))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Len(t, rec.Body.Bytes(), 200)
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
