# Sprint 5 Spec: HTTP/2 Support & Benchmark Harness (S5.T1–T4)

Status: ready-for-agent

---

## Problem Statement

The load balancer serves plain HTTP only. It cannot terminate TLS, negotiate HTTP/2 via ALPN, serve HTTP/2 over cleartext (h2c), or speak HTTP/2 to backends. There is no benchmark harness to produce reproducible performance numbers, and no head-to-head comparison against Nginx exists. Without these, the project cannot demonstrate production-grade transport capabilities, and performance claims remain unsubstantiated.

The existing metric `lb_active_connections` is also semantically wrong for an HTTP/2 world: it tracks per-request lifecycle, not TCP connections, and HTTP/2 multiplexes many requests over few TCP connections. This must be corrected before publishing benchmark numbers that reference the metric.

## Solution

Add three mutually exclusive listener modes (plain HTTP, TLS with ALPN-negotiated HTTP/2, and h2c), HTTP/2 support for backend connections via scheme-driven protocol selection, and a fully containerized benchmark harness using vegeta that produces reproducible throughput and latency numbers across a controlled matrix of algorithms, response sizes, and protocols — with honest, constrained-match Nginx comparisons.

## User Stories

1. As an operator, I want to configure the LB to terminate TLS with my own cert and key files, so that client connections are encrypted without an external TLS terminator.
2. As an operator, I want TLS mode to automatically negotiate HTTP/2 via ALPN, so that HTTP/2-capable clients get multiplexed connections without extra configuration beyond providing cert/key paths.
3. As an operator, I want to enable h2c mode for environments where TLS termination happens upstream (e.g., behind a CDN or hardware TLS terminator), so that I get HTTP/2 multiplexing performance without managing certificates at the LB layer.
4. As an operator, I want only one listener mode active at a time (plain, TLS, or h2c), so that there is no ambiguity about what protocol the LB is serving and the config is impossible to misconfigure into a contradictory state.
5. As an operator, I want a clear validation error when I accidentally set both `tls:` and `h2c: true`, so that mutual exclusivity is enforced at startup rather than producing undefined behavior.
6. As an operator, I want the LB to automatically disable `ReadTimeout` in HTTP/2 modes and rely on `IdleTimeout` instead, so that long-lived multiplexed connections are not killed by a connection-level read deadline designed for HTTP/1.1 slow-loris protection.
7. As an operator, I want metrics and health endpoints to remain plain HTTP regardless of client listener mode, so that Prometheus scrape configs and health check probes do not need TLS trust configuration.
8. As an operator, I want to point backends at `https://` URLs, so that the LB speaks HTTP/2 to them via ALPN without needing a separate transport configuration mode.
9. As an operator, I want to mix `http://` and `https://` backend URLs in the same config, so that I can migrate backends to HTTP/2 incrementally without an all-or-nothing cutover.
10. As an operator, I want an explicit `tls_skip_verify` config knob defaulting to `false`, so that self-signed backend certs work in test/staging environments without hardcoding insecure defaults that would be a security red flag in production.
11. As an operator, I want `force_http2: true` as an explicit, visible transport config field, so that HTTP/2 negotiation to backends is documented in config and can be disabled for debugging without changing backend URLs.
12. As an operator, I want the `lb_requests_total` metric to include a `protocol` label (`http/1.1`, `h2`, `h2c`), so that I can verify in dashboards and alerting that HTTP/2 is actually being negotiated and not silently falling back.
13. As an operator, I want the active-requests metric accurately named `lb_active_requests` (not `lb_active_connections`), so that the metric name matches its semantics when HTTP/2 multiplexes many requests over few TCP connections.
14. As an operator, I want TLS configuration changes to require a full restart (not SIGHUP reload), so that the non-reloadable policy for non-backend config is consistently enforced and I don't accidentally break TLS mid-flight.
15. As a developer, I want a `scripts/generate-cert.sh` that produces a SAN certificate covering localhost, 127.0.0.1, ::1, and all docker-compose service names, so that TLS works in local dev and benchmarks without manual OpenSSL incantations.
16. As a developer, I want the existing dummy backend extended with `/200b`, `/10kb`, `/1mb`, and `/health` response-size endpoints, so that benchmarks can exercise different payload profiles without deploying separate backend images.
17. As a developer, I want the dummy backend to optionally serve TLS via `TLS_CERT_FILE` / `TLS_KEY_FILE` env vars, so that HTTP/2 backend benchmarks work with the same binary image and the same env-var configuration pattern as `SLEEP_MS` / `FAIL_RATE`.
18. As a developer, I want the dummy backend to automatically serve HTTP/2 when TLS is enabled, so that I don't need HTTP/2-specific backend configuration — Go's default `ListenAndServeTLS` behavior is correct.
19. As a developer, I want a docker-compose file with the LB, Nginx, 4 backends, and a vegeta container on a single flat bridge network, so that benchmarks run identically on any machine with Docker and results are not affected by host OS networking variability.
20. As a developer, I want vegeta running as a container inside compose (not on the host), so that networking overhead is consistent and the benchmark is self-contained without a "install vegeta on your host" prerequisite.
21. As a developer, I want a `bench/run.sh` script with slices (`core`, `protocol`, `failure`, `all`), so that I can iterate on one benchmark section during development without waiting for the full 50-run matrix.
22. As a developer, I want peak throughput discovered via automated binary search (doubling rate until p99 exceeds 100ms or error rate exceeds 1%, then narrowing), so that published numbers are reproducible and not dependent on manual tuning or human interpretation.
23. As a developer, I want the binary search warmup period (first 3–5 seconds) discarded from each step, so that cold-start connection establishment artifacts don't skew the measured rates.
24. As a developer, I want only the two algorithm pairs with true Nginx equivalents (roundrobin ↔ round-robin, leastconn ↔ least_conn) benchmarked head-to-head, so that published comparisons don't misleadingly map consistent-hash to ip_hash or P2C-EWMA to least_conn when they are fundamentally different algorithms.
25. As a developer, I want consistent-hash and P2C-EWMA benchmarked solo (LB-only), so that their performance characteristics are documented without false Nginx equivalences.
26. As a developer, I want Nginx benchmarked with matched algorithm, topology, backend count, and keepalive settings, so that comparisons isolate implementation quality rather than conflating algorithms, tuning, and implementation in one number.
27. As a developer, I want Nginx configs published in the repo with every parameter documented side by side with the LB config, so that the comparison methodology is transparent and auditable.
28. As a developer, I want Nginx to also serve HTTP/2 (via TLS) in the protocol comparison slice, so that the HTTP/1.1-vs-HTTP/2 comparison tests the protocol difference, not the protocol-and-implementation difference.
29. As a developer, I want failure-mode benchmarks (backend-kill and SIGHUP-under-load) at 50% of peak rate with specific measurements (error count, detection time, recovery time, drop count), so that resilience patterns are demonstrated under load with quantifiable results.
30. As a developer, I want benchmark results stored as `.txt` summaries and `.hdr` histogram files with parameter-encoded filenames in `bench/results/{core,protocol,failure}/`, so that published numbers are auditable, re-plottable, and self-documenting.
31. As a developer, I want each `run.sh` slice to print a summary table to stdout (algorithm, size, competitor, p50, p99, throughput), so that I can evaluate results during development without grepping through output files.
32. As a developer, I want an ADR (ADR-0020) recording the vegeta-over-wrk decision, so that the tool choice is defensible when an interviewer asks "why not wrk, the industry standard?"
33. As a developer, I want eight fully self-contained benchmark YAML configs (`bench/configs/{http11,h2}/ × {roundrobin,leastconn,consistent-hash,p2c-ewma}`), so that every benchmark run is backed by a complete, readable config file with no templating or sed pipelines to debug.

