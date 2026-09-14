// Package obs holds observability wiring: structured logging (slog), metrics,
// tracing. P0-03 provides the logger; metrics/tracing land in later phases.
package obs

import (
	"log/slog"
	"os"
	"strings"
)

// NewLogger returns a JSON slog logger writing to stdout. Level comes from
// LOG_LEVEL (debug|info|warn|error); unknown values fall back to info.
func NewLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(handler)
}
