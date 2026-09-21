// Package logging configures the process-wide structured logger.
// ragabast logs are line-based key=value pairs by default so
// they remain grep-friendly for humans; an env switch flips the
// same logger to JSON for shippers (journald → Elastic,
// Datadog, Loki, etc.).
//
// The package exists so cmd/main and any test can install the
// same logger without each package re-reading RAGABAST_LOG_FORMAT.
// Init is idempotent: calling twice replaces the previous
// default.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Format identifies the output format of the process logger.
type Format string

// Format values the InitWith / FormatFrom helpers accept. The
// strings are matched case-insensitively; anything unrecognized
// falls back to FormatText so a typo doesn't accidentally
// switch operators to JSON.
const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// envLogFormat is the env var that selects the format.
// Operators who want JSON output set this in their service
// environment.
const envLogFormat = "RAGABAST_LOG_FORMAT"

// FormatFrom inspects the env value and returns the matching
// Format constant, falling back to FormatText for any value
// it doesn't recognize. The comparison is case-insensitive so
// "JSON", "Json", and "json" all work.
func FormatFrom(envValue string) Format {
	switch strings.ToLower(strings.TrimSpace(envValue)) {
	case string(FormatJSON):
		return FormatJSON
	default:
		return FormatText
	}
}

// Init configures slog.Default based on the RAGABAST_LOG_FORMAT
// env var. Returns the configured *slog.Logger for callers
// that want to pass it explicitly rather than relying on the
// global default.
//
// The default level is Info; debug-level logs are dropped.
func Init() *slog.Logger {
	return InitWith(os.Getenv(envLogFormat), os.Stderr)
}

// InitWith is the form that takes the format and the output
// writer explicitly. Useful in tests where env-var
// manipulation is awkward (t.Setenv leaves package-global
// state behind) and a buffer is preferred.
func InitWith(formatEnv string, out io.Writer) *slog.Logger {
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	switch FormatFrom(formatEnv) {
	case FormatJSON:
		handler = slog.NewJSONHandler(out, opts)
	case FormatText:
		handler = slog.NewTextHandler(out, opts)
	default:
		// FormatFrom already normalizes unknown values to
		// FormatText; reaching this branch is defensive.
		handler = slog.NewTextHandler(out, opts)
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}
