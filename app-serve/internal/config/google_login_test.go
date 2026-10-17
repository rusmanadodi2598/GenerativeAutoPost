// Package config_test covers the operator google login config keys.
//
// @file      internal/config/google_login_test.go
// @for       Fail-fast tests for the Google OIDC and operator allowlist keys.
// @uses      testing, strings, internal/config
// @reason    A missing or malformed login key must stop boot, not surface on the first sign in.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     config
// @stability stable
// @since     2026-10-17
package config_test

import (
	"strings"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/config"
)

// TestLoad_GoogleLoginKeysRequired proves each operator login key fails the
// boot when absent, and the error names the key an operator has to set.
func TestLoad_GoogleLoginKeysRequired(t *testing.T) {
	keys := []string{
		"GOOGLE_CLIENT_ID",
		"GOOGLE_CLIENT_SECRET",
		"GOOGLE_REDIRECT_URI",
		"OPERATOR_EMAIL",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			env := wiringEnv()
			delete(env, key)
			withEnv(t, env)
			_, err := config.Load()
			if err == nil {
				t.Fatalf("Load accepted a missing %s", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Fatalf("Load error %q does not name %s", err, key)
			}
		})
	}
}

// TestLoad_OperatorEmailMustBeAnEmail rejects an allowlist entry that is not an
// email before the process serves a request.
func TestLoad_OperatorEmailMustBeAnEmail(t *testing.T) {
	env := wiringEnv()
	env["OPERATOR_EMAIL"] = "not-an-email"
	withEnv(t, env)
	if _, err := config.Load(); err == nil {
		t.Fatal("Load accepted a malformed OPERATOR_EMAIL")
	}
}

// TestLoad_AppUIBaseURLIsOptional proves the redirect base may be empty for a
// same-origin console and round-trips when set.
func TestLoad_AppUIBaseURLIsOptional(t *testing.T) {
	env := wiringEnv()
	delete(env, "APP_UI_BASE_URL")
	withEnv(t, env)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load returned %v with APP_UI_BASE_URL empty", err)
	}
	if cfg.AppUIBaseURL != "" {
		t.Fatalf("AppUIBaseURL = %q, want empty", cfg.AppUIBaseURL)
	}

	env["APP_UI_BASE_URL"] = "https://console.example.com"
	withEnv(t, env)
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("Load returned %v with APP_UI_BASE_URL set", err)
	}
	if cfg.AppUIBaseURL != "https://console.example.com" {
		t.Fatalf("AppUIBaseURL = %q", cfg.AppUIBaseURL)
	}
}
