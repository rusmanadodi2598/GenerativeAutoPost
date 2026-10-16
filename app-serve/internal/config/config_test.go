// Package config_test exercises typed environment loading.
//
// @file      internal/config/config_test.go
// @for       Table tests for config.Load failure and success cases.
// @uses      testing, internal/config
// @reason    Proves fail-fast boot behavior before the loader exists.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     config
// @stability stable
// @since     2026-10-12
package config_test

import (
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/config"
)

// withEnv swaps the process env for one case and restores it after.
func withEnv(t *testing.T, vars map[string]string) {
	t.Helper()
	for k, v := range vars {
		t.Setenv(k, v)
	}
}

// baseEnv holds the smallest passing set; cases mutate one key each.
func baseEnv() map[string]string {
	return map[string]string{
		"APP_ENV":             "development",
		"APP_PORT":            "8080",
		"DATABASE_URL":        "postgres://localhost:5432/genauto?sslmode=disable",
		"REDIS_URL":           "redis://localhost:6379/0",
		"TIKTOK_CLIENT_KEY":   "key123",
		"TIKTOK_REDIRECT_URI": "https://app.example.com/cb",
		"TIKTOK_SCOPES":       "user.info.basic",
		"TOKEN_ENC_KEY":       "0123456789abcdef0123456789abcdef",
		"TOKEN_ENC_KEY_ID":    "k1",
		"OPERATOR_ID":         "0191e2c4-0000-7000-8000-000000000001",
		"MEDIA_DRIVER":        "disk",
		"MEDIA_ROOT":          "/srv/media",
	}
}

func TestLoad(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]string)
		wantErr bool
	}{
		// Missing DSN leaves the service with no datastore.
		{name: "missing DATABASE_URL", mutate: func(m map[string]string) {
			delete(m, "DATABASE_URL")
		}, wantErr: true},
		// Non-numeric port cannot bind a listener.
		{name: "malformed APP_PORT", mutate: func(m map[string]string) {
			m["APP_PORT"] = "notaport"
		}, wantErr: true},
		// Unknown driver has no Store implementation.
		{name: "bad MEDIA_DRIVER", mutate: func(m map[string]string) {
			m["MEDIA_DRIVER"] = "tape"
		}, wantErr: true},
		// Short keys break AES-256 which needs 32 bytes.
		{name: "short TOKEN_ENC_KEY", mutate: func(m map[string]string) {
			m["TOKEN_ENC_KEY"] = "tooshort"
		}, wantErr: true},
		{name: "valid minimal env", mutate: func(m map[string]string) {}, wantErr: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			env := baseEnv()
			tc.mutate(env)
			withEnv(t, env)
			_, err := config.Load()
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}
