// Package auth issues and checks operator sessions and OAuth state.
//
// @file      internal/auth/lock.go
// @for       Per-account lock guarding a token refresh across replicas.
// @uses      context, time, redis/go-redis/v9, internal/apierror
// @reason    Two replicas must not refresh the same TikTok account at once.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// errNilRedisClient is the internal cause for a nil client wiring fault.
var errNilRedisClient = errors.New("auth: nil redis client")

// Locker guards a per-account critical section across replicas.
type Locker interface {
	Acquire(ctx context.Context, key string, ttl time.Duration) (bool, error)
	Release(ctx context.Context, key string) error
}

// RedisLocker implements Locker with a SET NX PX claim.
type RedisLocker struct {
	client *redis.Client
}

// NewRedisLocker wires a Redis lock over the shared client.
func NewRedisLocker(client *redis.Client) *RedisLocker {
	return &RedisLocker{client: client}
}

// errLockUnavailable is the client safe answer for a Redis outage. It is a
// fresh value per call so concurrent Wrap calls stay off shared state.
func errLockUnavailable(cause error) *apierror.Error {
	return apierror.New(apierror.CodeServiceUnavailable, 0, "Could not secure the refresh lock. Try again shortly.").Wrap(cause)
}

// Acquire claims the key for ttl. A held key is a clean false. A nil client is
// a wiring fault, reported as a failed acquisition plus SERVICE_UNAVAILABLE
// rather than a nil dereference.
func (l *RedisLocker) Acquire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if l == nil || l.client == nil {
		return false, errLockUnavailable(errNilRedisClient)
	}
	ok, err := l.client.SetNX(ctx, key, 1, ttl).Result()
	if err != nil {
		return false, errLockUnavailable(err)
	}
	return ok, nil
}

// Release drops the claim. A missing key is not an error.
func (l *RedisLocker) Release(ctx context.Context, key string) error {
	if l == nil || l.client == nil {
		return errLockUnavailable(errNilRedisClient)
	}
	if err := l.client.Del(ctx, key).Err(); err != nil {
		return errLockUnavailable(err)
	}
	return nil
}
