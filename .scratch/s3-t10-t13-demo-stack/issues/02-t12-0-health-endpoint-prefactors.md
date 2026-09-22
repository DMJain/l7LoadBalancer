# 02: S3.T12.0 — Health-endpoint prefactor APIs (`ProbeRoundComplete`, `RecordProbe`, config block)

**What to build:** The three pure additive APIs and one schema addition that
ticket 03 (the health-endpoint handlers) will consume. Nothing here is
user-visible on its own; the acceptance signal is unit tests plus visible
Prometheus registration. This is the same prefactor pattern S3.T0.1, S3.T0.2,
and S3.T0.3 established at the start of Sprint 3.

Three surfaces land in this ticket:

1. **Active health checker** gains `ProbeRoundComplete() bool`, backed by a
   single `atomic.Bool` that flips from `false` to `true` exactly once, after
   the first full sweep in which every configured backend has been probed at
   least once. Stays `true` for the checker's lifetime — a one-shot latch.
2. **Metrics collector** gains `RecordProbe(endpoint string, statusCode int)`,
   which drives a new `lb_health_probe_total{endpoint, status}` counter
   registered on the same private Prometheus registry as everything else.
   Cardinality is bounded (3 endpoints × ~2 status classes = 6 series).
   Mirrors the existing `RecordRequest` shape so registration stays
   centralised.
3. **Config schema** gains a top-level `health_endpoint: { listen: ":8081" }`
   block. `listen` is optional with default `":8081"`. `KnownFields(true)`
   still rejects typos. `Validate()` parses the string as a `host:port` and
   rejects anything else, matching the existing `metrics.listen` shape.

**Blocked by:** 01.

**Status:** done

- [x] `HealthChecker.ProbeRoundComplete() bool` exists, is race-safe under
      `-race`, and returns `false` before any probe round completes, `true`
      after every configured backend has been probed at least once, and stays
      `true` under further probe rounds. Table-driven test with a fake probe
      transport, N backends, and enforced probe ordering.
- [x] `Collector.RecordProbe(endpoint string, statusCode int)` exists and
      increments `lb_health_probe_total{endpoint, status}` on a private
      Prometheus registry. Verified via `promtestutil.CollectAndCount` /
      `.ToFloat64`, exactly the pattern the existing `internal/metrics` tests
      use.
- [x] The `status` label uses status-class values (`2xx`, `5xx`, …) matching
      the ADR-0013 convention for `lb_requests_total`, not raw status codes —
      keeps label cardinality bounded and the two counters visually
      consistent on a Grafana panel.
- [x] Config schema gains the `health_endpoint: { listen: ":8081" }` block.
      Unknown fields under `health_endpoint` are rejected by `KnownFields(true)`;
      an unparseable `listen` fails `Validate()` with a wrapped error naming
      the field.
- [x] `configs/example.yaml` gains the new block as an inline-commented
      example with the default value, matching the way `metrics.listen` was
      documented in S3.T4.
- [x] Nothing in `internal/proxy`, `internal/circuit`, or `cmd/l7LoadBalancer`
      needs to change for this ticket. If a change to any of them is tempting,
      it belongs to ticket 03.
- [x] All new code carries the standard Sprint 3 doc-comment convention:
      one line pointing at ADR-0014 (which ticket 03 writes) as a
      forward-reference — comment can be added as `// ADR-0014 (S3.T12).`
      even before the ADR file exists in the repo, so ticket 03's diff is
      only the new file.
- [x] `make test`, `make test-race`, `go vet ./...`, and `make fmt` all pass.
- [x] `PROGRESS.md` entry for this ticket links back to spec decisions
      D8, D9, D11 for provenance.
