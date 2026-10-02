// Concurrency: the service under test fans out to the stub peers on one
// goroutine per peer, so the stubs' recorded state is shared with those
// goroutines and is guarded by each stub's mutex; snapshots take the same mutex.
package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// genStub stands in for one traffic generator's control endpoint (S5.T16.2):
// GET returns a status snapshot, POST records the target/total_rate it was sent.
// It records every call so a test can prove an invalid request made none.
type genStub struct {
	srv *httptest.Server

	mu        sync.Mutex
	target    string
	rate      float64
	gets      int
	posts     int
	failPosts bool
	offered   int64
	sent      int64
	dropped   int64
}

func newGenStub(t *testing.T) *genStub {
	t.Helper()
	g := &genStub{target: "http://lb-roundrobin:8080", rate: 400, offered: 11, sent: 10, dropped: 1}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			g.gets++
			if g.failPosts {
				http.Error(w, "stub down", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"rank": 1, "clients": 8, "zipf_s": 1.0,
				"total_rate": g.rate, "rate": g.rate / 8, "target": g.target,
				"offered": g.offered, "sent": g.sent, "dropped": g.dropped,
			})
		case http.MethodPost:
			g.posts++
			if g.failPosts {
				http.Error(w, "stub down", http.StatusInternalServerError)
				return
			}
			var in struct {
				TotalRate *float64 `json:"total_rate"`
				Target    *string  `json:"target"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			if in.TotalRate != nil {
				g.rate = *in.TotalRate
			}
			if in.Target != nil {
				g.target = *in.Target
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *genStub) snapshot() (target string, rate float64, gets, posts int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.target, g.rate, g.gets, g.posts
}

// backendStub stands in for one dummy backend's admin listener (S5.T16.1):
// POST only, omitted fields keep their value, the response is the full profile.
// An empty `{}` body is therefore a read, exactly as the real listener treats it.
type backendStub struct {
	srv *httptest.Server

	mu       sync.Mutex
	sleepMS  int
	jitterMS int
	failRate float64
	posts    int
}

func newBackendStub(t *testing.T) *backendStub {
	t.Helper()
	b := &backendStub{}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		b.posts++
		var in struct {
			SleepMS  *int     `json:"sleep_ms"`
			JitterMS *int     `json:"jitter_ms"`
			FailRate *float64 `json:"fail_rate"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil && err != io.EOF {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if in.SleepMS != nil {
			b.sleepMS = *in.SleepMS
		}
		if in.JitterMS != nil {
			b.jitterMS = *in.JitterMS
		}
		if in.FailRate != nil {
			b.failRate = *in.FailRate
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sleep_ms": b.sleepMS, "jitter_ms": b.jitterMS, "fail_rate": b.failRate,
		})
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *backendStub) snapshot() (sleepMS, jitterMS int, failRate float64, posts int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sleepMS, b.jitterMS, b.failRate, b.posts
}

// rig is the eight-generator / four-backend stand-in set the control service is
// pointed at, plus the LBs it validates against.
type rig struct {
	gens     []*genStub
	backends []*backendStub
	handler  http.Handler
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{}
	cfg := config{grafanaURL: "http://127.0.0.1:3000"}
	for i := 1; i <= 8; i++ {
		g := newGenStub(t)
		r.gens = append(r.gens, g)
		cfg.generators = append(cfg.generators, peer{name: "gen" + strconv.Itoa(i), url: g.srv.URL})
	}
	for i := 1; i <= 4; i++ {
		b := newBackendStub(t)
		r.backends = append(r.backends, b)
		cfg.backends = append(cfg.backends, peer{name: "backend" + strconv.Itoa(i), url: b.srv.URL})
	}
	cfg.lbs = []peer{
		{name: "lb-roundrobin", url: "http://lb-roundrobin:8080"},
		{name: "lb-leastconn", url: "http://lb-leastconn:8080"},
		{name: "lb-consistent-hash", url: "http://lb-consistent-hash:8080"},
		{name: "lb-p2c-ewma", url: "http://lb-p2c-ewma:8080"},
	}
	s, err := newServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	r.handler = s.handler()
	return r
}

func (r *rig) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)
	return rec
}

