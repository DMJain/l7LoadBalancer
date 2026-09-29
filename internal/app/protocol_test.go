package app

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"

	"github.com/DMJain/l7LoadBalancer/internal/config"
)

// requestProtocolLabel returns the protocol label value recorded on
// lb_requests_total for backendName, or "" if no such series exists yet.
func requestProtocolLabel(t *testing.T, reg *prometheus.Registry, backendName string) string {
	t.Helper()
	mfs, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != "lb_requests_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			var backend, protocol string
			for _, p := range m.GetLabel() {
				switch p.GetName() {
				case "backend":
					backend = p.GetValue()
				case "protocol":
					protocol = p.GetValue()
				}
			}
			if backend == backendName {
				return protocol
			}
		}
	}
	return ""
}

// startProtocolBackend starts a plain HTTP/1.1 backend the LB proxies to; the
// client listener mode is what the protocol label reflects, not the backend
// leg.
func startProtocolBackend(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestClientProtocolLabelMatchesListenerMode proves the client-facing protocol
// label is derived from the real wire protocol on each listener mode
// (S5.T3-main): "http/1.1" in plain mode, "h2" over TLS+ALPN, and "h2c" for a
// prior-knowledge cleartext HTTP/2 request.
func TestClientProtocolLabelMatchesListenerMode(t *testing.T) {
	silenceDefault(t)

	backend := startProtocolBackend(t)

	cases := []struct {
		name  string
		build func(t *testing.T) *config.Config
		get   func(addr string) (*http.Response, error)
		want  string
	}{
		{
			name: "plain http/1.1",
			build: func(t *testing.T) *config.Config {
				return testConfig([]config.BackendConfig{{Name: "backend-a", URL: backend.URL}})
			},
			get:  func(addr string) (*http.Response, error) { return http.Get("http://" + addr + "/") },
			want: "http/1.1",
		},
		{
			name: "tls h2",
			build: func(t *testing.T) *config.Config {
				return tlsTestConfig(t, backend.URL, nil)
			},
			get: func(addr string) (*http.Response, error) {
				client := &http.Client{Transport: &http.Transport{
					TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
					ForceAttemptHTTP2: true,
				}}
				return client.Get("https://" + addr + "/")
			},
			want: "h2",
		},
		{
			name: "h2c",
			build: func(t *testing.T) *config.Config {
				return h2cTestConfig(t, backend.URL, nil)
			},
			get: func(addr string) (*http.Response, error) {
				client := &http.Client{Transport: &http2.Transport{
					AllowHTTP: true,
					DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
						return (&net.Dialer{}).DialContext(ctx, network, addr)
					},
				}}
				return client.Get("http://" + addr + "/")
			},
			want: "h2c",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.build(t)
			application, addr := startTestApp(t, cfg)

			// Run starts its listeners asynchronously; poll until one answers.
			deadline := time.Now().Add(5 * time.Second)
			var resp *http.Response
			var err error
			for {
				resp, err = tc.get(addr)
				if err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("listener never came up: %v", err)
				}
				time.Sleep(20 * time.Millisecond)
			}
			require.Equal(t, http.StatusOK, resp.StatusCode)
			_, err = io.Copy(io.Discard, resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())

			require.Eventually(t, func() bool {
				return requestProtocolLabel(t, application.Collector().Registry(), "backend-a") == tc.want
			}, 5*time.Second, 10*time.Millisecond,
				"the request counter must reach the %q protocol label", tc.want)
		})
	}
}
