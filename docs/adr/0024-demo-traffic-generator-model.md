# ADR-0024: Demo traffic generator: open-loop Poisson, bounded in-flight, honest counters

**Status:** Accepted

## Context

The local demo (ADR-0023) needs realistic traffic from eight client services. Each has its own container IP, so each is its own consistent-hash key, and each sends a Zipf-ranked share of a total rate. Three risks shape how the traffic is made:

- A closed-loop generator (N workers on a queue) sends less when a backend slows. A slow backend would then look like less traffic, not like latency, and the demo exists to show latency.
- A generator with unbounded in-flight requests turns a higher rate into an OOM kill. The benchmark rig learned this (ADR-0021, ADR-0022).
- A generator that reports only what it intended to send lets the presenter claim a rate the LB never got.

## Decision

`demo/traffic-generator` is a stdlib-only Go program with its own small image, in the repository's single module. It reads environment variables at startup and takes runtime control over HTTP.

1. **Arrivals are open-loop Poisson at the client's own rate.** The client computes its share as `total × (1/k^s) / Σ_{i=1..N} (1/i^s)` from `RANK` and `CLIENTS`. Clients do not coordinate. Each inter-arrival time is `ExpFloat64()/rate`, independent of earlier requests. A slow backend therefore shows as latency and in-flight count, never as a lower send rate.
2. **In-flight is bounded, and an arrival that cannot start is dropped.** A semaphore per client (`MAX_INFLIGHT`) bounds concurrency. When an arrival is due and the semaphore is full, it counts as offered and dropped, and is never queued. The counters are:
   - `offered`: arrivals that came due,
   - `sent`: requests dispatched,
   - `dropped`: due while the bound was full (`offered = sent + dropped`),
   - `completed`: a response arrived and was drained,
   - `errors`: the request got no response.
3. **Response bodies are drained and discarded.** Each body is copied to `io.Discard` and closed. The compose file sets `GOMEMLIMIT=256MiB` and `GOGC=50` (the ADR-0021 method).
4. **A control endpoint on a second `net/http` server**, on the internal network only. `GET` returns the settings and all counters. `POST` accepts an optional `total_rate` and/or `target`. It returns `400` for unknown fields, a negative rate or a target outside `TARGET_ALLOWLIST`. A target change applies to the next arrival, and in-flight requests finish normally. The settings are one immutable value behind an `atomic.Pointer`, so each arrival reads a consistent `(rate, target)` pair. In the demo the allowlist holds one target, `http://lb:8080`.
5. **Defaults are named constants in one place.**

   | Setting | Default |
   |---|---|
   | `CLIENTS` | 8 |
   | Zipf exponent `s` | 1.0 |
   | Total rate | 200 req/s (the demo compose sets `TOTAL_RATE=400`) |
   | `MAX_INFLIGHT` | 64 |
   | Response mix | `/200b` 70, `/10kb` 25, `/1mb` 5 |
   | Request-body mix | none 70, 1 KiB 20, 64 KiB 10 |

   A request with a body is a `POST` to the payload path, and the backend discards the body. The compose file may override any setting. The defaults suit a laptop with Docker Desktop defaults.

## Consequences

- Latency is never hidden as lower load. At the demo rate, in-flight counts stay above zero, which least-connections needs.
- A dropped arrival is reported, so a rate that is too high shows on the page.
- The resource bound is structural (a semaphore and drained bodies), not a tuned threshold. The benchmark OOM cannot happen here.
- A rate that is too high shows up as drops, not as served requests. The counters make this explicit.
- No load-balancer code changes and no new dependency. Only the rate and the target change at runtime.
- Rejected alternatives:
  - A closed-loop worker pool (hides slow backends).
  - Unbounded in-flight with a queue (recreates the OOM).
  - Runtime-changeable mixes (the mix is a default).
  - A control API that restarts the client (rate and target must change without a restart).
