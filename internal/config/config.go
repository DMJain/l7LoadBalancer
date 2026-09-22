package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Algorithm identifiers. These are the canonical string vocabulary for the
// YAML `algorithm` field and the config-string -> selector-type mapping in
// balancer.NewFromConfig. The constants name every algorithm the project
// intends to support; the narrower set accepted by the current build is
// implementedAlgorithms. See ADR-0004 and
// docs/design/sprint-1-contracts.md "Algorithm identifier table".
const (
	AlgorithmRoundRobin     = "round_robin"
	AlgorithmLeastConn      = "least_conn"
	AlgorithmConsistentHash = "consistent_hash"
	AlgorithmP2CEWMA        = "p2c_ewma"
)

// implementedAlgorithms is the set of algorithms this build can actually
// select. Validation answers "will this work?", not "does this parse?".
// Adding a selector is a one-line addition here. See ADR-0004.
var implementedAlgorithms = map[string]struct{}{
	AlgorithmRoundRobin:     {},
	AlgorithmLeastConn:      {},
	AlgorithmConsistentHash: {},
	AlgorithmP2CEWMA:        {},
}

// backendNamePattern restricts backend names to the intersection of what is
// safe in every downstream context: Prometheus label values, structured log
// field values, grep targets, and future admin UIs. No escaping needed.
var backendNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Sprint 3 duration defaults, applied by Validate when the corresponding YAML
// field is omitted. Exported so tests, docs, and the packages that consume
// them (health, circuit) reference one value instead of restating it.
//
// ADR-0011 decision 10 is why these three are config while the
// consecutive-failure/-success thresholds, the passive-outlier window size and
// failure threshold, and the circuit's failure-to-open threshold stay Go
// constants: cadence and cooldown plausibly differ per deployment, algorithm-
// shaped tuning values do not.
const (
	// DefaultProbeInterval is how often each backend is probed when
	// health.probe_interval is omitted.
	DefaultProbeInterval = 5 * time.Second
	// DefaultProbeTimeout bounds a single probe when health.probe_timeout is
	// omitted. It is shorter than DefaultProbeInterval so the defaults cannot
	// overlap; Validate does not enforce the relationship, since an operator
	// may deliberately choose a timeout longer than the interval.
	DefaultProbeTimeout = 2 * time.Second
	// DefaultCircuitCooldown is how long a tripped circuit stays Open before a
	// read may promote it to Half-Open, when circuit.cooldown is omitted.
	DefaultCircuitCooldown = 30 * time.Second
	// DefaultMetricsListen is the address the Prometheus exposition endpoint
	// binds when metrics.listen is omitted. Always-on; no disable toggle.
	DefaultMetricsListen = ":9090"
	// DefaultHealthEndpointListen is the address the orchestrator probe
	// endpoint (livez/readyz/startupz) binds when health_endpoint.listen is
	// omitted. Always-on, mirroring DefaultMetricsListen. ADR-0014 (S3.T12).
	DefaultHealthEndpointListen = ":8081"
)

// Config is the top-level load balancer configuration, loaded from YAML.
//
// Sprint 3's health and circuit knobs are nested `health:`/`circuit:` sections
// rather than flat top-level keys, so each subsystem's settings stay grouped
// and the schema remains legible as it grows. See ADR-0011 decision 10.
type Config struct {
	Listen         string               `yaml:"listen"`
	Algorithm      string               `yaml:"algorithm"`
	Health         HealthConfig         `yaml:"health"`
	Circuit        CircuitConfig        `yaml:"circuit"`
	Metrics        MetricsConfig        `yaml:"metrics"`
	HealthEndpoint HealthEndpointConfig `yaml:"health_endpoint"`
	Backends       []BackendConfig      `yaml:"backends"`
}

// HealthConfig holds the active-health-check tunables shared by every backend.
// Global rather than per-backend per ADR-0011 decision 10: today's backends are
// homogeneous, and per-backend overrides can be added later without changing
// this shape.
//
// Each field is a *time.Duration rather than a time.Duration so Validate can
// distinguish "key omitted" (nil) from "key present but zero" (non-nil and
// non-positive). A plain value collapses both to 0, forcing Validate to either
// silently default an explicitly invalid value or reject an omitted one.
// Validate guarantees every pointer here is non-nil once it returns nil.
type HealthConfig struct {
	// ProbeInterval is how often each backend is probed. Omitted → DefaultProbeInterval.
	ProbeInterval *time.Duration `yaml:"probe_interval"`
	// ProbeTimeout bounds a single probe request. Omitted → DefaultProbeTimeout.
	ProbeTimeout *time.Duration `yaml:"probe_timeout"`
}

