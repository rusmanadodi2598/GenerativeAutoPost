// Package redisx opens a Redis client and owns key namespaces.
//
// @file      internal/platform/redisx/redisx.go
// @for       Redis client plus shared key prefix constants.
// @uses      github.com/redis/go-redis/v9, internal/config
// @reason    Single place for Redis wiring and key naming.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package redisx

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/config"
)

// Key namespaces shared by every Redis user in the service.
const (
	KeySession       = "genauto:session:"
	KeyOAuthState    = "genauto:oauth:state:"
	KeyRateLimit     = "genauto:ratelimit:"
	KeyIdempotency   = "genauto:idempotency:"
	KeyWebhookDedupe = "genauto:webhook:dedupe:"
)

// Open parses the Redis URL and verifies the connection with a timeout.
func Open(ctx context.Context, cfg config.Config) (*redis.Client, error) {
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("redisx: parse URL: %w", err)
	}
	client := redis.NewClient(opts)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redisx: ping: %w", err)
	}
	return client, nil
}
