// Package config parses raw environment values into typed settings.
//
// @file      internal/config/env.go
// @for       Per-variable parsing helpers used while loading the config.
// @uses      os, strconv, strings
// @reason    Every os.Getenv call stays in this package, so Load stays a pure mapping.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     config
// @stability stable
// @since     2026-10-16
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// getenv reads one variable; only this file touches os.Getenv.
func getenv(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

// intOr parses an optional integer env, falling back to def.
func intOr(key string, def int) (int, error) {
	raw := getenv(key)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parsing %s: %w", key, err)
	}
	return n, nil
}

// durationOr parses an optional duration env, falling back to def.
func durationOr(key string, def time.Duration) (time.Duration, error) {
	raw := getenv(key)
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parsing %s: %w", key, err)
	}
	return d, nil
}

// int64Or parses an optional 64-bit integer env, falling back to def.
func int64Or(key string, def int64) (int64, error) {
	raw := getenv(key)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing %s: %w", key, err)
	}
	return n, nil
}

// listOr splits a comma joined env into trimmed entries, dropping empties.
func listOr(key string) []string {
	raw := getenv(key)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// boolOr reads an optional boolean env, defaulting to false.
func boolOr(key string) bool {
	return strings.EqualFold(getenv(key), "true")
}