## Implementation Decisions

### 1. Listener mode model — single listener, config-switched, mutually exclusive

The LB supports exactly one of three listener modes at a time: plain HTTP (default), TLS with ALPN, or h2c. There is no dual-listener design (e.g., `:8080` for plain + `:8443` for TLS simultaneously). One `http.Server` for client traffic, one shutdown/reload code path.

**Rationale**: This is a portfolio project demonstrating capability, not a production edge proxy. A single listener keeps the code simple, the config schema clean, and the benchmark matrix manageable. Dual-listener adds wiring complexity (two client-facing `http.Server` instances, coordinated shutdown, reload interactions) for a feature no one will use.

### 2. Config expression — presence-based, not enum-based

A `tls:` block being present in the YAML triggers TLS mode. `h2c: true` triggers h2c mode. Neither present means plain HTTP. Validation rejects `tls:` and `h2c: true` appearing simultaneously with an explicit error message about mutual exclusivity: **"h2c and tls are mutually exclusive; remove one"** (or similar).

There is no `protocol: plain|tls|h2c` enum field. Presence-based is idiomatic YAML (matches Docker, Nginx, Traefik config patterns) and avoids redundancy (`protocol: tls` + `tls:` block both saying the same thing) and the new class of validation errors when they disagree.

**Nuance for implementer**: `h2c: true` without a `tls:` block is the trigger. Bare `h2c: true` with a `tls:` block present is a validation error — h2c does not "win." The error message must be explicit about mutual exclusivity.

### 3. TLS config surface — minimal

The `tls:` block contains only `cert_file` and `key_file`. No `min_version`, no `cipher_suites`, no OCSP stapling. Go's `crypto/tls` defaults are genuinely good: TLS 1.2+ minimum, secure cipher ordering, preference for TLS 1.3 when the client supports it.

Adding configurable cipher suites invites misconfiguration and demonstrates nothing in the portfolio. The extension point is backwards-compatible — adding `min_version` later is a non-breaking config change. Document the extension point in comments, don't build it.

Config schema shape:
```yaml
listen: ":8443"
tls:
  cert_file: "certs/server.crt"
  key_file: "certs/server.key"
```

