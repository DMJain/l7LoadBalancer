package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statusBody mirrors the control endpoint's status contract (S5.T16.2).
type statusBody struct {
	Rank        int     `json:"rank"`
	Clients     int     `json:"clients"`
	ZipfS       float64 `json:"zipf_s"`
	TotalRate   float64 `json:"total_rate"`
	Rate        float64 `json:"rate"`
	Target      string  `json:"target"`
	MaxInflight int     `json:"max_inflight"`
	Offered     int64   `json:"offered"`
	Sent        int64   `json:"sent"`
	Dropped     int64   `json:"dropped"`
	Completed   int64   `json:"completed"`
	Errors      int64   `json:"errors"`
}

// testLogger discards output; the generator's log line is asserted separately.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// baseConfig is a single-client, rank-1 configuration whose share is 1, so a
// test's expected own rate equals its configured total rate.
func baseConfig(target string, allowlist ...string) config {
	if len(allowlist) == 0 {
		allowlist = []string{target}
	}
	return config{
		rank:        1,
		clients:     1,
		zipfS:       1.0,
		totalRate:   200,
		target:      target,
		allowlist:   allowlist,
		maxInflight: 32,
	}
}

// controlServer starts the generator's control handler and returns its URL and
// a getter for the generator. No arrival loop runs unless the test starts one.
func controlServer(t *testing.T, cfg config) (*generator, string) {
	t.Helper()
	g, err := newGenerator(cfg, testLogger())
	require.NoError(t, err)
	srv := httptest.NewServer(g.controlHandler())
	t.Cleanup(srv.Close)
	return g, srv.URL
}

// runGenerator starts the arrival loop and cancels it when the test ends.
func runGenerator(t *testing.T, g *generator) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go g.run(ctx)
}

// getStatus reads the control endpoint's status.
func getStatus(t *testing.T, base string) statusBody {
	t.Helper()
	resp, err := http.Get(base)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var s statusBody
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&s))
	return s
}

// postControl sends a raw body to the control endpoint.
func postControl(t *testing.T, base, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(base, "application/json", strings.NewReader(body))
	require.NoError(t, err)
	return resp
}

// TestShareByRank proves each client's own rate is its Zipf-weighted share of
// the total, computed from RANK alone, for several ranks and exponents
// (S5.T16.2).
func TestShareByRank(t *testing.T) {
	const clients = 8
	exponents := []float64{0.0, 0.5, 1.0, 1.5}

	for _, s := range exponents {
		for rank := 1; rank <= clients; rank++ {
			t.Run("", func(t *testing.T) {
				cfg := config{
					rank: rank, clients: clients, zipfS: s,
					totalRate: 1000, target: "http://lb", allowlist: []string{"http://lb"},
					maxInflight: 4,
				}
				_, base := controlServer(t, cfg)

				want := 1000 * zipfShare(rank, clients, s)
				got := getStatus(t, base)
				assert.InDelta(t, want, got.Rate, 1e-6)
				assert.InDelta(t, 1000, got.TotalRate, 1e-9)
			})
		}
	}
}

// TestControlUpdatesSettings proves a runtime POST changes the total rate and
// the target, and that an omitted field keeps its value (S5.T16.2).
func TestControlUpdatesSettings(t *testing.T) {
	_, base := controlServer(t, baseConfig("http://lb-a", "http://lb-a", "http://lb-b"))

	start := getStatus(t, base)
	require.InDelta(t, 200, start.TotalRate, 1e-9)
	assert.Equal(t, "http://lb-a", start.Target)

	resp := postControl(t, base, `{"total_rate":500}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	after := getStatus(t, base)
	assert.InDelta(t, 500, after.TotalRate, 1e-9)
	assert.InDelta(t, 500, after.Rate, 1e-9)
	assert.Equal(t, "http://lb-a", after.Target, "omitted target keeps its value")

	resp = postControl(t, base, `{"target":"http://lb-b"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "http://lb-b", getStatus(t, base).Target)
}

// TestControlRejectsBadInput proves an unknown field, a negative rate and a
// target outside the allowlist are rejected with 400 and change nothing
// (S5.T16.2).
func TestControlRejectsBadInput(t *testing.T) {
	_, base := controlServer(t, baseConfig("http://lb-a", "http://lb-a", "http://lb-b"))

	tests := []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"bogus":1}`},
		{name: "negative rate", body: `{"total_rate":-1}`},
		{name: "disallowed target", body: `{"target":"http://evil"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := postControl(t, base, tc.body)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}

	got := getStatus(t, base)
	assert.InDelta(t, 200, got.TotalRate, 1e-9, "a rejected request leaves the rate untouched")
	assert.Equal(t, "http://lb-a", got.Target, "a rejected request leaves the target untouched")
}