// TestSwitchLBReachesAllEightGenerators proves the LB switch is a total fan-out
// carrying the LB's target URL, and touches no admin listener (S5.T16.4.1).
func TestSwitchLBReachesAllEightGenerators(t *testing.T) {
	r := newRig(t)
	rec := r.do(t, http.MethodPost, "/api/lb", `{"lb":"lb-p2c-ewma"}`)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp fanOutResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.OK)
	assert.Empty(t, resp.Failed)

	for i, g := range r.gens {
		target, _, _, posts := g.snapshot()
		assert.Equal(t, "http://lb-p2c-ewma:8080", target, "generator %d did not receive the target", i+1)
		assert.Equal(t, 1, posts, "generator %d should have received exactly one POST", i+1)
	}
	for i, b := range r.backends {
		_, _, _, posts := b.snapshot()
		assert.Zero(t, posts, "backend %d should not have been touched by an LB switch", i+1)
	}
}

// TestSetRateReachesAllEightGenerators proves the rate fan-out carries the same
// total to every generator (S5.T16.4.1).
func TestSetRateReachesAllEightGenerators(t *testing.T) {
	r := newRig(t)
	rec := r.do(t, http.MethodPost, "/api/rate", `{"total_rate":250}`)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp fanOutResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.OK)

	for i, g := range r.gens {
		_, rate, _, posts := g.snapshot()
		assert.Equal(t, 250.0, rate, "generator %d did not receive the rate", i+1)
		assert.Equal(t, 1, posts)
	}
}

// TestSetBackendProfileReachesOnlyThatBackend proves the profile action is a
// single forward to the named admin listener and no fan-out (S5.T16.4.1).
func TestSetBackendProfileReachesOnlyThatBackend(t *testing.T) {
	r := newRig(t)
	rec := r.do(t, http.MethodPost, "/api/backend", `{"backend":"backend3","sleep_ms":200,"jitter_ms":10,"fail_rate":0.5}`)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp backendResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.OK)
	assert.Equal(t, 200, resp.Profile.SleepMS)

	for i, b := range r.backends {
		sleepMS, jitterMS, failRate, posts := b.snapshot()
		if i == 2 {
			assert.Equal(t, 200, sleepMS)
			assert.Equal(t, 10, jitterMS)
			assert.Equal(t, 0.5, failRate)
			assert.Equal(t, 1, posts)
		} else {
			assert.Zero(t, posts, "backend %d should not have been touched", i+1)
		}
	}
	for i, g := range r.gens {
		_, _, _, posts := g.snapshot()
		assert.Zero(t, posts, "generator %d should not have been touched", i+1)
	}
}

// TestPartialFanOutNamesEveryFailedGenerator proves a down generator does not
// stop the other seven, and that it is named in the response (S5.T16.4.1).
func TestPartialFanOutNamesEveryFailedGenerator(t *testing.T) {
	r := newRig(t)
	r.gens[4].mu.Lock()
	r.gens[4].failPosts = true
	r.gens[4].mu.Unlock()

	rec := r.do(t, http.MethodPost, "/api/lb", `{"lb":"lb-leastconn"}`)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp fanOutResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.OK)
	assert.Equal(t, []string{"gen5"}, resp.Failed)

	for i, g := range r.gens {
		target, _, _, posts := g.snapshot()
		if i == 4 {
			continue
		}
		assert.Equal(t, "http://lb-leastconn:8080", target, "generator %d was not reached", i+1)
		assert.Equal(t, 1, posts)
	}
}

