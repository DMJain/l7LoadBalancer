# ADR-0016: Drain lifecycle: retired-context join, two-phase drain, and cancel-at-window

**Status:** Accepted

## Context

A reload marks removed backends before the snapshot swap but leaves their in-flight requests alone (ADR-0015). The goal is zero dropped requests on a reload with 1000 in-flight requests. So the drain must let all requests finish in the normal case. But a stuck backend must not hold a request forever.

The request path needs a per-backend signal that cancels only that backend's in-flight requests. It must join each outbound request without a goroutine, a mutex or a registry entry per request.

## Decision

1. **`reload.drain_window` is configuration.** It is a pointer duration in a top-level `reload:` block. The default is 30s. Zero or negative is rejected at load time. The pointer separates "key absent" (use the default) from "key set to zero" (reject). It is not reloadable, because a running drain's timer is already armed (ADR-0015). `configs/example.yaml` documents it.

2. **Each `Backend` owns a retired context.** It is created at construction, cancelled only by `Backend.Retire()`, and read through `Backend.RetiredContext()`. `Retire` is idempotent and cancels with the cause `backend.ErrDrainWindowExpired`. For a `Backend` built from a bare struct literal, the accessor returns `context.Background()` and `Retire` does nothing. Every production instance comes from `newBackend` and has a real context.

   `ErrDrainWindowExpired` is exported, but it is not a branchable error. The proxy compares it with `context.Cause` to choose the `window_expired` reason. It must be exported to cross from `backend` to `proxy`. `balancer.ErrNoHealthyBackends` stays the only branchable sentinel.

   A context is used because it composes with the client's request context, and `context.AfterFunc` registers a callback with no goroutine while the context is live. A plain done channel would need a goroutine per request.

3. **The proxy joins the two contexts.** `ServeHTTP` makes the outbound context with `context.WithCancelCause`. It registers a `context.AfterFunc` on the backend's retired context, which cancels the outbound context with the retirement cause. Both the cancel and the `AfterFunc` stop function run when the request completes. The stop matters: without it, a long-lived backend would keep one dead callback per completed request. The cause lets the proxy tell a drain cancellation from a client cancellation (`context.Canceled`) or a transport failure.

4. **A drain cancellation before the response headers** fails the request with 502, releases its active-request slot, and logs one WARN line with reason `window_expired`. It goes through `httputil.ReverseProxy`'s `ErrorHandler` like a transport failure.

5. **Observers are not called for a drain cancellation.** Only removed backends are retired, and the proxy already skips the observer fan-out for removed backends (ADR-0015). The check is `IsRemoved()`, not the cause. So a drain cancellation is no backend's failure, with no new rule.

6. **A drain cancellation after the response headers** truncates the body. `ModifyResponse` already ran and recorded a success. The success stays, and no second outcome is recorded. The slot releases through the body wrapper's `Close`. A backend that did answer is not counted as failed, and the truncation is the visible cost.

7. **Reload starts one drain goroutine per removed backend.**
   - Phase one: wait until `ActiveConns` is zero (polled every 100ms) or the window timer fires. On idle, delete the backend's active-request series only if no current backend has the same name, log `backend_drained` with reason `idle`, and exit.
   - Phase two, on window expiry: call `Retire()`, wait until `ActiveConns` is zero, delete the series under the same rule, and log `backend_drained` with reason `window_expired` and the number of cancelled requests.

   Polling keeps the request path free of drain work. `DecActive` stays one atomic add.

8. **Drain goroutines exit at once on process shutdown**, with no cleanup. The server shutdown grace period covers in-flight requests.

9. **A drain is tied to the removed `*Backend` instance, not its name.** Adding the same identity again makes a new instance (ADR-0015), so a later reload neither revives nor ends the drain. This is why the series is deleted only when no current backend has the name.

## Consequences

- Each request pays one `context.WithCancelCause`, one `context.AfterFunc` and two deferred calls. There is no goroutine, mutex or registry entry per request.
- A drain cancellation reuses the existing error path and the removed-backend rule, so "not a backend failure" needs no special case.
- The reason comes from the cancellation cause, not error text, so it is reliable.
- `Backend` grows `Retire`, `RetiredContext` and `ErrDrainWindowExpired`.
- After the headers, the client sees a truncated body, not a clean error. Buffering every response to avoid this would break streaming (ADR-0007).
- A test fixture that needs cancellation must build its backend through `NewRegistry`.
- Rejected alternatives:
  - A goroutine per request.
  - A mutex-guarded registry of cancel functions.
  - A `DecActive` hook to signal drain completion (it adds work to every request).
