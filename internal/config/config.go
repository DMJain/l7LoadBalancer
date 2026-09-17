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
// select. It deliberately excludes the Sprint 2 identifiers until the
// selectors that back them exist: validation answers "will this work?", not
// "does this parse?". Adding a selector is a one-line addition here.
//
// See spec: "Validation encodes will this work?, not does this parse?"
var implementedAlgorithms = map[string]struct{}{
	AlgorithmRoundRobin: {},
	AlgorithmLeastConn:  {},
}

// backendNamePattern restricts backend names to the intersection of what is
// safe in every downstream context: Prometheus label values, structured log
// field values, grep targets, and future admin UIs. No escaping needed.
var backendNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

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
// names, and a recognized Algorithm value.
//
// Validate normalizes before it validates: an empty Algorithm is set to
// AlgorithmRoundRobin. After Validate returns nil, every field is populated
// and valid, so downstream consumers never re-check or re-default. This is
// why Validate mutates; splitting Normalize() out isn't justified by a single
// mutation (revisit if defaults grow in Sprint 3+).
//
// Validation is fail-fast: the first problem is returned. The order is
// Listen → backends count → per-backend name/URL → name uniqueness →
// algorithm.
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
		b := &c.Backends[i]
		if err := validateBackendName(b.Name); err != nil {
			return err
		}
		if err := validateBackendURL(b.Name, b.URL); err != nil {
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
	return nil
}

// validateListen enforces syntactic host:port validity only. It never binds,
// resolves, or checks availability — those depend on runtime state and belong
// to http.Server.ListenAndServe. net.SplitHostPort rejects strings with no
// port ("foobar", "1.2.3.4"); the uint16 parse rejects out-of-range ports
// like 99999.
func validateListen(listen string) error {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("config: listen %q is not a valid host:port: %w", listen, err)
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return fmt.Errorf("config: listen %q has an invalid port: %w", listen, err)
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
// string or fragment. Paths are allowed — httputil.ReverseProxy joins path
// prefixes correctly in its default Director.
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
