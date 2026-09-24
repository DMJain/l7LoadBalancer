package config

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
	oldIdentities := backendIdentities(oldCfg.Backends)
	newIdentities := backendIdentities(newCfg.Backends)

	var diff BackendDiff
	for _, b := range newCfg.Backends {
		if _, ok := oldIdentities[BackendIdentity(b)]; ok {
			diff.Unchanged = append(diff.Unchanged, b)
		} else {
			diff.Added = append(diff.Added, b)
		}
	}
	for _, b := range oldCfg.Backends {
		if _, ok := newIdentities[BackendIdentity(b)]; !ok {
			diff.Removed = append(diff.Removed, b)
		}
	}
	return diff
}

// NonBackendChanges returns the names of the non-backend config fields that
// differ between oldCfg and newCfg, in the fixed order listen, algorithm,
// health, circuit, metrics, health_endpoint. A non-empty result is what makes
// a reload be rejected whole; only the backend list is reloadable. A section
// with any differing sub-field is named once by its section name. See ADR-0015
// decision 4.
//
// Both configs must be validated. Validate materializes every default (a
// non-empty Algorithm and non-nil duration and listen pointers), so comparing
// the fields directly already compares resolved values: an omitted field and
// an explicit default are equal by the time they reach here, and there is no
// re-defaulting to do.
func NonBackendChanges(oldCfg, newCfg *Config) []string {
	var changed []string
	if oldCfg.Listen != newCfg.Listen {
		changed = append(changed, "listen")
	}
	if oldCfg.Algorithm != newCfg.Algorithm {
		changed = append(changed, "algorithm")
	}
	if *oldCfg.Health.ProbeInterval != *newCfg.Health.ProbeInterval ||
		*oldCfg.Health.ProbeTimeout != *newCfg.Health.ProbeTimeout {
		changed = append(changed, "health")
	}
	if *oldCfg.Circuit.Cooldown != *newCfg.Circuit.Cooldown {
		changed = append(changed, "circuit")
	}
	if *oldCfg.Metrics.Listen != *newCfg.Metrics.Listen {
		changed = append(changed, "metrics")
	}
	if *oldCfg.HealthEndpoint.Listen != *newCfg.HealthEndpoint.Listen {
		changed = append(changed, "health_endpoint")
	}
	return changed
}

// BackendIdentity is the identity key for a backend in a reload diff: the
// (name, URL) pair, per ADR-0015 decision 2. It is exported so the registry's
// Apply classifies added and unchanged backends by the exact same key the diff
// does, rather than restating the identity contract. The NUL separator cannot
// occur in either a validated name or a URL.
func BackendIdentity(b BackendConfig) string {
	return b.Name + "\x00" + b.URL
}

// backendIdentities builds the identity set of a backend list.
func backendIdentities(cfgs []BackendConfig) map[string]struct{} {
	ids := make(map[string]struct{}, len(cfgs))
	for _, b := range cfgs {
		ids[BackendIdentity(b)] = struct{}{}
	}
	return ids
}