// CircuitConfig holds the circuit-breaker tunables applied to every backend.
// Global for the same reason as HealthConfig (ADR-0011 decision 10): circuit
// state is per-backend, but these knobs are not. Its fields follow the same
// nil-means-omitted convention.
type CircuitConfig struct {
	// Cooldown is how long a tripped circuit stays Open before a read may
	// promote it to Half-Open. Omitted → DefaultCircuitCooldown.
	Cooldown *time.Duration `yaml:"cooldown"`
}

// MetricsConfig holds the Prometheus exposition tunables. Always-on: there is
// no disable toggle, matching the health:/circuit: precedent. Listen follows
// the same nil-means-omitted convention as HealthConfig and CircuitConfig, so
// Validate can tell "key absent" from "key set to empty".
type MetricsConfig struct {
	// Listen is the host:port the /metrics endpoint binds. Omitted →
	// DefaultMetricsListen.
	Listen *string `yaml:"listen"`
}

// HealthEndpointConfig holds the orchestrator probe endpoint's tunables. Like
// MetricsConfig it is always-on with a single listen address; Listen follows
// the same nil-means-omitted convention. ADR-0014 (S3.T12).
type HealthEndpointConfig struct {
	// Listen is the host:port the /livez, /readyz, and /startupz endpoints
	// bind. Omitted → DefaultHealthEndpointListen.
	Listen *string `yaml:"listen"`
}

// BackendConfig describes one backend entry in the YAML config.
type BackendConfig struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// Load reads and strictly unmarshals the YAML config file at path into a
// Config, then returns it unvalidated (callers must call Validate).
//
// Strictness: yaml.NewDecoder(f).KnownFields(true) rejects unknown fields at
// decode time, so typos like `listn` fail here rather than being silently
// ignored. yaml.Unmarshal is deliberately not used.
//
// Load is pure deserialization — it does not normalize, default, or validate.
// Error handling convention (frozen S1.T0.5): errors are wrapped, not
// sentinel — fmt.Errorf("config: %w", err). The wrapped OS error remains
// inspectable via errors.Is(err, os.ErrNotExist) for callers that need it.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: load %s: %w", path, err)
	}
	defer f.Close()

	var cfg Config
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: load %s: %w", path, err)
	}
	return &cfg, nil
}

// Validate checks the config for correctness: non-empty Listen, at least
// one backend, each backend URL parseable with a host, unique backend
// names, a recognized Algorithm value, positive Sprint 3 durations, and
// host:port-valid metrics/health-endpoint listen addresses.
//
// Validate normalizes before it validates: an empty Algorithm is set to
// AlgorithmRoundRobin, and each omitted Sprint 3 duration or listen address is
// set to its exported default. After Validate returns nil, every field is
// populated and valid, so downstream consumers never re-check or re-default.
// This is why Validate mutates; splitting Normalize() out isn't justified by
// these mutations (revisit if defaults grow further).
//
// Validation is fail-fast: the first problem is returned. The order is
// Listen → backends count → per-backend name/URL → name uniqueness →
// algorithm → health/circuit durations → metrics/health_endpoint listen.
func (c *Config) Validate() error {
	if c.Algorithm == "" {
		c.Algorithm = AlgorithmRoundRobin
	}

	if c.Listen == "" {
		return errors.New("config: listen must not be empty")
	}
	if err := validateListen(c.Listen); err != nil {
		return err
	}

	if len(c.Backends) == 0 {
		return errors.New("config: at least one backend is required")
	}

	for i := range c.Backends {
		backend := &c.Backends[i]
		if err := validateBackendName(backend.Name); err != nil {
			return err
		}
		if err := validateBackendURL(backend.Name, backend.URL); err != nil {
			return err
		}
	}

	seen := make(map[string]struct{}, len(c.Backends))
	for i := range c.Backends {
		name := c.Backends[i].Name
		if _, dup := seen[name]; dup {
			return fmt.Errorf("config: duplicate backend name %q", name)
		}
		seen[name] = struct{}{}
	}

	if _, ok := implementedAlgorithms[c.Algorithm]; !ok {
		return fmt.Errorf("config: unsupported algorithm %q", c.Algorithm)
	}

	if err := c.normalizeAndValidateDurations(); err != nil {
		return err
	}
	if err := normalizeListen("metrics listen", &c.Metrics.Listen, DefaultMetricsListen); err != nil {
		return err
	}
	return normalizeListen("health_endpoint listen", &c.HealthEndpoint.Listen, DefaultHealthEndpointListen)
}

