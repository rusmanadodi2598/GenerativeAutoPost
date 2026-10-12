// Package logging builds the process wide slog logger.
//
// @file      internal/platform/logging/logging.go
// @for       JSON slog in production and text slog in development.
// @uses      log/slog, internal/config
// @reason    One place decides log shape per environment.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     util
// @stability stable
// @since     2026-10-12
package logging

import (
	"log/slog"
	"os"
	"strings"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/config"
)

// New returns a slog logger shaped by APP_ENV and LOG_LEVEL.
func New(cfg config.Config) *slog.Logger {
	level := parseLevel(cfg.LogLevel)
	opts := &slog.HandlerOptions{Level: level}
	if cfg.IsDevelopment() {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

// parseLevel maps LOG_LEVEL text to a slog level, defaulting to info.
func parseLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
