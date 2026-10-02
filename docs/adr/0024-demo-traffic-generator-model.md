# ADR-0024: Demo traffic generator — open-loop Poisson, bounded in-flight, honest counters

- **Status**: Accepted
- **Date**: 2026-10-02
- **Deciders**: Darshan Jain (project owner) + OpenCode (deepseek-v4.1-flash) (S5.T16.2)

## Context

The local live demo (ADR-0023) needs realistic traffic against the four
per-algorithm load balancers: eight client services, each with its own container
IP and therefore its own consistent-hash key, each sending a Zipf-ranked share of
a total rate. The traffic must make the selectors' differences visible while the
viewer watches — which constrains how it is generated:

- A closed-loop generator (N workers pulling from a queue) silently reduces its
  offered load when a backend slows, so a slow backend would look like less
  traffic, not like latency. The demo's whole point is to show latency and a
  selector moving off it.
- A generator with unbounded in-flight requests turns a raised rate into an
  out-of-memory kill, making the demo itself the bottleneck. The benchmark rig
  already learned this (ADR-0021, ADR-0022).
- A generator that reports only what it *intended* to send lets the owner narrate
  a rate the load balancer never received.

The behaviour, control surface and resource bounds are specified in
`.scratch/s5-t16-t17-local-demo/spec.md` (*Traffic generator (S5.T16.2)*) and
ADR-0023. This ADR records the model those documents leave to the implementation:
the scheduling law, what "bound" means when arrivals outrun it, the concrete
defaults, and the counter semantics.

## Decision

A new stdlib-only Go program, `demo/traffic-generator`, shipped as its own small
image in the repository's single module (the dummy-backend precedent). It is
configured by environment at startup and controlled at runtime over HTTP.

1. **Arrivals are open-loop Poisson at the client's own rate.** The client
   computes its own share `total × (1/k^s) / Σ_{i=1..N} (1/i^s)` from `RANK` and
   `CLIENTS`; there is no coordination between clients. It schedules an arrival by
   drawing an exponential inter-arrival time at that rate (`ExpFloat64()/rate`),
   independent of whether earlier requests have finished. A slow backend
   therefore shows up as latency and in-flight count, never as a quietly reduced
   send rate.

2. **In-flight is bounded, and an arrival that cannot start is counted as
   dropped.** A fixed semaphore per client (`MAX_INFLIGHT`) bounds concurrency.
   When an arrival is due and the semaphore is full, it is counted as *offered*
   and *dropped* and never queued. The counters are: `offered` (arrivals that
   came due), `sent` (requests dispatched), `dropped` (due while the bound was
   full; `offered = sent + dropped`), `completed` (a response arrived and was
   drained), and `errors` (the request failed to reach a response). The control
   endpoint reports all of them, so a rate claim is checkable against what was
   actually sent.

3. **Response bodies are drained and discarded.** Every response is copied to
   `io.Discard` and closed, so eight clients cannot retain a live demo's worth of
   response bodies. The compose file sets `GOMEMLIMIT`/`GOGC` (ADR-0021's
   mechanism) and the program documents that it expects them.

4. **The control endpoint is a second `net/http` server on the client's own
   port** (internal network only). `GET` returns the current settings and every
   counter; `POST` accepts an optional `total_rate` and/or `target`, rejects
   unknown fields, a negative rate and any target outside the configured
   allowlist with `400`. A target change applies to the **next** arrival; requests
   already in flight to the old target complete normally. Settings are one
   immutable value behind an `atomic.Pointer`, so an arrival reads one consistent
   `(rate, target)` pair.

5. **Defaults are named constants in one place.** `CLIENTS=8`, Zipf exponent
   `s=1.0`, total rate `200` req/s, `MAX_INFLIGHT=64`; the response-size mix is
   mostly `/200b` (70) and `/10kb` (25) with a small `/1mb` (5) share, and the
   request-body mix is none (70), small 1 KiB (20), medium 64 KiB (10), sent as a
   discarded `POST` to the payload paths (S5.T16.1). The compose stack may
   override all of them; the constants are the demo's starting point, sized for a
   laptop with Docker Desktop defaults.

## Consequences

- Positive: latency is never hidden as reduced load — the demo shows what the
  viewer is meant to see. In-flight counts stay above zero at the demo rate,
  which least-connections needs to balance.
- Positive: a dropped arrival is reported, not silently absorbed, so an
  over-high rate is visible on the page rather than narrated as delivered.
- Positive: the resource bound is structural (a semaphore and a drained body),
  not a tuned threshold; the same class of OOM that sank the benchmark 1 MB runs
  cannot recur here.
- Negative: an arbitrary rate can be rejected as dropped rather than served. That
  is the honest reading, and the dropped counter makes it explicit.
- Neutral: no load-balancer code changes and no new dependency. No timing-config
  reload: only the rate and target are runtime-mutable, matching the spec.

## Alternatives considered

- **Closed-loop generator (fixed worker pool).** Rejected: it reduces offered
  load when a backend slows, so a slow backend looks like less traffic and the
  selector's reaction is not what the viewer is told to look for. The demo needs
  open-loop, and the in-flight bound is what keeps open-loop safe.
- **Unbounded in-flight with a queue.** Rejected: it recreates the benchmark
  OOM and makes latency under load unreadable. The bound plus a dropped counter
  is the ADR-0021/0022 answer applied to the demo.
- **Runtime-mutable weights and mix.** Rejected for this ticket: the spec fixes
  only the rate and target as runtime controls; the mix is a demo default.
- **A closed control API that restarts the client on change.** Rejected: the
  spec requires rate and target changes to apply without a restart, and to
  leave in-flight requests to the old target alone.