New `TLSConfig` struct with `CertFile` and `KeyFile` string fields. New `H2C` bool field on the root `Config`.

### 4. Handler chain — three distinct branches, not unconditional wrapper

The `http.Server` handler is built per mode in `App.Build`. This is NOT an unconditional h2c wrapper with a no-op path for non-h2c modes — it is three separate handler chains:

- **Plain HTTP:** `metricsMiddleware(proxyHandler)` — identical to today.
- **TLS+ALPN:** `metricsMiddleware(proxyHandler)` on an `http.Server` with `TLSConfig` set. Go auto-configures HTTP/2 via `http2.ConfigureServer`. Server calls `ListenAndServeTLS`.
- **h2c:** `h2c.NewHandler(metricsMiddleware(proxyHandler), &http2.Server{})` — h2c handler wraps everything else as the outermost layer.

**Why h2c must be outermost**: `h2c.NewHandler` inspects raw connection bytes for the HTTP/2 `PRI * HTTP/2.0\r\n` preface to detect prior-knowledge connections. Any middleware upstream that assumes HTTP/1.1 framing will choke on a prior-knowledge HTTP/2 connection. The h2c handler passes HTTP/1.1 requests through to the inner handler unchanged, and also handles the HTTP/1.1 Upgrade mechanism.

**Why not conditional wrap**: In plain HTTP mode, the h2c handler is not present at all. This avoids executing h2c detection logic (however cheap) on every connection in the most common mode, and makes the code self-documenting about what each mode does.

### 5. h2c — both prior knowledge and HTTP/1.1 Upgrade

`h2c.NewHandler` supports both connection modes out of the box. Prior knowledge: client speaks HTTP/2 frames immediately on a plaintext connection. Upgrade: client sends an HTTP/1.1 request with `Upgrade: h2c` header, server responds 101. Restricting to prior-knowledge-only would require writing code to *remove* functionality, for no benefit.

### 6. h2c adds `golang.org/x/net` — first x/ dependency

The `h2c` package lives in `golang.org/x/net/http2/h2c`. This is the first `x/` dependency in the project. `x/net` is semi-stdlib — maintained by the Go team, versioned separately from the main Go release. There is no stdlib alternative for h2c support. This may warrant an amendment to ADR-0004 (stdlib-only dependencies) noting the exception and rationale.

### 7. HTTP/2 server configuration — zero-value defaults

Both h2c and TLS modes use `&http2.Server{}` with no fields set. Go defaults: `MaxConcurrentStreams` = 250, `MaxReadFrameSize` = 16KB. No tuning without benchmark data.

**Rationale**: Premature tuning of `MaxConcurrentStreams` without data looks bad in a portfolio review — "why 1000?" "seemed higher." If benchmarks in T4 show HTTP/2 framing as a bottleneck, tune in a follow-up with measured justification.

### 8. ReadTimeout disabled in HTTP/2 modes — hardcoded per-mode

Go's `http.Server.ReadTimeout` applies to the *entire connection*, not per-stream. On a multiplexed HTTP/2 connection, this kills all active streams after N seconds regardless of activity. This is architecturally wrong.

Behavior per mode:
- **Plain HTTP:** `ReadTimeout` set from config (slow-loris protection per Sprint 4 ADR).
- **TLS mode:** `ReadTimeout: 0`. Rely on `IdleTimeout` for idle connection cleanup.
- **h2c mode:** `ReadTimeout: 0`. Same reasoning — h2c is HTTP/2 regardless of encryption.

This is hardcoded per-mode behavior in server construction, not a config knob. The operator doesn't choose whether HTTP/2 gets `ReadTimeout`; it's architecturally wrong and the code disables it.

HTTP/2's stream-level flow control replaces connection-level `ReadTimeout` for slow-client protection. A malicious client can open streams without sending data, but `MaxConcurrentStreams` (250) bounds the damage, and `IdleTimeout` reclaims idle connections.

The existing `ServerConfig` struct needs an `IdleTimeout` field added — it was implicit before, now it's needed as the primary timeout in HTTP/2 modes.

**Implementer note**: Add a code comment in server construction referencing the Go docs on `ReadTimeout` and HTTP/2 semantics, and note the Sprint 4 slow-loris ADR as the HTTP/1.1 protection context.

### 9. TLS is non-reloadable

All TLS fields (`tls:` block and `h2c:` flag) are added to the `NonBackendChanges()` comparison. Changing cert paths, toggling h2c, or switching listener modes requires a full process restart. This is consistent with the existing non-backend-non-reloadable policy established in Sprint 4.

Cert hot-reload via `tls.Config.GetCertificate` (re-reading cert files per handshake for rotation) is explicitly deferred to a future ticket. It has its own test surface: file watching or polling, error handling on corrupt cert files, race with in-flight handshakes. Mention it in `what-id-do-differently.md`.

### 10. Metrics and health endpoints — always plain HTTP

