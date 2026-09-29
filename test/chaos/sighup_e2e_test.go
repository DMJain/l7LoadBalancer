package chaos_test

import (
	"bytes"
	"flag"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/DMJain/l7LoadBalancer/internal/config"
)

var e2eEnabled = flag.Bool("e2e", false,
	"run the S4.T14 SIGHUP e2e zero-drop test (skipped without this flag; see `make e2e`)")

const (
	e2eInFlightDeadline = 30 * time.Second
	e2eDrainWindow      = time.Minute
	e2eProbeCount       = 20
	e2eTotal            = 1000
)

// TestChaosSighupReloadZeroDrop1000 is the OS-boundary sibling of
// TestChaosReloadDrainExitCriterion1000: it builds the real binary, spawns it,
// holds 1000 requests in flight through gated counting backends, rewrites the
// config file, sends a real SIGHUP, and proves zero drops — plus that live
// traffic shifts to the added backend and never touches the removed one.
//
// Division of labor (story 13): the in-process test keeps the registry-view
// assertions (instance identity, EWMA/circuit survival); this e2e asserts
// externally visible outcomes only — HTTP statuses, per-backend request
// counts, process exit status. Neither duplicates the other.
func TestChaosSighupReloadZeroDrop1000(t *testing.T) {
	if !*e2eEnabled {
		t.Skip("e2e test is flag-gated; run `make e2e`")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no Go toolchain on PATH; cannot build the binary under test")
	}

	raiseFileDescriptorLimit(t)

	fbs := newFlippableBackends(t, 2)
	a, b := fbs[0], fbs[1]

	listen := freeLoopbackAddr(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	writeE2EConfig(t, configPath, listen, fbs)

	binPath := filepath.Join(dir, "l7lb")
	buildBinary(t, goTool, binPath)

	lbURL := "http://" + listen + "/"

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(binPath, "-config", configPath)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Start()
	require.NoError(t, err)

	shutdown := false
	t.Cleanup(func() {
		if !shutdown {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("binary stdout:\n%s", stdout.String())
			t.Logf("binary stderr:\n%s", stderr.String())
		}
	})

	waitForListener(t, lbURL)

	release := make(chan struct{})
	entered := make(chan string, 4)
	a.ServeGated(entered, release)
	b.ServeGated(entered, release)

	baseline := a.RequestCount() + b.RequestCount()

	client := &http.Client{Timeout: 2 * e2eInFlightDeadline}
	results := make(chan requestResult, e2eTotal)
	for i := 0; i < e2eTotal; i++ {
		go func() {
			status, err := e2eGet(client, lbURL)
			results <- requestResult{status: status, err: err}
		}()
	}

	require.Eventually(t, func() bool {
		return a.RequestCount()+b.RequestCount() == baseline+e2eTotal
	}, e2eInFlightDeadline, 10*time.Millisecond, "all %d requests must be held open", e2eTotal)
	require.Positive(t, a.RequestCount(), "some requests must be in flight on the unchanged backend")
	require.Positive(t, b.RequestCount(), "some requests must be in flight on the removed backend")

	bPre := b.RequestCount()

	c := newFlippableBackend(t, "backend-c")
	writeE2EConfig(t, configPath, listen, []*flippableBackend{a, c})

	require.NoError(t, cmd.Process.Signal(syscall.SIGHUP))

	require.Eventually(t, func() bool {
		return c.RequestCount() >= 1
	}, e2eInFlightDeadline, 10*time.Millisecond, "the added backend must be admitted by its first probe")
	cBase := c.RequestCount()

	probeResults := make(chan requestResult, e2eProbeCount)
	for i := 0; i < e2eProbeCount; i++ {
		go func() {
			status, err := e2eGet(client, lbURL)
			probeResults <- requestResult{status: status, err: err}
		}()
	}

	require.Eventually(t, func() bool {
		return c.RequestCount() > cBase
	}, e2eInFlightDeadline, 10*time.Millisecond, "live traffic must shift to the added backend")
	require.Equal(t, bPre, b.RequestCount(), "the removed backend's counter must freeze")

	close(release)
	for i := 0; i < e2eTotal; i++ {
		select {
		case r := <-results:
			require.NoError(t, r.err, "in-flight request %d must complete", i)
			require.Equal(t, http.StatusOK, r.status, "every in-flight request must survive the reload")
		case <-time.After(e2eInFlightDeadline):
			t.Fatalf("only %d/%d requests completed", i, e2eTotal)
		}
	}

	for i := 0; i < e2eProbeCount; i++ {
		select {
		case r := <-probeResults:
			require.NoError(t, r.err, "probe %d must complete", i)
			require.Equal(t, http.StatusOK, r.status, "probe %d must succeed", i)
		case <-time.After(e2eInFlightDeadline):
			t.Fatalf("only %d/%d probes completed", i, e2eProbeCount)
		}
	}

	require.Equal(t, bPre, b.RequestCount(), "the removed backend must serve no further requests")
	require.NoError(t, cmd.Process.Signal(syscall.Signal(0)), "the process must still be alive")

	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case err := <-waitDone:
		require.NoError(t, err, "clean shutdown must exit 0")
	case <-time.After(10 * time.Second):
		t.Fatal("process did not exit after SIGTERM")
	}
	shutdown = true
}

