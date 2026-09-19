package balancer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/config"
)

// TestNewFromConfigMapsAlgorithm proves the config-string -> selector-type
// mapping is exhaustive over the algorithms this build accepts: each
// implemented identifier yields the concrete selector named in the frozen
// algorithm table (docs/design/sprint-1-contracts.md), not just any Selector.
func TestNewFromConfigMapsAlgorithm(t *testing.T) {
	tests := []struct {
		algorithm string
		want      any
	}{
		{algorithm: config.AlgorithmRoundRobin, want: &RoundRobin{}},
		{algorithm: config.AlgorithmLeastConn, want: &LeastConnections{}},
		{algorithm: config.AlgorithmConsistentHash, want: &ConsistentHashBoundedLoads{}},
		{algorithm: config.AlgorithmP2CEWMA, want: &PowerOfTwoChoicesEWMA{}},
	}

	for _, tt := range tests {
		t.Run(tt.algorithm, func(t *testing.T) {
			reg := newTestRegistry(t)
			sel, err := NewFromConfig(&config.Config{Algorithm: tt.algorithm}, reg)
			require.NoError(t, err)
			require.NotNil(t, sel)
			assert.IsType(t, tt.want, sel)
		})
	}
}

// TestNewFromConfigRejectsUnsupportedAlgorithm covers every value the factory
// must refuse: an arbitrary unknown string and the empty string. Empty is
// included deliberately — config.Validate normalizes it to round_robin, so an
// empty Algorithm reaching the factory means the caller skipped validation,
// and a silent default here would mask that.
func TestNewFromConfigRejectsUnsupportedAlgorithm(t *testing.T) {
	tests := []struct {
		name      string
		algorithm string
	}{
		{name: "unknown string", algorithm: "random"},
		{name: "empty string", algorithm: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := newTestRegistry(t)
			sel, err := NewFromConfig(&config.Config{Algorithm: tt.algorithm}, reg)
			require.Error(t, err)
			assert.Nil(t, sel)
		})
	}
}
