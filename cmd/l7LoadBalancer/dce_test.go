package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNaiveConsistentHashNotLinked guards the invariant that the deliberately
// unwired naiveConsistentHash comparator never reaches the shipped binary:
// nothing in non-test code references it, so gc/linker dead-code-eliminates
// it. The type is kept as normal (not test-only) source because the Sprint 2
// spec and ADR-0008 treat it as a real package Selector; this test makes the
// "it ships nothing" claim a checked invariant rather than a remembered fact.
//
// This asserts a negative about gc/linker DCE behavior, not a language
// guarantee. If it fails after a Go toolchain upgrade, first check whether the
// linker changed emission rules for blank-identifier interface assertions
// before assuming a code regression. See ADR-0008.
func TestNaiveConsistentHashNotLinked(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "l7LoadBalancer")

	cmd := exec.Command("go", "build", "-o", bin, ".")
	// Pin the target so DCE behavior is identical on every dev machine: a
	// darwin/arm64 binary and a linux/amd64 binary must not disagree about
	// whether this type survives. Strip any inherited GOOS/GOARCH first so
	// our values are the ones the go command sees.
	cmd.Env = append(withoutEnv("GOOS", "GOARCH"), "GOOS=linux", "GOARCH=amd64")
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "go build failed: %s", out)

	data, err := os.ReadFile(bin)
	require.NoError(t, err)
	binary := string(data)

	// Positive control: a live selector is present by name, so the binary
	// retains function names and the negative assertion below is meaningful
	// rather than trivially true of a stripped binary.
	require.Contains(t, binary, "RoundRobin",
		"binary carries no function names; the negative assertion below would be vacuous")

	require.NotContains(t, binary, "naiveConsistentHash",
		"naiveConsistentHash is linked into the production binary; something now references it")
}

// withoutEnv returns os.Environ() minus the named variables, in the form
// cmd.Env expects.
func withoutEnv(names ...string) []string {
	env := os.Environ()
	kept := env[:0]
	for _, kv := range env {
		drop := false
		for _, name := range names {
			if strings.HasPrefix(kv, name+"=") {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, kv)
		}
	}
	return kept
}
