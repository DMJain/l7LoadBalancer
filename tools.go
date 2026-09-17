//go:build tools

// Package tools pins build-time-only dependencies so `go mod tidy` retains
// them before the packages that import them exist. This file is never
// compiled into the binary (the `tools` build tag is never set).
package tools

import (
	_ "github.com/stretchr/testify/require"
	_ "gopkg.in/yaml.v3"
)
