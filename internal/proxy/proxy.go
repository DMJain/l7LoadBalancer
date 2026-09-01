package proxy

import (
	"net/http"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/balancer"
)

// Proxy wraps net/http/httputil.ReverseProxy, delegating backend selection
// to a balancer.Selector on each request and tracking ActiveConns around
// the round trip. Implemented in S1.T6.
type Proxy struct {
	reg *backend.Registry
	sel balancer.Selector
}

// New constructs a Proxy over reg using sel for backend selection.
// Implemented in S1.T6.
func New(reg *backend.Registry, sel balancer.Selector) *Proxy {
	panic("not implemented: S1.T6")
}

// ServeHTTP implements http.Handler. No healthy backend responds 503, no
// panic, no hang. Implemented in S1.T6.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	panic("not implemented: S1.T6")
}

var _ http.Handler = (*Proxy)(nil)
