package health

import (
	"net/http"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// NewHandler returns the orchestrator probe handler — a mux serving /livez,
// /readyz, and /startupz — over checker, reg, and collector. ADR-0014 (S3.T12).
func NewHandler(checker *Checker, reg *backend.Registry, configLoaded bool, collector *metrics.Collector) http.Handler {
	panic("not implemented: S3.T12")
}
