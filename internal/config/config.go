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

// envVarNamePattern matches the variable name inside a backend URL's ${...}
// reference: [A-Za-z_][A-Za-z0-9_]*. There is deliberately no ${VAR:-default}
// syntax; anything else is a malformed reference and fails the load. S4.T16.
var envVarNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Duration and listen defaults, applied by Validate when the corresponding
// YAML field is omitted. Exported so tests, docs, and the packages that consume
// them (health, circuit) reference one value instead of restating it. Sprint 3
// added the health/circuit/metrics defaults; S4.T7 added DefaultReadTimeout;
// S4.T8 added the four transport defaults.
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
	// DefaultDrainWindow is how long a removed backend may keep its in-flight
	// requests before they are cancelled, when reload.drain_window is omitted.
	// See ADR-0016 decision 1.
	DefaultDrainWindow = 30 * time.Second
	// DefaultReadTimeout is how long the client-facing server may take to read
	// a full request, including the body, when server.read_timeout is omitted.
	// Generous enough for slow-but-legitimate uploads while still bounding a
	// slow-loris body; the server's ReadHeaderTimeout covers slow headers.
	// S4.T7.
	DefaultReadTimeout = 60 * time.Second
	// DefaultIdleTimeout is how long an idle client keep-alive connection is
	// held open before it is closed, when server.idle_timeout is omitted. In
	// HTTP/2 modes (TLS, h2c) it is the primary connection-lifetime bound:
	// ReadTimeout is disabled there because it spans the whole multiplexed
	// connection, and IdleTimeout is what reclaims idle connections. 90s is a
	// deliberate server keep-alive bound, generous enough for multiplexed
	// HTTP/2 connections; http.Server itself has no non-zero default (left
	// unset it would derive from ReadTimeout). S5.T1.
	DefaultIdleTimeout = 90 * time.Second
	// DefaultDialTimeout bounds dialing a backend when transport.dial_timeout
	// is omitted. LAN backends accept in milliseconds; 5s is generous headroom
	// that still fails a non-accepting address fast. S4.T8.
	DefaultDialTimeout = 5 * time.Second
	// DefaultResponseHeaderTimeout bounds how long a backend may take to begin
	// answering headers before the round trip is failed, when
	// transport.response_header_timeout is omitted. S4.T8.
	DefaultResponseHeaderTimeout = 30 * time.Second
	// DefaultMaxIdleConnsPerHost is the per-host idle connection pool size when
	// transport.max_idle_conns_per_host is omitted. The stdlib default of 2 is
	// far too low for a proxy multiplexing many requests onto few backends.
	// S4.T8.
	DefaultMaxIdleConnsPerHost = 100
	// DefaultIdleConnTimeout recycles idle keep-alive connections when
	// transport.idle_conn_timeout is omitted. Kept at the stdlib default
	// deliberately; the Sprint 5 benchmarks can revisit. S4.T8.
	DefaultIdleConnTimeout = 90 * time.Second
)

// Config is the top-level load balancer configuration, loaded from YAML.
//
// Sprint 3's health and circuit knobs are nested `health:`/`circuit:` sections
// rather than flat top-level keys, so each subsystem's settings stay grouped
// and the schema remains legible as it grows. See ADR-0011 decision 10.
type Config struct {
	Listen string `yaml:"listen"`
	// TLS selects TLS mode when the `tls:` block is present (non-nil). The
	// block's presence — not an enum field — is the mode switch, so there is
	// no second source of truth that can disagree with it (S5.T1).
	TLS *TLSConfig `yaml:"tls"`
	// H2C selects cleartext HTTP/2 mode when true. It is mutually exclusive
	// with a present `tls:` block; Validate rejects them together naming both
	// (S5.T1). Plain HTTP is the absence of both. In h2c mode app.Build wraps
	// the proxy handler in golang.org/x/net/http2/h2c, which serves both
	// prior-knowledge and HTTP/1.1-Upgrade h2c connections over the plaintext
	// client listener (S5.T2).
	H2C            bool                 `yaml:"h2c"`
	Algorithm      string               `yaml:"algorithm"`
	Health         HealthConfig         `yaml:"health"`
	Circuit        CircuitConfig        `yaml:"circuit"`
	Metrics        MetricsConfig        `yaml:"metrics"`
	HealthEndpoint HealthEndpointConfig `yaml:"health_endpoint"`
	Reload         ReloadConfig         `yaml:"reload"`
	Server         ServerConfig         `yaml:"server"`
	Transport      TransportConfig      `yaml:"transport"`
	Backends       []BackendConfig      `yaml:"backends"`
}

