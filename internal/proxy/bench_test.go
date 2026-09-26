package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/balancer"
	"github.com/DMJain/l7LoadBalancer/internal/config"
)

// BenchmarkProxyServeHTTP measures the full request path — selection, the
// retired-context join (context.WithCancelCause + context.AfterFunc), the real
// HTTP round trip, the observer fan-out, and the whole-request metrics/log
// hook — against a local httptest backend. Its purpose is the S4.T4.0 claim
// that the drain join costs one registration per request and no goroutine;
// compare the ns/op and allocs/op across a build with the join removed.
func BenchmarkProxyServeHTTP(b *testing.B) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	reg, err := backend.NewRegistry([]config.BackendConfig{{Name: "backend-a", URL: srv.URL}})
	if err != nil {
		b.Fatal(err)
	}
	p := New(reg, balancer.NewRoundRobin(reg))
	p.RegisterObserver(NewLatencyObserver())

	req := httptest.NewRequest(http.MethodGet, "/", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("unexpected status %d", rec.Code)
		}
	}
}
