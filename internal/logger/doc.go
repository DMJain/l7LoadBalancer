// Package logger configures the application's log/slog structured logger.
//
// Canonical structured field names. All logging across this project MUST
// use these names so log lines stay grep-able and consistent:
//
//	backend      - name of the backend a log line concerns
//	method       - HTTP method
//	status       - HTTP response status code
//	latency_ms   - request latency in milliseconds
//	remote_addr  - client remote address
//	path         - request path
//
// See docs/design/sprint-1-contracts.md "Log field vocabulary" for an
// example log line per event type.
package logger