// TLSConfig holds the client listener's TLS settings. A non-nil *TLSConfig on
// Config is the presence switch for TLS mode; the pair is loaded by app.Build
// and installed on the client http.Server, which then negotiates HTTP/2 via
// ALPN automatically. Deliberately minimal: no min_version, cipher suites, or
// OCSP — Go's crypto/tls defaults are secure, and adding those knobs later is a
// backwards-compatible extension point (S5.T1).
type TLSConfig struct {
	// CertFile is the path to the PEM-encoded certificate (or chain).
	CertFile string `yaml:"cert_file"`
	// KeyFile is the path to the PEM-encoded private key.
	KeyFile string `yaml:"key_file"`
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

// ServerConfig holds the client-facing HTTP server's tunables. Nested like the
// other subsystems so its shape can grow without a flat top-level key, and
// following the same nil-means-omitted convention.
//
// WriteTimeout is deliberately absent: the standard library's WriteTimeout
// spans end-of-request-headers through the entire response body copy, so a
// slow-but-healthy upstream (large response, slow client) would trip it even
// though nothing is wrong. It is not symmetric with ReadTimeout and must not be
// added without revisiting this reasoning (S4.T7). The residual exposure — a
// client that stalls reading while the proxy still has buffered data to send
// pins one goroutine until the OS TCP retry window, but only while data
// remains to write — is recorded in the bundle spec and is out of scope here.
type ServerConfig struct {
	// ReadTimeout bounds reading a full client request, including the body.
	// Omitted → DefaultReadTimeout. Not reloadable: NonBackendChanges names
	// "server" when it differs, so a reload that would silently change it is
	// rejected (S4.T7).
	//
	// It applies in plain HTTP mode only. In the HTTP/2 modes (TLS, h2c) the
	// server sets ReadTimeout to 0 because ReadTimeout spans the whole
	// connection and would kill every multiplexed stream on it; IdleTimeout is
	// the connection-lifetime bound there instead (S5.T1).
	ReadTimeout *time.Duration `yaml:"read_timeout"`
	// IdleTimeout bounds an idle client keep-alive connection. Omitted →
	// DefaultIdleTimeout. It applies in every mode: in plain HTTP it is the
	// idle keep-alive bound, and in the HTTP/2 modes it is the primary
	// connection-lifetime bound, where ReadTimeout is disabled (S5.T1). Not
	// reloadable: NonBackendChanges names "server" when it differs.
	IdleTimeout *time.Duration `yaml:"idle_timeout"`
}

// TransportConfig holds the upstream http.Transport tunables. Nested like the
// other subsystems and following the same nil-means-omitted convention; after
// Validate returns nil every pointer is non-nil. These knobs replace
// http.DefaultTransport for the proxy's backend connections (S4.T8).
// MaxConnsPerHost, ForceAttemptHTTP2, and backend TLS are deliberately out of
// scope.
type TransportConfig struct {
	// DialTimeout bounds dialing and connection establishment to a backend.
	// Omitted → DefaultDialTimeout.
	DialTimeout *time.Duration `yaml:"dial_timeout"`
	// ResponseHeaderTimeout bounds how long a backend may take to begin
	// answering headers. Omitted → DefaultResponseHeaderTimeout. A backend
	// that accepts a connection but never answers becomes a failure instead of
	// a pinned request.
	ResponseHeaderTimeout *time.Duration `yaml:"response_header_timeout"`
	// MaxIdleConnsPerHost is the per-host idle connection pool size. Omitted →
	// DefaultMaxIdleConnsPerHost. The transport's total MaxIdleConns is sized
	// from this and the backend count in construction code, so the per-host
	// knob is not silently capped by the stdlib default of 100.
	MaxIdleConnsPerHost *int `yaml:"max_idle_conns_per_host"`
	// IdleConnTimeout recycles idle keep-alive connections. Omitted →
	// DefaultIdleConnTimeout.
	IdleConnTimeout *time.Duration `yaml:"idle_conn_timeout"`
}

// ReloadConfig holds the zero-downtime-reload tunables. It is one field today,
// but nested like the other subsystems so its shape can grow without a flat
// top-level key. Its field follows the same nil-means-omitted convention as
// HealthConfig and CircuitConfig. ADR-0016 decision 1.
type ReloadConfig struct {
	// DrainWindow is how long a removed backend may keep its in-flight requests
	// before the drain cancels them. Omitted → DefaultDrainWindow. Not
	// reloadable: NonBackendChanges names "reload" when it differs, so a reload
	// that would silently change it is rejected (ADR-0016 decision 1).
	DrainWindow *time.Duration `yaml:"drain_window"`
}

// BackendConfig describes one backend entry in the YAML config.
type BackendConfig struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// Load reads and strictly unmarshals the YAML config file at path into a
// Config, resolves ${VAR} references in backend URLs from the process
// environment, then returns it unvalidated (callers must call Validate).
//
// Strictness: yaml.NewDecoder(f).KnownFields(true) rejects unknown fields at
// decode time, so typos like `listn` fail here rather than being silently
// ignored. yaml.Unmarshal is deliberately not used.
//
// Load is no longer pure deserialization: after decode it expands ${VAR}
// references in backend URL strings from the process environment, so the
// resolved URL is what Validate checks and a secret can live only in the
// environment. Only backend URLs are templated — every other field passes
// through untouched. Interpolation re-runs on every Load, so a reload resolves
// from the current environment. See expandBackendURLs and the S4.T16 bundle
// spec.
//
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
	if err := expandBackendURLs(&cfg); err != nil {
		return nil, fmt.Errorf("config: load %s: %w", path, err)
	}
	return &cfg, nil
}

