// Package config_test covers the wiring fields the composition root reads.
//
// @file      internal/config/wiring_test.go
// @for       Tests for the media caps, chunk bound, operator id, and migration flags.
// @uses      testing, internal/config
// @reason    The composition root has no other place to read these, so a missing variable would surface only at boot.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     config
// @stability stable
// @since     2026-10-16
package config_test

import (
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/config"
)

// TestLoadWiringFields covers the variables the composition root needs to build
// the media store, the publish chunk cap, and the operator identity.
func TestLoadWiringFields(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]string)
		wantErr bool
		check   func(*testing.T, config.Config)
	}{
		{
			name:    "missing OPERATOR_ID",
			mutate:  func(m map[string]string) { delete(m, "OPERATOR_ID") },
			wantErr: true,
		},
		{
			name:    "OPERATOR_ID is not a uuid",
			mutate:  func(m map[string]string) { m["OPERATOR_ID"] = "not-a-uuid" },
			wantErr: true,
		},
		{
			name:    "missing MEDIA_ROOT on the disk driver",
			mutate:  func(m map[string]string) { delete(m, "MEDIA_ROOT") },
			wantErr: true,
		},
		{
			name: "s3 driver without a bucket",
			mutate: func(m map[string]string) {
				m["MEDIA_DRIVER"] = "s3"
				m["S3_ENDPOINT"] = "https://account.r2.cloudflarestorage.com"
				m["S3_ACCESS_KEY"] = "key"
				m["S3_SECRET_KEY"] = "secret"
			},
			wantErr: true,
		},
		{
			name: "s3 driver without an endpoint",
			mutate: func(m map[string]string) {
				m["MEDIA_DRIVER"] = "s3"
				m["S3_BUCKET"] = "media"
				m["S3_ACCESS_KEY"] = "key"
				m["S3_SECRET_KEY"] = "secret"
			},
			wantErr: true,
		},
		{
			name: "s3 driver without an access key",
			mutate: func(m map[string]string) {
				m["MEDIA_DRIVER"] = "s3"
				m["S3_ENDPOINT"] = "https://account.r2.cloudflarestorage.com"
				m["S3_BUCKET"] = "media"
				m["S3_SECRET_KEY"] = "secret"
			},
			wantErr: true,
		},
		{
			name: "s3 driver loads credentials and defaults the region",
			mutate: func(m map[string]string) {
				m["MEDIA_DRIVER"] = "s3"
				m["S3_ENDPOINT"] = "https://account.r2.cloudflarestorage.com"
				m["S3_BUCKET"] = "media"
				m["S3_ACCESS_KEY"] = "key"
				m["S3_SECRET_KEY"] = "secret"
			},
			check: func(t *testing.T, cfg config.Config) {
				if cfg.S3Region != "auto" {
					t.Errorf("S3Region = %q, want auto", cfg.S3Region)
				}
			},
		},
		{
			name:   "disk driver loads caps and allowed types",
			mutate: func(m map[string]string) {},
			check: func(t *testing.T, cfg config.Config) {
				if cfg.MediaRoot != "/srv/media" {
					t.Errorf("MediaRoot = %q", cfg.MediaRoot)
				}
				if cfg.MediaMaxBytes != 200<<20 {
					t.Errorf("MediaMaxBytes = %d", cfg.MediaMaxBytes)
				}
				if len(cfg.MediaAllowedTypes) != 2 || cfg.MediaAllowedTypes[0] != "video/mp4" {
					t.Errorf("MediaAllowedTypes = %v", cfg.MediaAllowedTypes)
				}
				if cfg.ChunkMaxBytes != 64<<20 {
					t.Errorf("ChunkMaxBytes = %d", cfg.ChunkMaxBytes)
				}
			},
		},
		{
			name:   "worker size falls back to the default",
			mutate: func(m map[string]string) { delete(m, "WORKER_SIZE") },
			check: func(t *testing.T, cfg config.Config) {
				if cfg.WorkerSize != 4 {
					t.Errorf("WorkerSize = %d, want the documented default", cfg.WorkerSize)
				}
			},
		},
		{
			name:    "malformed WORKER_SIZE fails fast",
			mutate:  func(m map[string]string) { m["WORKER_SIZE"] = "many" },
			wantErr: true,
		},
		{
			name:   "webhook throttle falls back to the documented default",
			mutate: func(m map[string]string) { delete(m, "WEBHOOK_THROTTLE_MAX") },
			check: func(t *testing.T, cfg config.Config) {
				if cfg.WebhookThrottleMax != 120 {
					t.Errorf("WebhookThrottleMax = %d, want the documented default", cfg.WebhookThrottleMax)
				}
				if cfg.WebhookThrottleWnd != 2*time.Minute {
					t.Errorf("WebhookThrottleWnd = %v, want the documented default", cfg.WebhookThrottleWnd)
				}
			},
		},
		{
			name:    "a zero throttle max fails fast",
			mutate:  func(m map[string]string) { m["WEBHOOK_THROTTLE_MAX"] = "0" },
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := wiringEnv()
			tc.mutate(env)
			withEnv(t, env)
			cfg, err := config.Load()
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load returned %v", err)
			}
			tc.check(t, cfg)
		})
	}
}

// wiringEnv is baseEnv plus every variable the composition root consumes.
func wiringEnv() map[string]string {
	env := baseEnv()
	env["OPERATOR_ID"] = "0191e2c4-0000-7000-8000-000000000001"
	env["MEDIA_ROOT"] = "/srv/media"
	env["MEDIA_MAX_BYTES"] = "209715200"
	env["MEDIA_MAX_DURATION_MS"] = "600000"
	env["MEDIA_ALLOWED_TYPES"] = "video/mp4,image/jpeg"
	env["CHUNK_MAX_BYTES"] = "67108864"
	env["WORKER_SIZE"] = "6"
	return env
}

// TestAddrUsesTheConfiguredPort proves the listener address follows config.
func TestAddrUsesTheConfiguredPort(t *testing.T) {
	env := wiringEnv()
	env["APP_PORT"] = "9999"
	withEnv(t, env)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load returned %v", err)
	}
	if cfg.Addr() != ":9999" {
		t.Fatalf("Addr = %q", cfg.Addr())
	}
}
