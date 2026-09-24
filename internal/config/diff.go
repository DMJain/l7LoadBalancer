package config

import "time"

// This file holds the pure comparison a reload is decided on. Neither function
// logs, returns an error, or touches runtime state: a reload is rejected when
// NonBackendChanges is non-empty, and the BackendDiff is what apply (S4.T2)
// turns into the next registry snapshot. Both functions are stateless, so they
// are safe to call from any goroutine. The reload architecture is recorded in
// ADR-0015.

// BackendDiff is the result of comparing two configs by backend identity.
// Added and Unchanged preserve the new config's order; Removed preserves the
// old config's order.
type BackendDiff struct {
	Added     []BackendConfig
	Removed   []BackendConfig
	Unchanged []BackendConfig
}

// DiffBackends compares oldCfg and newCfg by backend identity — the (name, URL)
// pair — and reports which backends were added, removed, and unchanged. A name
// kept with a changed URL is one removal plus one addition, never an update:
// state learned about the old host must not be applied to a different host.
// Duplicate names are already rejected by Validate, so identity is unique
// within each config. Order is neither identity nor equality: a pure reorder
// diffs to all-unchanged. See ADR-0015 decisions 2–3.
func DiffBackends(oldCfg, newCfg *Config) BackendDiff {
	oldIdentities := make(map[string]struct{}, len(oldCfg.Backends))
	for _, b := range oldCfg.Backends {
		oldIdentities[backendIdentity(b)] = struct{}{}
	}
	newIdentities := make(map[string]struct{}, len(newCfg.Backends))
	for _, b := range newCfg.Backends {
		newIdentities[backendIdentity(b)] = struct{}{}
	}

	var diff BackendDiff
	for _, b := range newCfg.Backends {
		if _, ok := oldIdentities[backendIdentity(b)]; ok {
			diff.Unchanged = append(diff.Unchanged, b)
		} else {
			diff.Added = append(diff.Added, b)
		}
	}
	for _, b := range oldCfg.Backends {
		if _, ok := newIdentities[backendIdentity(b)]; !ok {
			diff.Removed = append(diff.Removed, b)
		}
	}
	return diff
}

// NonBackendChanges returns the names of the non-backend config fields that
// differ between oldCfg and newCfg, in the fixed order listen, algorithm,
// health, circuit, metrics, health_endpoint. A non-empty result is what makes
// a reload be rejected whole; only the backend list is reloadable. Values are
// compared as resolved, so an omitted field and an explicit default compare
// equal after Validate's defaulting. A section with any differing sub-field is
// named once by its section name. See ADR-0015 decision 4.
func NonBackendChanges(oldCfg, newCfg *Config) []string {
	var changed []string
	if oldCfg.Listen != newCfg.Listen {
		changed = append(changed, "listen")
	}
	if resolvedAlgorithm(oldCfg) != resolvedAlgorithm(newCfg) {
		changed = append(changed, "algorithm")
	}
	if resolvedDuration(oldCfg.Health.ProbeInterval, DefaultProbeInterval) != resolvedDuration(newCfg.Health.ProbeInterval, DefaultProbeInterval) ||
		resolvedDuration(oldCfg.Health.ProbeTimeout, DefaultProbeTimeout) != resolvedDuration(newCfg.Health.ProbeTimeout, DefaultProbeTimeout) {
		changed = append(changed, "health")
	}
	if resolvedDuration(oldCfg.Circuit.Cooldown, DefaultCircuitCooldown) != resolvedDuration(newCfg.Circuit.Cooldown, DefaultCircuitCooldown) {
		changed = append(changed, "circuit")
	}
	if resolvedString(oldCfg.Metrics.Listen, DefaultMetricsListen) != resolvedString(newCfg.Metrics.Listen, DefaultMetricsListen) {
		changed = append(changed, "metrics")
	}
	if resolvedString(oldCfg.HealthEndpoint.Listen, DefaultHealthEndpointListen) != resolvedString(newCfg.HealthEndpoint.Listen, DefaultHealthEndpointListen) {
		changed = append(changed, "health_endpoint")
	}
	return changed
}

// backendIdentity is the identity key for the diff: the (name, URL) pair. The
// NUL separator cannot occur in either a validated name or a URL.
func backendIdentity(b BackendConfig) string {
	return b.Name + "\x00" + b.URL
}

// resolvedAlgorithm applies Validate's default so an omitted algorithm and an
// explicit "round_robin" compare equal.
func resolvedAlgorithm(c *Config) string {
	if c.Algorithm == "" {
		return AlgorithmRoundRobin
	}
	return c.Algorithm
}

// resolvedDuration applies the field's default so an omitted duration and an
// explicit default compare equal.
func resolvedDuration(value *time.Duration, def time.Duration) time.Duration {
	if value == nil {
		return def
	}
	return *value
}

// resolvedString applies the field's default so an omitted listen address and
// an explicit default compare equal.
func resolvedString(value *string, def string) string {
	if value == nil {
		return def
	}
	return *value
}