// expandBackendURLs resolves ${VAR} references in every backend URL from the
// process environment, in place. It is the one expansion step Load runs after
// decode and before returning, so Validate and every consumer see only
// resolved values. Non-backend fields are not touched. S4.T16.
func expandBackendURLs(cfg *Config) error {
	for i := range cfg.Backends {
		expanded, err := expandEnv(cfg.Backends[i].Name, cfg.Backends[i].URL)
		if err != nil {
			return err
		}
		cfg.Backends[i].URL = expanded
	}
	return nil
}

// expandEnv resolves every ${VAR} reference in raw from the process
// environment, where VAR matches envVarNamePattern. A reference to an unset or
// empty variable, an unterminated ${, an empty ${}, or a name with invalid
// characters is an error naming the backend; the expanded value is never
// included in an error, so a resolved secret cannot reach the logs through this
// path. raw with no ${ is returned unchanged. S4.T16.
func expandEnv(backendName, raw string) (string, error) {
	if !strings.Contains(raw, "${") {
		return raw, nil
	}

	var b strings.Builder
	b.Grow(len(raw))
	for i := 0; i < len(raw); {
		if raw[i] == '$' && i+1 < len(raw) && raw[i+1] == '{' {
			rest := raw[i+2:]
			end := strings.IndexByte(rest, '}')
			if end < 0 {
				return "", fmt.Errorf("backend %q has an unterminated ${...} reference in its url", backendName)
			}
			name := rest[:end]
			if !envVarNamePattern.MatchString(name) {
				return "", fmt.Errorf("backend %q has a malformed ${...} reference %q in its url", backendName, "${"+name+"}")
			}
			value, ok := os.LookupEnv(name)
			if !ok || value == "" {
				return "", fmt.Errorf("backend %q references environment variable %q which is not set or is empty", backendName, name)
			}
			b.WriteString(value)
			i += 2 + end + 1
			continue
		}
		b.WriteByte(raw[i])
		i++
	}
	return b.String(), nil
}

