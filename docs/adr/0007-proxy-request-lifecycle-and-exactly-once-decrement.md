# ADR-0007: Proxy request lifecycle and exactly-once active-connection decrement

**Status:** Accepted. Amended by ADR-0016 and ADR-0017, which split the error handler into drain, client-gone (499) and transport-failure (502) cases.

## Context

`internal/proxy` wraps `httputil.ReverseProxy`. The lifecycle is:

- `ServeHTTP` calls `Selector.Select` itself, not `Director`. `Director` has no `ResponseWriter`, so it cannot send a 503 when `Select` returns `ErrNoHealthyBackends`.
- After a successful selection, `ServeHTTP` calls `IncActive()` and puts the chosen `*backend.Backend` in the request context. `Director` reads it and sets only `req.URL.Scheme` and `req.URL.Host`.
- Two places decrement `ActiveConns`: a response-body wrapper installed in `ModifyResponse`, and `ErrorHandler` on a round-trip failure.
- Each request emits exactly one "request complete" log line.

This design has two hazards:

1. **Double decrement.** If the body wrapper and `ErrorHandler` both decrement without a guard, one `IncActive()` can get two `DecActive()` calls. `ActiveConns` then goes negative, and `LeastConnections` sees the wrong load.
2. **Status capture.** Since Go 1.21, `ReverseProxy` flushes and hijacks through `http.NewResponseController(rw)`, which unwraps the writer chain. A `ResponseWriter` wrapper must then re-implement `Unwrap`, `Flusher` and `Hijacker`, or streaming breaks.

## Decision

1. **Per-request state lives in the request context.** The unexported `reqState` holds the backend, the status and a `sync.Once`. An unexported context-key type stores it. `ServeHTTP` creates it, and `Director`, `ModifyResponse` and `ErrorHandler` read it. There is no package-global state.
2. **`DecActive` runs only through `reqState.release()`, guarded by `sync.Once`.** The body wrapper's `Close()` and `ErrorHandler` both call `release()`. The counter goes down exactly once, even if the internal order of `ReverseProxy` changes.
3. **The decrement happens in the body wrapper.** `releaseBody` embeds the original `io.ReadCloser`. Its `Close` calls `release` and then closes the body. `Director` and `ModifyResponse` never decrement, because the body can stream. "Request done" means the client finished or abandoned the body.
4. **The proxy captures the status without a `ResponseWriter` wrapper.** `ModifyResponse` records `resp.StatusCode`. `ErrorHandler` records the status of its case. The 503 paths record 503. One deferred call in `ServeHTTP` emits the "request complete" line, so the line also appears on the `http.ErrAbortHandler` panic path. The level is `WARN` for `status >= 500` and `INFO` for other statuses.
5. **`ErrorHandler` logs the transport error on its own line.** The completion-line vocabulary has no `err` field. A separate line keeps the cause without a new field on the completion line.
6. **The logger is `slog.Default()`, captured in `New`.** The `New(reg, sel)` signature takes no logger. `internal/logger` sets the process default.

`Director` sets only the scheme and host. It does not join a path prefix from the backend URL. This is a known limitation.

## Consequences

- `ActiveConns` cannot go down twice or leak on the success, 503, error and abort paths. `LeastConnections` depends on this.
- Streaming, flush and hijack behave exactly as in `ReverseProxy`.
- The completion line comes after the response ends, so `latency_ms` covers the full request.
- A failed round trip emits two lines: the cause and the summary. They answer different questions.
- `ServeHTTP` reads `reqState.status` after `ReverseProxy.ServeHTTP` returns, with no lock. This is safe because `ReverseProxy` handles a request on one goroutine.
- A boolean "already released" flag was not used. It needs the same synchronization as `sync.Once`, and `sync.Once` states "exactly once" directly.
- A decrement only in `ErrorHandler` leaks on normal completion. A decrement only in the body wrapper leaks on transport failure, where there is no body.
- A hand-written proxy was not used. `ReverseProxy` already handles hop-by-hop headers, `X-Forwarded-For`, buffering and flushing.
- `httputil.NewSingleHostReverseProxy` was not used. Its `Director` cannot send a 503.