// TestControlRejectsNonGetPost proves only GET and POST are served (S5.T16.2).
func TestControlRejectsNonGetPost(t *testing.T) {
	_, base := controlServer(t, baseConfig("http://lb-a"))

	req, err := http.NewRequest(http.MethodDelete, base, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// countServer is an httptest stand-in for an LB that records the arrival
// timestamps, paths and methods it receives.
type countServer struct {
	*httptest.Server
	mu     sync.Mutex
	times  []time.Time
	paths  map[string]int
	method map[string]int
	posts  int
}

func newCountServer(t *testing.T) *countServer {
	t.Helper()
	c := &countServer{paths: map[string]int{}, method: map[string]int{}}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.times = append(c.times, time.Now())
		c.paths[r.URL.Path]++
		c.method[r.Method]++
		if r.Method == http.MethodPost {
			c.posts++
			_, _ = io.Copy(io.Discard, r.Body)
		}
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *countServer) snapshot() (paths map[string]int, methods map[string]int, times []time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	paths = map[string]int{}
	for k, v := range c.paths {
		paths[k] = v
	}
	methods = map[string]int{}
	for k, v := range c.method {
		methods[k] = v
	}
	times = append(times, c.times...)
	return paths, methods, times
}

// TestReceivedRateWithinTolerance proves the stand-in receives roughly the
// client's configured rate over a fixed window (S5.T16.2).
func TestReceivedRateWithinTolerance(t *testing.T) {
	c := newCountServer(t)
	cfg := baseConfig(c.URL)
	cfg.totalRate = 200
	g, base := controlServer(t, cfg)
	runGenerator(t, g)

	const window = 2 * time.Second
	start := time.Now()
	require.Eventually(t, func() bool {
		_, _, times := c.snapshot()
		return len(times) >= 1
	}, 2*time.Second, 10*time.Millisecond, "generator never sent a request")
	time.Sleep(window)
	elapsed := time.Since(start)

	_, _, times := c.snapshot()
	observed := float64(len(times)) / elapsed.Seconds()
	assert.InDelta(t, 200, observed, 100, "observed %.0f req/s", observed)
	// The status must agree that it sent them, too.
	require.Eventually(t, func() bool {
		return getStatus(t, base).Sent > 0
	}, time.Second, 10*time.Millisecond)
}

// TestArrivalsAreNotAFixedInterval proves arrivals are spread, not a metronome
// (S5.T16.2).
func TestArrivalsAreNotAFixedInterval(t *testing.T) {
	c := newCountServer(t)
	cfg := baseConfig(c.URL)
	cfg.totalRate = 300
	g, _ := controlServer(t, cfg)
	runGenerator(t, g)

	require.Eventually(t, func() bool {
		_, _, times := c.snapshot()
		return len(times) >= 50
	}, 3*time.Second, 10*time.Millisecond, "not enough arrivals for a dispersion check")

	_, _, times := c.snapshot()
	var deltas []float64
	for i := 1; i < len(times); i++ {
		deltas = append(deltas, times[i].Sub(times[i-1]).Seconds())
	}
	mean := 0.0
	for _, d := range deltas {
		mean += d
	}
	mean /= float64(len(deltas))
	var variance float64
	for _, d := range deltas {
		variance += (d - mean) * (d - mean)
	}
	variance /= float64(len(deltas))
	cv := math.Sqrt(variance) / mean
	assert.Greater(t, cv, 0.2, "arrivals look like a fixed interval (CV %.2f)", cv)
}

// TestSizeMixAppears proves the response-size and request-body mixes reach the
// stand-in (S5.T16.2).
func TestSizeMixAppears(t *testing.T) {
	c := newCountServer(t)
	cfg := baseConfig(c.URL)
	cfg.totalRate = 300
	g, _ := controlServer(t, cfg)
	runGenerator(t, g)

	require.Eventually(t, func() bool {
		paths, _, _ := c.snapshot()
		return paths["/200b"] > 0 && paths["/10kb"] > 0 && paths["/1mb"] > 0
	}, 5*time.Second, 20*time.Millisecond, "the full response-size mix did not appear")

	paths, methods, _ := c.snapshot()
	for _, p := range []string{"/200b", "/10kb", "/1mb"} {
		assert.Positive(t, paths[p], "response path %s never requested", p)
	}
	assert.Positive(t, methods[http.MethodPost], "no request carried a body")
	assert.Positive(t, methods[http.MethodGet], "no bodyless request appeared")
}

// TestRuntimeTargetChange proves a target change takes effect for subsequent
// arrivals without a restart (S5.T16.2).
func TestRuntimeTargetChange(t *testing.T) {
	a := newCountServer(t)
	b := newCountServer(t)
	cfg := baseConfig(a.URL, a.URL, b.URL)
	cfg.totalRate = 200
	g, base := controlServer(t, cfg)
	runGenerator(t, g)

	require.Eventually(t, func() bool {
		_, _, times := a.snapshot()
		return len(times) > 0
	}, 2*time.Second, 10*time.Millisecond, "no traffic reached the first target")
	_, _, bTimes := b.snapshot()
	require.Zero(t, len(bTimes), "second target saw traffic before the switch")

	resp := postControl(t, base, `{"target":"`+b.URL+`"}`)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Eventually(t, func() bool {
		_, _, times := b.snapshot()
		return len(times) > 0
	}, 2*time.Second, 10*time.Millisecond, "no traffic reached the new target after the switch")
}

// TestInflightBoundAndDropped proves the in-flight bound holds against a slow
// stand-in and that arrivals it cannot start are counted as dropped
// (S5.T16.2; ADR-0021/ADR-0022).
func TestInflightBoundAndDropped(t *testing.T) {
	release := make(chan struct{})
	var current, maxSeen int64
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&current, 1)
		for {
			if old := atomic.LoadInt64(&maxSeen); n <= old || atomic.CompareAndSwapInt64(&maxSeen, old, n) {
				break
			}
		}
		<-release
		atomic.AddInt64(&current, -1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() { close(release); slow.Close() })

	cfg := baseConfig(slow.URL)
	cfg.totalRate = 1000
	cfg.maxInflight = 3
	g, base := controlServer(t, cfg)
	runGenerator(t, g)

	require.Eventually(t, func() bool {
		return getStatus(t, base).Dropped > 0
	}, 3*time.Second, 20*time.Millisecond, "no arrivals were counted as dropped")

	assert.LessOrEqual(t, atomic.LoadInt64(&maxSeen), int64(3), "in-flight requests exceeded the bound")
	st := getStatus(t, base)
	assert.Positive(t, st.Sent, "nothing was ever sent")
	assert.Equal(t, st.Offered, st.Sent+st.Dropped, "offered must equal sent + dropped")
}

// TestStartupLogLine proves the startup line records rank, share, target and
// bounds (S5.T16.2).
func TestStartupLogLine(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	cfg := baseConfig("http://lb", "http://lb")
	cfg.addr = ":9090"
	g, err := newGenerator(cfg, logger)
	require.NoError(t, err)

	g.logStartup()

	var line map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line))
	assert.Equal(t, float64(1), line["rank"])
	assert.Equal(t, "http://lb", line["target"])
	assert.Equal(t, float64(32), line["max_inflight"])
	assert.Equal(t, ":9090", line["addr"])
	assert.Contains(t, line, "share")
	assert.Contains(t, line, "rate")
}