Regardless of client listener mode. No TLS config surface for operational endpoints. Prometheus scrape targets are almost always plain HTTP behind a firewall; health check endpoints likewise. Adding TLS to these would require Prometheus to trust the self-signed cert, adding harness complexity for zero portfolio value.

### 11. mTLS — out of scope

Mutual TLS (client certificate verification) is a separate concern: authentication, not transport. It doesn't affect benchmarks, doesn't demonstrate HTTP/2, and adds config/test surface. Already covered by ADR-0005's "out of scope" boundary.

### 12. Certificate generation script

New `scripts/generate-cert.sh` producing a self-signed SAN certificate. **Must use SANs, not just CN** — Go's `crypto/tls` client rejects certificates without Subject Alternative Names since Go 1.15.

SAN list: `localhost`, `127.0.0.1`, `::1`, `backend1`, `backend2`, `backend3`, `backend4`, `lb`. Output: cert and key file pair at a known path (e.g., `certs/server.crt`, `certs/server.key`). The `certs/` directory should be gitignored.

The same cert is used by frontend (LB serving TLS) and backends (dummy backends serving TLS for HTTP/2 benchmarks). Separate frontend/backend certs add complexity for zero security benefit in a benchmark environment. Expanding the SAN list costs nothing.

### 13. Backend transport — scheme-driven protocol selection

Backend URL scheme determines protocol:
- `http://` → HTTP/1.1 (unchanged from today)
- `https://` → HTTP/2 via ALPN (Go auto-negotiates when `ForceAttemptHTTP2` is set)

No custom h2c-to-backends plumbing (which would require `http2.Transport` with `AllowHTTP: true` and a custom `DialTLSContext`). Scheme-driven is simpler and maps to how real deployments work.

### 14. Global transport — single shared `*http.Transport`

One transport for all backends, as today. No per-backend transports. Per-backend transports would fragment connection pools, change performance characteristics, and break the active-request accounting (the `ModifyResponse` body-wrapper pattern assumes a shared transport).

### 15. Mixed backend URL schemes — allowed

A config can contain both `http://` and `https://` backend URLs. Go's `http.Transport` handles mixed schemes correctly per-request — it auto-negotiates TLS for `https://` URLs and dials plaintext for `http://` URLs on the same transport. `ForceAttemptHTTP2: true` only affects TLS connections; it doesn't try h2c on `http://` URLs.

No validation, no warning. This is a legitimate transitional state (migrating backends to HTTP/2 one at a time).

### 16. New transport config fields

```yaml
transport:
  force_http2: true          # default true; set false to force HTTP/1.1 to backends
  tls_skip_verify: false     # default false; required for self-signed backend certs
  dial_timeout: 5s           # existing
  response_header_timeout: 10s  # existing
  max_idle_conns_per_host: 100  # existing
  idle_conn_timeout: 90s     # existing
```

**`force_http2`** (default `true`): An explicit knob lets someone disable HTTP/2 to backends for debugging without changing all backend URLs. The config default is `true`, but the code must explicitly set `ForceAttemptHTTP2: true` on the transport.

**`tls_skip_verify`** (default `false`): Required for self-signed backend certs. An explicit config knob (not hardcoded `InsecureSkipVerify: true`) signals understanding of the security tradeoff. An interviewer seeing hardcoded skip-verify is a red flag; a config knob with documentation is a signal you understand the tradeoff.

### 17. The `ForceAttemptHTTP2` gotcha — CRITICAL

Go's `http.Transport` does **NOT** default `ForceAttemptHTTP2` to `true`. It only auto-negotiates HTTP/2 when:
1. The transport is zero-value, AND
2. `TLSClientConfig` is nil (not set at all).

The moment you set `TLSClientConfig` (which you must, for `InsecureSkipVerify`), automatic HTTP/2 is **silently lost**. The transport falls back to HTTP/1.1 without error, without warning, without logging. You MUST explicitly set `ForceAttemptHTTP2: true` on the transport struct.

This is the single most common Go HTTP/2 mistake. The implementer must set this explicitly in `buildTransport()` when `force_http2` config is true.

### 18. Metric rename: `lb_active_connections` → `lb_active_requests`

The current metric tracks per-request lifecycle (increment in `reqState.activate()`, decrement in `reqState.release()`), not TCP connections. With HTTP/2 multiplexing, 100 in-flight requests may use 1 TCP connection, but the gauge reads 100. The metric is "active requests to this backend."

Sprint 5 is the last clean rename window — no external consumers, no published dashboards, no alerts reference the name yet. After benchmarks are published, the name becomes load-bearing.

Touches:
- Metric registration (name string and all method names: `IncActive*`, `DecActive*`, `SetActive*`, `DeleteActive*`)
- Proxy callers (`reqState.activate`, `reqState.release`)
- Grafana dashboard JSON (from S3.T7)
- AGENTS.md locked vocabulary

This is a correctness fix recorded in a commit message, not an ADR — the old name was simply wrong for HTTP/2, no alternatives were considered.

