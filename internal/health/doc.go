// Package health implements active health checking (periodic probes per
// backend), passive outlier detection (ejecting backends based on recent
// request outcomes), and the orchestrator probe endpoint (/livez, /readyz,
// /startupz) that exposes their state.
package health