// TestInvalidInputsMakeNoOutboundCall proves validation happens before any peer
// is contacted (S5.T16.4.1).
func TestInvalidInputsMakeNoOutboundCall(t *testing.T) {
	cases := []struct {
		name string
		path string
		body string
	}{
		{"unknown lb", "/api/lb", `{"lb":"lb-nope"}`},
		{"missing lb", "/api/lb", `{}`},
		{"lb unknown field", "/api/lb", `{"lb":"lb-p2c-ewma","extra":1}`},
		{"negative rate", "/api/rate", `{"total_rate":-1}`},
		{"missing rate", "/api/rate", `{}`},
		{"rate unknown field", "/api/rate", `{"total_rate":1,"extra":1}`},
		{"unknown backend", "/api/backend", `{"backend":"backend9"}`},
		{"missing backend", "/api/backend", `{}`},
		{"backend unknown field", "/api/backend", `{"backend":"backend1","extra":1}`},
		{"negative sleep", "/api/backend", `{"backend":"backend1","sleep_ms":-1}`},
		{"negative jitter", "/api/backend", `{"backend":"backend1","jitter_ms":-1}`},
		{"fail_rate above one", "/api/backend", `{"backend":"backend1","fail_rate":1.1}`},
		{"fail_rate below zero", "/api/backend", `{"backend":"backend1","fail_rate":-0.1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			rec := r.do(t, http.MethodPost, tc.path, tc.body)
			assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
			for i, g := range r.gens {
				_, _, gets, posts := g.snapshot()
				assert.Zero(t, gets+posts, "generator %d was called on an invalid input", i+1)
			}
			for i, b := range r.backends {
				_, _, _, posts := b.snapshot()
				assert.Zero(t, posts, "backend %d was called on an invalid input", i+1)
			}
		})
	}
}

// TestStateReadsEveryPeerAndDerivesAgreement proves GET /api/state reads the
// eight generators and four admin listeners live and reports the active LB only
// when the generators agree (S5.T16.4.1).
func TestStateReadsEveryPeerAndDerivesAgreement(t *testing.T) {
	r := newRig(t)

	rec := r.do(t, http.MethodGet, "/api/state", "")
	require.Equal(t, http.StatusOK, rec.Code)

	var st stateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st))
	require.Len(t, st.Generators, 8)
	require.Len(t, st.Backends, 4)
	assert.Equal(t, "lb-roundrobin", st.ActiveLB)
	require.NotNil(t, st.TotalRate)
	assert.Equal(t, 400.0, *st.TotalRate)
	assert.Equal(t, int64(11), st.Generators[0].Offered)
	assert.Equal(t, int64(10), st.Generators[0].Sent)
	assert.Equal(t, int64(1), st.Generators[0].Dropped)

	for i, g := range r.gens {
		_, _, gets, _ := g.snapshot()
		assert.Equal(t, 1, gets, "generator %d was not read", i+1)
	}
	for i, b := range r.backends {
		_, _, _, posts := b.snapshot()
		assert.Equal(t, 1, posts, "backend %d was not read (a POST-{} read)", i+1)
	}
}

// TestStateReportsMixedWhenGeneratorsDisagree proves the derived active LB is
// withheld, not guessed, when the generators' targets differ (S5.T16.4.1).
func TestStateReportsMixedWhenGeneratorsDisagree(t *testing.T) {
	r := newRig(t)
	r.gens[2].mu.Lock()
	r.gens[2].target = "http://lb-p2c-ewma:8080"
	r.gens[2].mu.Unlock()

	rec := r.do(t, http.MethodGet, "/api/state", "")
	require.Equal(t, http.StatusOK, rec.Code)

	var st stateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st))
	assert.Empty(t, st.ActiveLB, "active LB must be withheld when generators disagree")
}

// TestServesEmbeddedPage proves the page is served with the embedded markers the
// browser needs: the dashboard iframe query and the API endpoints (S5.T16.4.1).
func TestServesEmbeddedPage(t *testing.T) {
	r := newRig(t)
	rec := r.do(t, http.MethodGet, "/", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	body := rec.Body.String()
	assert.Contains(t, body, "var-window=15s")
	assert.Contains(t, body, "refresh=5s")
	assert.Contains(t, body, "/api/state")
}
