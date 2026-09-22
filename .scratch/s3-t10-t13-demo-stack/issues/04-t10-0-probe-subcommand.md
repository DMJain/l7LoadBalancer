# 04: S3.T10.0 — `probe` subcommand and version/commit injection

**What to build:** Two purely additive CLI changes to the main binary that
ticket 05 (the Dockerfile) will consume for its `HEALTHCHECK` directive and
its OCI labels. Nothing container-related lands here — this is `go test`-shaped
work only.

Grammar for the subcommand: `l7lb probe <url>` (positional argument, not a
flag). Behaviour: issue an HTTP GET to the URL with a small timeout
(≈2 seconds); on any 2xx response, exit 0; on any non-2xx or transport error
(connection refused, timeout, DNS failure), exit non-zero. Implementation is
an early branch in `main` that inspects `os.Args[1]` before flag parsing,
executes the probe, and calls `os.Exit`. No third-party CLI framework; no
`cobra`; no subcommand registry beyond this one branch. The doc-comment on
the branch points at ADR-0014 (written by ticket 03) explaining why the
default HEALTHCHECK URL targets `/livez`.

Second addition: two package-level `var version = "dev"` and
`var commit = "unknown"` in `cmd/l7LoadBalancer/main.go`, both injectable via
`go build -ldflags="-X main.version=$VERSION -X main.commit=$COMMIT"`. A
startup `slog.Info("starting", "version", version, "commit", commit)` line
lands at the very top of `main` so both values appear in the JSON log on
every process start, regardless of whether the binary was built with the
ldflags override.

The subcommand is why this ticket precedes the Dockerfile: without it the
Dockerfile's `HEALTHCHECK CMD ["/l7lb", "probe", …]` cannot work in a
distroless base that ships no shell and no `curl`.

**Blocked by:** 03. Waits on ticket 03 so that the doc-comment on the probe
subcommand can point at a real ADR-0014 file, and so the target URL
(`/livez`) exists in the running process the subcommand is testing.

**Status:** done

- [x] Tests written first: table-driven test that stubs `os.Args`, runs the
      probe branch against a `httptest`-hosted server returning a matrix of
      status codes (200, 204, 301, 404, 500, 503), asserts the process exits
      0 on 2xx and non-zero otherwise. A second test asserts a connection
      refusal (`http://127.0.0.1:0`) exits non-zero within the small timeout.
- [x] The probe branch does not consume `flag`-parsed globals — invoking
      `l7lb probe <url>` never triggers `-config`'s default file lookup or
      any other flag side-effect.
- [x] `main.version` and `main.commit` exist as package-level `var`s with
      defaults `"dev"` and `"unknown"`, injectable via ldflags. A separate
      test invokes `go build -ldflags "-X main.version=x.y.z -X main.commit=abc123"`
      in a `t.TempDir()`, runs the produced binary with a stubbed no-op
      subcommand, and asserts the emitted JSON log line contains the
      injected values.
- [x] A `l7lb`-invocation without arguments still runs the load balancer
      exactly as before this ticket — the probe branch does not intercept
      the empty-args case.
- [x] `make test`, `make test-race`, `go vet ./...`, `make fmt` pass.
- [x] Manual verification recorded in the session log: `go build -o /tmp/l7lb ./cmd/l7LoadBalancer`;
      then `make run` in one shell and `/tmp/l7lb probe http://127.0.0.1:8081/livez`
      in another; assert exit 0. Also `/tmp/l7lb probe http://127.0.0.1:1/nope`
      → non-zero exit.
- [x] `PROGRESS.md` entry for this ticket links back to spec decisions
      D16 and D17 for provenance.
