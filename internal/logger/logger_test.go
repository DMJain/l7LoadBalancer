package logger

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything fn wrote. logger.New binds os.Stdout when it builds its handler
// and its frozen New(level) signature takes no writer, so swapping the
// package-level *os.File is the only seam available to observe its output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)

	original := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = original }()

	fn()

	require.NoError(t, w.Close())
	os.Stdout = original

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	return string(out)
}

// parseRecords decodes newline-delimited JSON log output into one map per
// record, failing the test on any malformed line. An empty output yields nil.
func parseRecords(t *testing.T, out string) []map[string]any {
	t.Helper()

	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil
	}

	var records []map[string]any
	for _, line := range strings.Split(trimmed, "\n") {
		var record map[string]any
		require.NoErrorf(t, json.Unmarshal([]byte(line), &record), "malformed JSON log line: %q", line)
		records = append(records, record)
	}
	return records
}

func TestNewEmitsAtConfiguredLevelAndAbove(t *testing.T) {
	levels := []struct {
		level slog.Level
		msg   string
	}{
		{slog.LevelDebug, "debug-record"},
		{slog.LevelInfo, "info-record"},
		{slog.LevelWarn, "warn-record"},
		{slog.LevelError, "error-record"},
	}

	for _, configured := range levels {
		t.Run(configured.level.String(), func(t *testing.T) {
			out := captureStdout(t, func() {
				log := New(configured.level)
				for _, rec := range levels {
					log.Log(context.Background(), rec.level, rec.msg)
				}
			})

			var want []string
			for _, rec := range levels {
				if rec.level >= configured.level {
					want = append(want, rec.msg)
				}
			}

			records := parseRecords(t, out)
			require.Lenf(t, records, len(want), "output was: %q", out)

			var got []string
			for _, record := range records {
				msg, ok := record["msg"].(string)
				require.Truef(t, ok, "record has no string msg: %v", record)
				got = append(got, msg)
			}
			assert.Equal(t, want, got)
		})
	}
}

func TestNewWritesWellFormedJSON(t *testing.T) {
	t.Run("base record has exactly time, level, and msg", func(t *testing.T) {
		out := captureStdout(t, func() {
			New(slog.LevelInfo).Info("plain record")
		})

		records := parseRecords(t, out)
		require.Len(t, records, 1)
		require.Len(t, records[0], 3)
		for _, key := range []string{"time", "level", "msg"} {
			assert.Contains(t, records[0], key)
		}
	})

	t.Run("attributes become top-level keys", func(t *testing.T) {
		out := captureStdout(t, func() {
			New(slog.LevelInfo).Info("request complete", "backend", "backend-a", "status", 200)
		})

		records := parseRecords(t, out)
		require.Len(t, records, 1)
		record := records[0]

		assert.IsType(t, "", record["time"])
		assert.Equal(t, "INFO", record["level"])
		assert.Equal(t, "request complete", record["msg"])
		assert.Equal(t, "backend-a", record["backend"])
		assert.Equal(t, float64(200), record["status"])
	})
}