// TestConfigFromEnvStrict proves startup env parsing follows the dummy
// backend's strictness and names the variable on failure (S5.T16.2).
func TestConfigFromEnvStrict(t *testing.T) {
	base := map[string]string{
		"RANK":             "1",
		"CLIENTS":          "8",
		"ZIPF_S":           "1.0",
		"TOTAL_RATE":       "200",
		"TARGET_LB":        "http://lb-a",
		"TARGET_ALLOWLIST": "http://lb-a,http://lb-b",
		"MAX_INFLIGHT":     "64",
	}

	tests := []struct {
		name    string
		mutate  map[string]string
		wantErr string
		want    config
	}{
		{name: "valid", want: config{
			rank: 1, clients: 8, zipfS: 1.0, totalRate: 200,
			target: "http://lb-a", allowlist: []string{"http://lb-a", "http://lb-b"},
			maxInflight: 64,
		}},
		{name: "missing rank", mutate: map[string]string{"RANK": ""}, wantErr: "RANK"},
		{name: "rank zero", mutate: map[string]string{"RANK": "0"}, wantErr: "RANK"},
		{name: "rank above clients", mutate: map[string]string{"RANK": "9"}, wantErr: "RANK"},
		{name: "bad zipf", mutate: map[string]string{"ZIPF_S": "abc"}, wantErr: "ZIPF_S"},
		{name: "negative total rate", mutate: map[string]string{"TOTAL_RATE": "-1"}, wantErr: "TOTAL_RATE"},
		{name: "bad max inflight", mutate: map[string]string{"MAX_INFLIGHT": "0"}, wantErr: "MAX_INFLIGHT"},
		{name: "missing allowlist", mutate: map[string]string{"TARGET_ALLOWLIST": ""}, wantErr: "TARGET_ALLOWLIST"},
		{name: "missing target", mutate: map[string]string{"TARGET_LB": ""}, wantErr: "TARGET_LB"},
		{name: "target not allowlisted", mutate: map[string]string{"TARGET_LB": "http://evil"}, wantErr: "TARGET_LB"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			for k, v := range base {
				env[k] = v
			}
			for k, v := range tc.mutate {
				env[k] = v
			}
			for k, v := range env {
				t.Setenv(k, v)
			}

			got, err := configFromEnv()
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
