package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubArgs replaces os.Args for the duration of a test and returns a restore
// function, so the probe branch can be exercised exactly as the real binary
// sees it — through os.Args, not a parameter.
func stubArgs(args []string) func() {
	orig := os.Args
	os.Args = args
	return func() { os.Args = orig }
}

// TestProbeCommandStatusMatrix pins the exit-code contract of `l7lb probe
// <url>`: any 2xx response is success (0), every other status is failure
// (non-zero). 301 is included deliberately — the probe must not follow the
// redirect, mirroring the health checker's own CheckRedirect policy.
func TestProbeCommandStatusMatrix(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   int
	}{
		{name: "200 OK", status: http.StatusOK, want: 0},
		{name: "204 No Content", status: http.StatusNoContent, want: 0},
		{name: "301 redirect", status: http.StatusMovedPermanently, want: 1},
		{name: "404 not found", status: http.StatusNotFound, want: 1},
		{name: "500 server error", status: http.StatusInternalServerError, want: 1},
		{name: "503 unavailable", status: http.StatusServiceUnavailable, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			defer stubArgs([]string{"l7lb", "probe", srv.URL})()

			code, handled := probeCommand(os.Args)
			require.True(t, handled, "a probe invocation must be handled")
			assert.Equal(t, tt.want, code)
		})
	}
}

// TestProbeCommandConnectionRefused proves a transport error (connection
// refused) exits non-zero and does so promptly, well inside the client
// timeout, so a Docker HEALTHCHECK cannot hang on a dead listener.
func TestProbeCommandConnectionRefused(t *testing.T) {
	defer stubArgs([]string{"l7lb", "probe", "http://127.0.0.1:0"})()

	start := time.Now()
	code, handled := probeCommand(os.Args)
	elapsed := time.Since(start)

	require.True(t, handled)
	assert.NotZero(t, code, "a refused connection must exit non-zero")
	assert.Less(t, elapsed, probeTimeout, "refusal must return before the probe timeout")
}

// TestProbeCommandRejectsMissingURL pins the usage-error exit code: `l7lb
// probe` with no URL is handled (it is a probe invocation) but reports the
// malformed invocation as a distinct non-2xx-style failure.
func TestProbeCommandRejectsMissingURL(t *testing.T) {
	code, handled := probeCommand([]string{"l7lb", "probe"})
	require.True(t, handled, "`l7lb probe` is a probe invocation")
	assert.Equal(t, 2, code)
}

// TestProbeCommandDoesNotInterceptLoadBalancer proves the branch is a
// subcommand, not a catch-all: an ordinary invocation (no args, or flag-led)
// falls through to the load-balancer startup path untouched.
func TestProbeCommandDoesNotInterceptLoadBalancer(t *testing.T) {
	for _, args := range [][]string{
		{"l7lb"},
		{"l7lb", "-config", "configs/example.yaml"},
		{"l7lb", "some-other-arg"},
	} {
		code, handled := probeCommand(args)
		assert.Falsef(t, handled, "args %v must fall through to the load balancer", args)
		assert.Zerof(t, code, "args %v must not produce an exit code", args)
	}
}

// TestBinaryLogsInjectedVersionAndCommit covers the two build-time facts the
// image relies on (spec D16): -ldflags -X main.version/main.commit reach the
// JSON startup log line, and the probe subcommand runs without ever touching
// flag parsing — the binary is executed from an empty directory with no
// configs/example.yaml, so a fall-through to config.Load would fail the probe
// for the wrong reason.
func TestBinaryLogsInjectedVersionAndCommit(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "l7lb")
	build := exec.Command("go", "build",
		"-ldflags=-X main.version=9.9.9 -X main.commit=deadbeef",
		"-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cmd := exec.Command(bin, "probe", srv.URL)
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "probe against 2xx should exit 0; output: %s", out)

	assert.Contains(t, string(out), `"version":"9.9.9"`)
	assert.Contains(t, string(out), `"commit":"deadbeef"`)
}

// TestBinaryLogsStartupRuntimeFields covers the benchmark methodology's two
// runtime facts (S5.T5.6): the "l7LoadBalancer starting" line reports the
// effective GOMAXPROCS and the Go version the binary was built with. The
// harness reads gomaxprocs to prove CPU pinning took effect (Go derives
// GOMAXPROCS from CPU affinity), and go_version is the only way a distroless
// image can name its toolchain. It runs the built binary with GOMAXPROCS=2 and
// a temp config, reads the startup JSON line, then SIGTERMs the process.
func TestBinaryLogsStartupRuntimeFields(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "l7lb")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := fmt.Sprintf(`listen: "127.0.0.1:0"
metrics:
  listen: "127.0.0.1:0"
health_endpoint:
  listen: "127.0.0.1:0"
backends:
  - name: "backend"
    url: %q
`, backend.URL)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfg), 0o600))

	cmd := exec.Command(bin, "-config", cfgPath)
	cmd.Env = append(os.Environ(), "GOMAXPROCS=2")
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	// If any assertion below fails, the child must not outlive the test.
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	done := make(chan startupResult, 1)
	go func() {
		entry, err := readStartupLogEntry(stdout)
		done <- startupResult{entry: entry, err: err}
	}()

	var entry startupLogEntry
	select {
	case result := <-done:
		require.NoError(t, result.err)
		entry = result.entry
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for the startup log line")
	}

	assert.Equal(t, "l7LoadBalancer starting", entry.Msg)
	assert.Equal(t, 2, entry.GOMAXPROCS)
	assert.Equal(t, runtime.Version(), entry.GoVersion)

	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case <-waitDone:
	case <-time.After(10 * time.Second):
		t.Fatal("process did not exit after SIGTERM")
	}
}

// startupLogEntry is the subset of the "l7LoadBalancer starting" JSON line this
// test asserts on.
type startupLogEntry struct {
	Msg        string `json:"msg"`
	GOMAXPROCS int    `json:"gomaxprocs"`
	GoVersion  string `json:"go_version"`
}

// startupResult carries readStartupLogEntry's outcome off the pipe-reading
// goroutine so the test can bound the wait.
type startupResult struct {
	entry startupLogEntry
	err   error
}

// readStartupLogEntry scans r for the "l7LoadBalancer starting" JSON line and
// decodes it, skipping the earlier "starting" line. It returns an error if the
// process exits or the stream fails before the line appears.
func readStartupLogEntry(r io.Reader) (startupLogEntry, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		var entry startupLogEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Msg == "l7LoadBalancer starting" {
			return entry, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return startupLogEntry{}, err
	}
	return startupLogEntry{}, io.EOF
}