### 19. Protocol observability — label on existing metric + log field

**Client-facing**: add `protocol` label to the existing `lb_requests_total` counter. Values: `http/1.1`, `h2`, `h2c`. Source: `req.Proto` in the proxy's request observation path. Cardinality increase is bounded: exactly 3 values. This goes on the existing counter, not a new counter.

**Backend-facing**: test assertion on `resp.Proto == "HTTP/2.0"` for HTTPS backends is the primary regression gate. For runtime visibility, add a structured log field `backend_proto`. No dedicated backend-protocol metric — don't over-instrument.

**Nuance**: `resp.Proto` on the backend side tells you if the LB→backend leg negotiated HTTP/2. The test assertion catches regressions. The log field is visible during benchmarks in structured log output.

### 20. Dummy backend extension

Extend the existing binary (which already supports `-name`, `-addr`, `SLEEP_MS`, `FAIL_RATE`):

**New endpoints**: `/200b` (200 bytes), `/10kb` (10 KB), `/1mb` (1 MB) of pre-generated byte slices, plus `/health` (200 OK, minimal body). The `/health` endpoint is for the LB's health checker and should not be a benchmarked endpoint.

**TLS via env vars**: `TLS_CERT_FILE` and `TLS_KEY_FILE`. If set, `ListenAndServeTLS`; if unset, `ListenAndServe`. Consistent with the existing `SLEEP_MS`/`FAIL_RATE` env var pattern.

**HTTP/2 auto-enabled**: Go's `ListenAndServeTLS` auto-configures HTTP/2 on the server side. The implementer must NOT set `TLSNextProto` to an empty map on the server — that disables HTTP/2. This is the server-side equivalent of the `ForceAttemptHTTP2` client-side gotcha.

### 21. Benchmark tool — vegeta only (ADR-0020)

**wrk does not support HTTP/2.** Since half the benchmark story is HTTP/2 performance, wrk cannot cover it.

