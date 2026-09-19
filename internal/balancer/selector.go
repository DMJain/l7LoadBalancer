package balancer

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/config"
)

// Selector chooses a backend for a given request. Implementations live in
// this package, not in the consumer (proxy), because Sprint 1 and Sprint 2
// together add four implementations that all satisfy this one contract,
// and proxy only ever needs the interface type — no methods of its own to
// hide behind it. See docs/design/sprint-1-contracts.md "Interface
// placement decision".
type Selector interface {
	Select(ctx context.Context, r *http.Request) (*backend.Backend, error)
}

// ErrNoHealthyBackends is returned by Select when no backend is currently
// eligible for selection. It is the one exported sentinel in this
// project's error handling convention — callers (proxy) branch on it to
// return 503 instead of 502. All other errors are wrapped, not sentinel.
var ErrNoHealthyBackends = errors.New("balancer: no healthy backends")

// NewFromConfig builds the Selector named by cfg.Algorithm. This is the
// selector factory; it lives here (not in main.go) so main stays a thin
// wiring layer and the balancer package owns the config-string-to-type
// mapping it also documents (docs/design/sprint-1-contracts.md "Algorithm
// identifier table").
//
// It accepts exactly the identifiers config.Validate accepts
// (config's implementedAlgorithms set). An unrecognized value — including
// the empty string — returns a wrapped error rather than falling back to a
// default: config.Validate already normalizes an omitted algorithm to
// round_robin, so an unrecognized value here means validation was skipped,
// and defaulting would mask that. Each sprint adds a case per new selector.
func NewFromConfig(cfg *config.Config, reg *backend.Registry) (Selector, error) {
	switch cfg.Algorithm {
	case config.AlgorithmRoundRobin:
		return NewRoundRobin(reg), nil
	case config.AlgorithmLeastConn:
		return NewLeastConnections(reg), nil
	case config.AlgorithmConsistentHash:
		return NewConsistentHashBoundedLoads(reg), nil
	case config.AlgorithmP2CEWMA:
		return NewPowerOfTwoChoicesEWMA(reg), nil
	default:
		return nil, fmt.Errorf("balancer: unsupported algorithm %q", cfg.Algorithm)
	}
}
