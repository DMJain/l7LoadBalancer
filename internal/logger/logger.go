package logger

import (
	"log/slog"
	"os"
)

// New returns a JSON slog.Logger writing to stdout at the given level.
func New(level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}