// Validate checks the config for correctness: non-empty Listen, at most one
// listener mode, a complete tls: block when present, at least one backend,
// each backend URL parseable with a host, unique backend names, a recognized
// Algorithm value, positive Sprint 3 durations, and host:port-valid
// metrics/health-endpoint listen addresses.
//
// Validate normalizes before it validates: an empty Algorithm is set to
// AlgorithmRoundRobin, and each omitted Sprint 3 duration or listen address is
// set to its exported default. After Validate returns nil, every field is
// populated and valid, so downstream consumers never re-check or re-default.
// This is why Validate mutates; splitting Normalize() out isn't justified by
// these mutations (revisit if defaults grow further).
//
// Validation is fail-fast: the first problem is returned. The order is
// Listen → listener mode → backends count → per-backend name/URL → name
// uniqueness → algorithm → health/circuit/reload/server/transport durations →
// metrics/health_endpoint listen.
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

	if c.TLS != nil && c.H2C {
		return errors.New("config: h2c and tls are mutually exclusive; remove one")
	}
	if c.TLS != nil {
		if c.TLS.CertFile == "" {
			return errors.New("config: tls cert_file must not be empty")
		}
		if c.TLS.KeyFile == "" {
			return errors.New("config: tls key_file must not be empty")
		}
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

// normalizeAndValidateDurations applies the Sprint 3, S4.T7, S4.T8, and S5.T1
// defaults to every omitted duration and rejects an explicitly-set non-positive
// one. Called last so the listener and backend checks keep their fail-fast
// order.
func (c *Config) normalizeAndValidateDurations() error {
	if err := normalizeDuration("health probe_interval", &c.Health.ProbeInterval, DefaultProbeInterval); err != nil {
		return err
	}
	if err := normalizeDuration("health probe_timeout", &c.Health.ProbeTimeout, DefaultProbeTimeout); err != nil {
		return err
	}
	if err := normalizeDuration("circuit cooldown", &c.Circuit.Cooldown, DefaultCircuitCooldown); err != nil {
		return err
	}
	if err := normalizeDuration("reload drain_window", &c.Reload.DrainWindow, DefaultDrainWindow); err != nil {
		return err
	}
	if err := normalizeDuration("server read_timeout", &c.Server.ReadTimeout, DefaultReadTimeout); err != nil {
		return err
	}
	if err := normalizeDuration("server idle_timeout", &c.Server.IdleTimeout, DefaultIdleTimeout); err != nil {
		return err
	}
	if err := normalizeDuration("transport dial_timeout", &c.Transport.DialTimeout, DefaultDialTimeout); err != nil {
		return err
	}
	if err := normalizeDuration("transport response_header_timeout", &c.Transport.ResponseHeaderTimeout, DefaultResponseHeaderTimeout); err != nil {
		return err
	}
	if err := normalizeDuration("transport idle_conn_timeout", &c.Transport.IdleConnTimeout, DefaultIdleConnTimeout); err != nil {
		return err
	}
	return normalizePositiveInt("transport max_idle_conns_per_host", &c.Transport.MaxIdleConnsPerHost, DefaultMaxIdleConnsPerHost)
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

// normalizePositiveInt replaces an omitted int (nil) with def and rejects an
// explicitly-set non-positive one, mirroring normalizeDuration for the
// transport's max_idle_conns_per_host knob.
func normalizePositiveInt(field string, value **int, def int) error {
	if *value == nil {
		*value = &def
		return nil
	}
	if **value <= 0 {
		return fmt.Errorf("config: %s must be positive, got %d", field, **value)
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
//
// Every error names only the backend and the url field, never the raw URL:
// after env interpolation (S4.T16) the raw string can embed a resolved secret,
// and main logs validation errors. url.Parse's own error is deliberately not
// wrapped here because it echoes the raw URL; the parse branch reports only
// that the url is invalid (S4.T17).
func validateBackendURL(name, raw string) error {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return fmt.Errorf("config: backend %q url must use http or https scheme", name)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("config: backend %q has an invalid url", name)
	}
	if u.Host == "" {
		return fmt.Errorf("config: backend %q url must include a host", name)
	}
	if u.RawQuery != "" {
		return fmt.Errorf("config: backend %q url must not include a query string", name)
	}
	if u.Fragment != "" {
		return fmt.Errorf("config: backend %q url must not include a fragment", name)
	}
	return nil
}