Vegeta advantages:
- HTTP/2 support natively.
- Constant-rate load avoids coordinated omission (wrk's open-loop model is susceptible).
- HDR histograms natively — proper latency distribution analysis.
- Single tool for both throughput discovery (rate ramping) and latency profiling (constant rate).

The sprint spec's original S5.T6 (wrk peak-throughput) and S5.T7 (vegeta latency) are rewritten:
- S5.T6 → Vegeta peak-throughput discovery (binary-search the rate ceiling).
- S5.T7 → Vegeta fixed-rate latency profiling at 30/50/70/90% of discovered peak.

Record this tool change in ADR-0020 when the harness is built. Amend MILESTONES.md at the same time.

### 22. Vegeta runs in-compose, not on host

Containerized vegeta eliminates host OS networking variability (macOS Docker Desktop's userspace networking is measurably different from Linux bridge) and the "install vegeta on your host" prerequisite.

The benchmark measures *relative* overhead (LB vs Nginx). Both see identical container-to-container networking overhead, which cancels out in the comparison. `bench/run.sh` invokes `docker compose run vegeta ...`, never a host-installed binary.

### 23. Docker-compose topology

**Services**: `lb` (this project's binary), `nginx` (pinned 1.27.x), `backend1`–`backend4` (dummy backend), `vegeta` (load generator).

**Network**: single flat bridge network. LB and Nginx publish ports for external access. Vegeta, backends are internal. All containers reach each other by service name.

**Rationale**: Network segmentation adds compose complexity for zero benchmark value. Docker's bridge network latency is uniform across containers on the same network.

### 24. Nginx comparison — constrained match, not "each at their best"

Nginx is configured with **matched** algorithm, backend count, and keepalive settings. NOT "Nginx at its best."

**Why not "each at their best"**: If Nginx uses `least_conn` and your LB uses `p2c_ewma`, you're comparing two algorithms AND two implementations simultaneously. A reader cannot attribute the difference. "Each at their best" makes for a bad benchmark, not an honest one.

**The portfolio story with constrained match**: "My roundrobin vs Nginx roundrobin, same backends, same topology. If I'm within 5–15% of Nginx throughput, that's impressive — I wrote a load balancer in a few weeks that's competitive with battle-tested production software." That's the story.

### 25. Algorithm-to-Nginx mapping

| This project | Nginx directive | True equivalent? | Benchmarked head-to-head? |
|---|---|---|---|
| `roundrobin` | default (round-robin) | Yes | Yes |
| `leastconn` | `least_conn` | Yes | Yes |
| `consistent-hash` | `ip_hash` | **No** — Maglev-style consistent hashing with bounded loads vs source-IP mod-N | No — solo only |
| `p2c-ewma` | (none) | No equivalent | No — solo only |

P2C-EWMA's superiority is shown in the project's own algorithm-vs-algorithm comparison (P2C-EWMA vs roundrobin under latency skew), not against Nginx.

### 26. Nginx HTTP/2 in protocol comparison slice

Two Nginx configs:
- Plain HTTP: `listen 80;`
- TLS + HTTP/2: `listen 443 ssl;` + `http2 on;`

**Nginx version note**: `listen ... http2;` directive was deprecated in Nginx 1.25.1 in favor of `http2 on;` as a separate directive. Pin to Nginx 1.27.x and use the correct `http2 on;` syntax for that version. The Nginx version and syntax must be specified explicitly.

All Nginx configs published in the repo with every parameter documented side by side with the LB config.

### 27. Benchmark matrix — 50 runs

**Core (36 runs):**
- Head-to-head: 2 matched algorithms (roundrobin, leastconn) × 3 sizes (200B, 10KB, 1MB) × 2 competitors (LB, Nginx) × 2 load types (throughput, latency) = 24
- Solo: 2 unmatched algorithms (consistent-hash, P2C-EWMA) × 3 sizes × LB-only × 2 load types = 12

**Protocol comparison (12 runs):**
- roundrobin × 3 sizes × 2 competitors × 2 load types × HTTP/1.1 = 12
- (HTTP/2 numbers already captured in the core slice)

**Failure-mode (2 runs):**
- roundrobin × 10KB × LB-only × backend-kill = 1
- roundrobin × 10KB × LB-only × SIGHUP-under-load = 1

### 28. Peak throughput discovery — binary search

Automated, reproducible, produces a single number:

1. Seed rate: 1000 req/s (constant at top of `run.sh`).
2. Run vegeta for 10–15s at each rate. Discard first 3–5s as warmup (avoid cold-start connection establishment artifacts).
3. If p99 < 100ms AND error rate < 1%, double the rate.
4. When a rate fails, binary search between last-good and first-bad.
5. Converge within 500 req/s granularity.
6. Report the peak sustainable rate.

Thresholds (p99 ceiling, error rate ceiling, warmup duration, convergence granularity) are constants at the top of `bench/run.sh`, not flags. Published numbers must use published thresholds. Flags would invite "but I ran it with different thresholds" comparisons that undermine reproducibility.

### 29. Failure-mode benchmark scenarios

**Backend-kill:**
- Steady-state: vegeta at 50% of discovered peak, roundrobin, 10KB, TLS+HTTP/2.
- Event: `docker compose stop backend3` at T+30s.
- Total duration: 60s (30s steady-state, 30s post-event to capture full recovery arc).
- Measurements: time-to-detection (first error to health-check marking unhealthy), error count during detection window, p99 recovery time (returns to within 10% of pre-kill baseline), total error count.
- The health check interval is the dominant factor in detection time — document its setting.

**SIGHUP-under-load:**
- Same steady-state parameters.
- Event: `docker compose exec lb kill -HUP 1` at T+30s with an **unchanged** config file.
- Total duration: 60s.
- Measurements: request drop count (target: **zero**, per Sprint 4 exit criteria), p99 spike magnitude and duration.
- This validates ADR-0015's atomic pointer swap under real load.

Both failure-mode runs are the demo video material — they're the most visually compelling evidence of resilience.

### 30. Benchmark output format

- **Files**: `.txt` text summaries (p50/p99/p99.9, throughput, status codes) + `.hdr` HDR histogram files (small, human-readable, re-plottable).
- **Filenames encode parameters**: e.g., `roundrobin-10kb-lb-throughput.txt`, `roundrobin-10kb-lb-throughput.hdr`.
- **Directory structure mirrors slices**:
  ```
  bench/results/
    core/
      roundrobin-200b-lb-throughput.txt
      roundrobin-200b-lb-throughput.hdr
      roundrobin-200b-nginx-throughput.txt
      ...
    protocol/
      roundrobin-200b-http11-lb-throughput.txt
      ...
    failure/
      roundrobin-10kb-backend-kill.txt
      roundrobin-10kb-sighup.txt
      ...
  ```
- **No raw `.bin` files committed** — large and regenerable from `run.sh`.
- **No PNGs/SVGs in repo** — bloat git history and go stale on re-run. README gets a markdown table.

### 31. Benchmark configs — eight self-contained YAML files

```
bench/configs/
  http11/
    roundrobin.yaml
    leastconn.yaml
    consistent-hash.yaml
    p2c-ewma.yaml
  h2/
    roundrobin.yaml
    leastconn.yaml
    consistent-hash.yaml
    p2c-ewma.yaml
```

Each file is complete, fully self-contained, under 20 lines, and readable in isolation. No templating, no sed, no envsubst. A reviewer can open any single file and understand exactly what that benchmark run does.

**Why 8 files instead of 4 + templating**: DRY is the wrong optimization for benchmark configs. Readability and auditability matter more. The alternative is someone re-running benchmarks six months later and debugging a sed pipeline.

### 32. `bench/run.sh` contract

- Slices: `core`, `protocol`, `failure`, `all` (default).
- `./bench/run.sh all` satisfies Sprint 5 exit criteria ("reproduces all published numbers").
- Each slice prints a summary table to stdout: algorithm, size, competitor, p50, p99, throughput.
- Script is the **single source of truth** for benchmark parameters (rates, durations, warmup periods). Don't scatter those across multiple config files.
- All parameters are constants at the top of the script.

### 33. Ticket execution order

The metric rename must land before T1's tests so new tests use the correct name:

1. **T3-prefactor** — metric rename (`lb_active_connections` → `lb_active_requests`). Single commit, no behavioral change.
2. **T1** — TLS listener: cert script, `tls:` config schema, server construction branching (plain vs TLS), `ReadTimeout: 0` in TLS mode, integration tests.
3. **T2** — h2c support: `h2c: true` config, conditional handler chain, `ReadTimeout: 0` in h2c mode, integration tests for both prior knowledge and Upgrade.
4. **T3-main** — HTTP/2 to backends: `force_http2` and `tls_skip_verify` transport config, `ForceAttemptHTTP2` on transport, `protocol` label on `lb_requests_total`, backend protocol logging, integration tests.
5. **T4** — Benchmark harness: dummy backend extension, compose file, Nginx configs, `bench/run.sh`, benchmark configs, ADR-0020.

**Dependencies**: T1 depends on metric rename being done. T2 depends on T1 (handler chain branching established). T3-main depends on T1 (TLS infrastructure). T4 depends on everything.

### 34. Work-item mapping into existing tickets

| Work item | Ticket | Rationale |
|---|---|---|
| `scripts/generate-cert.sh` | S5.T1 | Can't test TLS without certs; first thing T1 does |
| `ReadTimeout` disable for HTTP/2 | S5.T1 | Server construction is T1's core concern |
| h2c conditional handler chain | S5.T2 | T2's entire scope |
| Metric rename | S5.T3 (first commit) | Precondition for correct HTTP/2 observability |
| `protocol` label on `lb_requests_total` | S5.T3 | HTTP/2 verification metric, natural fit |
| `ForceAttemptHTTP2` + `tls_skip_verify` config | S5.T3 | Backend transport config is T3's scope |
| Dummy backend extension (TLS + response sizes) | S5.T4 | Can't benchmark without correct backends |
| Vegeta container in compose | S5.T4 | Part of benchmark harness |
| `bench/run.sh` with slices | S5.T4 | Part of benchmark harness |
| ADR-0020 (vegeta-over-wrk) | S5.T4 | Record decision when harness is built |

No orphaned prefactor tickets. Each piece has a natural home in an existing ticket.

## Testing Decisions

### What makes a good test

Tests verify **external behavior** through the module's public interface. They assert *what* the system does given specific inputs, not *how* it does it internally. A test should break only when behavior changes, not when the implementation is refactored. Table-driven where the input space is enumerable. `testify/require` for setup assertions (fail fast), `testify/assert` for value checks (see all failures).

### Test seams — all existing, no new seams needed

**1. Config validation** (`internal/config/`):
- Table-driven tests for TLS block parsing, h2c flag parsing, mutual exclusivity rejection.
- Table-driven tests for new transport fields: `force_http2` default to true when omitted, `tls_skip_verify` default to false when omitted.
- `NonBackendChanges` tests: TLS and h2c changes detected as non-backend changes that block reload.
- Prior art: existing `TestLoad` and `TestValidate` table-driven tests.

**2. App.Build** (`internal/app/`):
- Verify correct `http.Server` construction per mode:
  - TLS mode: server has `TLSConfig` set, `ReadTimeout` is 0, `IdleTimeout` from config.
  - h2c mode: server handler is `h2c.NewHandler(...)`, `ReadTimeout` is 0.
  - Plain mode: server has no `TLSConfig`, `ReadTimeout` from config (unchanged behavior).
- Verify transport construction: `ForceAttemptHTTP2: true` when `force_http2` is true and `TLSClientConfig` is set.
- Prior art: existing `app_test.go` integration tests.

**3. Proxy protocol label** (`internal/proxy/`):
- Verify `lb_requests_total` counter includes `protocol` label with correct value for HTTP/1.1, h2, and h2c requests.
- Prior art: existing `proxy_test.go` tests that assert metric labels.

**4. Integration tests** (via `httptest` / `crypto/tls`):
- **TLS handshake + ALPN**: create TLS listener, connect with `crypto/tls.Dial`, assert `ConnectionState().NegotiatedProtocol == "h2"`. Assert `resp.Proto == "HTTP/2.0"`.
- **h2c prior knowledge**: use `http2.Transport` with `AllowHTTP: true` and custom `DialTLSContext` that dials plaintext. Assert `resp.Proto == "HTTP/2.0"`.
- **h2c Upgrade**: send HTTP/1.1 request with `Upgrade: h2c` header. Assert upgrade succeeds.
- **HTTP/2 to backends**: start `httptest.NewTLSServer` as a mock backend, configure LB with `https://` backend URL and `tls_skip_verify: true`. Assert the backend received an HTTP/2 request (`resp.Proto` check on the backend side).
- **HTTP/1.1 backends still work**: `http://` backend URL with HTTP/2 transport settings produces HTTP/1.1 requests to backend.
- Prior art: existing integration tests using `httptest.NewServer`.

**5. Metric rename verification**: all existing tests updated to reference `lb_active_requests` (not `lb_active_connections`). Verify the renamed metric is registered, incremented, and decremented correctly during request lifecycle. This is a find-and-replace in existing test assertions.

### Race detection

All tests must pass with `go test -race`. HTTP/2's multiplexed nature makes race conditions more likely in shared-state paths (metric updates with new protocol label, observer fan-out across streams sharing a TCP connection). The existing `make test-race` target covers this.

## Out of Scope

- **mTLS (mutual TLS / client certificate verification)** — authentication, not transport. Doesn't affect benchmarks or HTTP/2 demonstration. Covered by ADR-0005's "out of scope" boundary.
- **Cert hot-reload** via `tls.Config.GetCertificate` — production feature with its own test surface (file watcher/polling, error handling on corrupt certs, race with in-flight handshakes). Deferred to future ticket. Mention in `what-id-do-differently.md`.
- **Configurable TLS `min_version` / `cipher_suites`** — Go defaults are secure. Extension point documented, not built. Backwards-compatible to add later.
- **Per-backend transport configuration** — would fragment connection pools and break active-request accounting. No benchmark scenario requires mixed protocols across backends.
- **h2c to backends** — requires custom `http2.Transport` + `DialTLSContext` plumbing. No real-world benefit over scheme-driven TLS-based HTTP/2. More code, more complexity, no benchmark value.
- **HTTP/2 server tuning** (`MaxConcurrentStreams`, `MaxReadFrameSize`) — premature without benchmark data from T4.
- **wrk** — does not support HTTP/2. Replaced by vegeta. Rationale in ADR-0020.
- **Nginx "best practice" / unmatched comparison** — conflates algorithm choice with implementation quality. Constrained match only.
- **P2C-EWMA or consistent-hash vs Nginx** — no true Nginx equivalents exist. Solo benchmarks only.
- **Benchmark plots (PNG/SVG) in repo** — bloat git history, go stale. README uses markdown tables.
- **README, `design-decisions.md`, `what-id-do-differently.md`** — Sprint 5 deliverables but separate tickets (S5.T5+), outside T1–T4 scope.
- **S5.T6–T9 (benchmark execution runs)** — this spec covers the harness (T4), not the runs themselves.

## Further Notes

### The `ForceAttemptHTTP2` gotcha deserves emphasis

This is easy to get wrong and produces silent fallback to HTTP/1.1. It is mentioned in Implementation Decision #17 but bears repeating: the moment `TLSClientConfig` is customized (even just setting `InsecureSkipVerify`), Go's automatic HTTP/2 negotiation is silently disabled. The code must explicitly set `ForceAttemptHTTP2: true`. If this is missed, all benchmarks will show HTTP/1.1 to backends with no error, no warning, no logging — just wrong numbers.

### Dummy backend server-side HTTP/2 gotcha

Parallel to the client-side gotcha: if the dummy backend creates a custom `http.Server` with `TLSNextProto` set to an empty map, HTTP/2 is silently disabled server-side. The fix is simple: don't set `TLSNextProto`. Go's `ListenAndServeTLS` auto-configures HTTP/2 when `TLSNextProto` is nil.

### Sprint spec amendments needed

When T4 is implemented:
- Amend MILESTONES.md: S5.T6 (wrk) → vegeta peak-throughput discovery. S5.T7 (vegeta latency) → vegeta fixed-rate latency profiling.
- Write ADR-0020: vegeta-over-wrk rationale (HTTP/2 coverage, coordinated omission avoidance, single-tool consistency).
- Consider ADR-0004 amendment for the `golang.org/x/net` dependency.

### Design-decisions.md notes for later tickets

The grill surfaced several points to capture in `docs/design-decisions.md` (a separate Sprint 5 deliverable):
- HTTP/2's stream-level flow control replaces `ReadTimeout` for slow-client protection; `MaxConcurrentStreams` bounds malicious stream-opening attacks.
- `ForceAttemptHTTP2` must be set explicitly when `TLSClientConfig` is customized — the most common Go HTTP/2 mistake.
- Metric name `active_requests` chosen over `active_connections` for HTTP/2 accuracy.
- Vegeta chosen over wrk for HTTP/2 coverage and coordinated omission avoidance.
- Constrained Nginx comparison methodology: matched algorithm, matched topology, every parameter documented side by side.
- "Each system at its best" produces bad benchmarks because you can't attribute the difference to implementation vs algorithm vs tuning.

### Benchmark failure-mode runs are demo material

The backend-kill and SIGHUP-under-load scenarios are the most visually compelling evidence of resilience patterns working together. Backend-kill shows health-check detection → circuit breaker activation → selector routing around. SIGHUP shows zero-drop reload under load. These should be highlighted in any presentation or README walkthrough.