type requestResult struct {
	status int
	err    error
}

func e2eGet(client *http.Client, url string) (int, error) {
	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func writeE2EConfig(t *testing.T, path, listen string, fbs []*flippableBackend) {
	t.Helper()
	probeInterval := time.Hour
	probeTimeout := time.Second
	cooldown := time.Hour
	drainWindow := e2eDrainWindow
	readTimeout := config.DefaultReadTimeout
	idleTimeout := config.DefaultIdleTimeout
	dialTimeout := config.DefaultDialTimeout
	responseHeaderTimeout := config.DefaultResponseHeaderTimeout
	maxIdleConnsPerHost := config.DefaultMaxIdleConnsPerHost
	idleConnTimeout := config.DefaultIdleConnTimeout
	metricsListen := ":0"
	healthListen := ":0"
	cfg := &config.Config{
		Listen:    listen,
		Algorithm: config.AlgorithmRoundRobin,
		Health: config.HealthConfig{
			ProbeInterval: &probeInterval,
			ProbeTimeout:  &probeTimeout,
		},
		Circuit:        config.CircuitConfig{Cooldown: &cooldown},
		Metrics:        config.MetricsConfig{Listen: &metricsListen},
		HealthEndpoint: config.HealthEndpointConfig{Listen: &healthListen},
		Reload:         config.ReloadConfig{DrainWindow: &drainWindow},
		Server:         config.ServerConfig{ReadTimeout: &readTimeout, IdleTimeout: &idleTimeout},
		Transport: config.TransportConfig{
			DialTimeout:           &dialTimeout,
			ResponseHeaderTimeout: &responseHeaderTimeout,
			MaxIdleConnsPerHost:   &maxIdleConnsPerHost,
			IdleConnTimeout:       &idleConnTimeout,
		},
		Backends: backendConfigs(fbs),
	}
	require.NoError(t, cfg.Validate())

	data, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	tmp, err := os.CreateTemp(filepath.Dir(path), "*.tmp")
	require.NoError(t, err)
	defer tmp.Close()
	_, err = tmp.Write(data)
	require.NoError(t, err)
	require.NoError(t, tmp.Close())
	require.NoError(t, os.Rename(tmp.Name(), path))
}

func buildBinary(t *testing.T, goTool, binPath string) {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	moduleRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	out, err := exec.Command(goTool, "build", "-C", moduleRoot, "-o", binPath, "./cmd/l7LoadBalancer").CombinedOutput()
	require.NoError(t, err, "build failed: %s", out)
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

func waitForListener(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.After(e2eInFlightDeadline)
	for {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		select {
		case <-deadline:
			t.Fatalf("listener at %s did not answer within %s", url, e2eInFlightDeadline)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func raiseFileDescriptorLimit(t *testing.T) {
	t.Helper()
	var rlim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rlim); err != nil {
		t.Logf("could not read RLIMIT_NOFILE: %v", err)
		return
	}
	rlim.Cur = rlim.Max
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rlim); err != nil {
		t.Logf("could not raise RLIMIT_NOFILE: %v", err)
	}
}
