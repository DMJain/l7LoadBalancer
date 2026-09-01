package config

// Algorithm identifiers. These are the exact string values accepted in the
// YAML `algorithm` field and matched against in balancer.NewFromConfig.
// See docs/design/sprint-1-contracts.md "Algorithm identifier table".
const (
	AlgorithmRoundRobin     = "round_robin"
	AlgorithmLeastConn      = "least_conn"
	AlgorithmConsistentHash = "consistent_hash"
	AlgorithmP2CEWMA        = "p2c_ewma"
)

// Config is the top-level load balancer configuration, loaded from YAML.
type Config struct {
	Listen    string          `yaml:"listen"`
	Algorithm string          `yaml:"algorithm"`
	Backends  []BackendConfig `yaml:"backends"`
}

// BackendConfig describes one backend entry in the YAML config.
type BackendConfig struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// Load reads and strictly unmarshals the YAML config file at path into a
// Config, then returns it unvalidated (callers must call Validate).
//
// Error handling convention (frozen S1.T0.5): errors are wrapped, not
// sentinel — fmt.Errorf("config: %w", err). No exported sentinel errors for
// load/validate failures; callers inspect the message or use errors.Is on
// the wrapped stdlib error (e.g. os.ErrNotExist) if they need to branch.
//
// Implemented in S1.T2.
func Load(path string) (*Config, error) {
	panic("not implemented: S1.T2")
}

// Validate checks the config for correctness: non-empty Listen, at least
// one backend, each backend URL parseable with a host, unique backend
// names, and a recognized Algorithm value.
//
// Implemented in S1.T2.
func (c *Config) Validate() error {
	panic("not implemented: S1.T2")
}
