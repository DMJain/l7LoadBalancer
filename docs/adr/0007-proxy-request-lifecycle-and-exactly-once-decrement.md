# ADR-0007: Proxy request lifecycle and exactly-once active-connection decrement

- **Status**: Accepted
- **Date**: 2026-09-18
- **Deciders**: Darshan Jain (project owner) + opencode agent (S1.T6)

## Context

S1.T6 implements `internal/proxy` around `httputil.ReverseProxy`. The
ticket and the frozen S1.T0.5 contract prescribe a specific lifecycle:

- `ServeHTTP` calls `Selector.Select` itself (not `Director`) so it can
  short-circuit a 503 when `Select` returns `ErrNoHealthyBackends` —
  `Director` has no `ResponseWriter` and cannot write a response.
- On success `ServeHTTP` calls `IncActive()` and carries the chosen
  `*backend.Backend` in the request context; `Director` reads it back and
  sets only `req.URL.Scheme`/`Host`.
- `ActiveConns` is decremented by a response-body wrapper's `Close()`,
  installed in `ModifyResponse`, **and** by `ErrorHandler` on round-trip
  failure.
- Exactly one "request complete" structured log line is emitted per
  request on every path, with the frozen field vocabulary.

Two hazards fall out of that prescription and are not resolved by any
existing ADR:

1. **Double decrement.** `ReverseProxy` calls `ErrorHandler` on a
   transport error (no body wrapper installed) and also, in some Go
   versions/paths, after a body wrapper exists. If both the wrapper's
   `Close()` and `ErrorHandler` decrement unconditionally, a single
   `IncActive()` can be matched by two `DecActive()` calls, driving
   `ActiveConns` negative and corrupting `LeastConnections`' view of load.
   The ticket's own wording ("Close() decrements exactly once";
   "ErrorHandler also decrements") makes this a live concern.
2. **Status capture for logging** without disturbing `ReverseProxy`.
   Since Go 1.21 `ReverseProxy` drives flushing/hijacking through
   `http.NewResponseController(rw)`, which unwraps the writer chain.
   Wrapping the `ResponseWriter` to record the status code would require
   re-implementing `Unwrap`/`Flusher`/`Hijacker` passthrough or regress
   streaming responses. Status is already available as
   `resp.StatusCode` in `ModifyResponse`.

## Decision

1. **A per-request state object carried in the request context.** Define
   an unexported `reqState{backend *backend.Backend; status int; once
   sync.Once}` and an unexported context-key type. `ServeHTTP` creates the
   state, computes `IncActive()`, attaches it with
   `context.WithValue`/`r.WithContext`, and hands the request to
   `ReverseProxy`. `Director`, `ModifyResponse`, and `ErrorHandler`
   retrieve it through the same unexported key — no package-global state,
   no per-request allocation shared across requests.

2. **`DecActive` goes through `reqState.release()`, guarded by
   `sync.Once`.** Both the body wrapper's `Close()` and `ErrorHandler`
   call `release()`, so the backend's counter is decremented exactly once
   even though the ticket mandates both triggers fire. This is the direct
   expression of "exactly once" and is robust if `ReverseProxy`'s internal
   ordering changes.

3. **The decrement is installed in `ModifyResponse` as a body wrapper**
   (`releaseBody` embeds the original `io.ReadCloser`; its `Close` calls
   `release` then closes the underlying body). `Director` never
   decrements, and `ModifyResponse` never decrements directly — the
   response body may be streamed, and "request done" must mean the client
   has finished consuming (or abandoned) it.

4. **Status is captured without wrapping the `ResponseWriter`.**
   `ModifyResponse` records `resp.StatusCode`; `ErrorHandler` records 502;
   the 503 short-circuit records 503. A single deferred call in
   `ServeHTTP` emits the "request complete" line, so it also runs on the
   `http.ErrAbortHandler` panic path. `WARN` for `status >= 500`, `INFO`
   otherwise. This leaves `ReverseProxy`'s `ResponseController` unwrapping
   (flush, hijack) untouched.

5. **`ErrorHandler` logs the transport error at WARN before responding
   502**, in addition to the one "request complete" line. The cause is
   kept as a separate record because the frozen field vocabulary has no
   `err` field, and folding it into the completion line would either drop
   the cause or invent a field.

6. **The logger is `slog.Default()` captured in `New`.** The frozen
   `New(reg, sel)` signature does not take a logger, and S1.T7 wires the
   process logger via `internal/logger`, so `proxy` reads the default
   rather than growing a constructor parameter or a package global of its
   own.

`Director` sets only scheme/host, per the frozen contract. Backend URL
path prefixes are therefore not joined in Sprint 1; that is recorded as a
known limitation, not implemented here.

## Consequences

- Positive: `ActiveConns` cannot be double-decremented or leaked across
  the success, 503, 502, and abort paths — the invariant
  `LeastConnections` depends on.
- Positive: no `ResponseWriter` wrapper, so streaming/flush/hijack
  behavior is exactly `ReverseProxy`'s.
- Positive: one "request complete" line per request on every path,
  emitted after the response completes so `latency_ms` covers the full
  round trip.
- Negative: the 502 path emits two log lines (the transport-error cause
  and the request-complete summary). Accepted: they answer different
  questions and only the summary is per-request.
- Negative: `reqState` carries mutable fields (`status`) read after
  `ReverseProxy.ServeHTTP` returns. Safe because `ReverseProxy` handles a
  request synchronously on one goroutine, but the safety argument is
  implicit rather than enforced by a lock. Documented here.
- Neutral: backend URL path prefixes are ignored by `Director` in Sprint
  1. No acceptance criterion requires them; revisit if a deployment needs
  a prefix.

## Alternatives considered

- **Wrap the `ResponseWriter` to capture the status code**: rejected —
  would need `Unwrap`/`Flusher`/`Hijacker` passthrough to avoid regressing
  streaming and WebSocket upgrades; `resp.StatusCode` already gives the
  status without touching the writer.
- **A boolean "already wrapped" flag on `reqState` instead of
  `sync.Once`**: rejected — `sync.Once` is the direct, concurrency-safe
  expression of "exactly once"; a flag would need the same synchronization
  and relies on knowing which trigger runs first.
- **Decrement only in `ErrorHandler`**: rejected — leaks on the normal
  completion path, where `ErrorHandler` is never called.
- **Decrement only in the body wrapper's `Close()`**: rejected — leaks on
  transport failure, where no body wrapper is installed.
- **Hand-roll the whole proxy instead of `httputil.ReverseProxy`**:
  rejected by the frozen architecture (AGENTS.md / ADR-0002) — hop-by-hop
  header stripping, `X-Forwarded-For`, buffering, and flushing are already
  handled, and the interesting work is the selection layer.
- **`httputil.NewSingleHostReverseProxy` with per-request mutation**:
  rejected — its `Director` cannot express the 503 short-circuit, and the
  body-wrapper decrement has to be attached separately anyway.