// normalizeAndValidateDurations applies the Sprint 3 defaults to every omitted
// duration and rejects an explicitly-set non-positive one. Called last so the
// pre-Sprint-3 checks keep their fail-fast order.
func (c *Config) normalizeAndValidateDurations() error {
	if err := normalizeDuration("health probe_interval", &c.Health.ProbeInterval, DefaultProbeInterval); err != nil {
		return err
	}
	if err := normalizeDuration("health probe_timeout", &c.Health.ProbeTimeout, DefaultProbeTimeout); err != nil {
		return err
	}
	return normalizeDuration("circuit cooldown", &c.Circuit.Cooldown, DefaultCircuitCooldown)
}

// normalizeListen defaults an omitted listen address to def and rejects an
// explicitly-set value that is empty or not a valid host:port. The pointer's
// nil-ness distinguishes "key absent" from "key set to empty", mirroring
// normalizeDuration. field is the config key being validated, used only to
// build an attributable error message. Both the metrics and health-endpoint
// listeners share this: they are always-on (ADR-0013 decision 8, ADR-0014
// (S3.T12)), so an explicit empty string is an error rather than a way to
// switch either off.
func normalizeListen(field string, value **string, def string) error {
	if *value == nil {
		v := def
		*value = &v
		return nil
	}
	if **value == "" {
		return fmt.Errorf("config: %s must not be empty", field)
	}
	return validateHostPort(field, **value)
}

// normalizeDuration replaces an omitted duration (nil) with def and rejects an
// explicitly-set non-positive one. The pointer's nil-ness is what distinguishes
// "key absent" from "key set to zero"; the result is written back through value.
func normalizeDuration(field string, value **time.Duration, def time.Duration) error {
	if *value == nil {
		*value = &def
		return nil
	}
	if **value <= 0 {
		return fmt.Errorf("config: %s must be positive, got %s", field, **value)
	}
	return nil
}

// validateListen enforces syntactic host:port validity for the client-traffic
// listener.
func validateListen(listen string) error {
	return validateHostPort("listen", listen)
}

// validateHostPort enforces syntactic host:port validity only. It never binds,
// resolves, or checks availability — those depend on runtime state and belong
// to http.Server.ListenAndServe. net.SplitHostPort rejects strings with no
// port ("foobar", "1.2.3.4"); the uint16 parse rejects out-of-range ports
// like 99999. field is the config key being validated ("listen",
// "metrics listen", or "health_endpoint listen"), used only to build an
// attributable error message.
func validateHostPort(field, addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("config: %s %q is not a valid host:port: %w", field, addr, err)
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return fmt.Errorf("config: %s %q has an invalid port: %w", field, addr, err)
	}
	return nil
}

func validateBackendName(name string) error {
	if name == "" {
		return errors.New("config: backend name must not be empty")
	}
	if !backendNamePattern.MatchString(name) {
		return fmt.Errorf("config: backend name %q is invalid: must match ^[a-zA-Z0-9_-]+$", name)
	}
	return nil
}

// validateBackendURL enforces a parseable URL that starts with a lowercase
// http:// or https:// scheme, has a non-empty host, and carries no query
// string or fragment. Paths are allowed by the schema; note that Sprint 1's
// proxy Director sets only scheme/host, so a configured path prefix is a
// known limitation, not joined onto the request path (see ADR-0007).
//
// The scheme is checked against the raw string before url.Parse because
// url.Parse lowercases the scheme it reports, so "HTTP://host" would
// otherwise pass a post-parse check against "http".
func validateBackendURL(name, raw string) error {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return fmt.Errorf("config: backend %q url %q must use http or https scheme", name, raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("config: backend %q has an invalid url %q: %w", name, raw, err)
	}
	if u.Host == "" {
		return fmt.Errorf("config: backend %q url %q must include a host", name, raw)
	}
	if u.RawQuery != "" {
		return fmt.Errorf("config: backend %q url %q must not include a query string", name, raw)
	}
	if u.Fragment != "" {
		return fmt.Errorf("config: backend %q url %q must not include a fragment", name, raw)
	}
	return nil
}
