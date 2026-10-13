// Package auth_test pins the Redis refresh lock.
//
// @file      internal/auth/lock_test.go
// @for       RedisLocker acquire, release, and nil client behavior.
// @uses      testing, context, time, miniredis/v2, redis/go-redis/v9, internal/apierror
// @reason    A wiring fault must fail closed, not panic a refresh worker.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package auth

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

func TestRedisLockerNilClientFailsClosed(t *testing.T) {
	locker := NewRedisLocker(nil)
	ok, err := locker.Acquire(context.Background(), "k", time.Second)
	if ok {
		t.Fatal("Acquire with a nil client returned success")
	}
	wantCode(t, err, apierror.CodeServiceUnavailable)
	if err := locker.Release(context.Background(), "k"); err == nil {
		t.Fatal("Release with a nil client returned nil")
	} else {
		wantCode(t, err, apierror.CodeServiceUnavailable)
	}
}

func TestRedisLockerAcquireExcludesUntilRelease(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	locker := NewRedisLocker(client)
	ctx := context.Background()

	ok, err := locker.Acquire(ctx, "acct-1", time.Second)
	if err != nil || !ok {
		t.Fatalf("first Acquire = (%v, %v), want (true, nil)", ok, err)
	}
	ok, err = locker.Acquire(ctx, "acct-1", time.Second)
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if ok {
		t.Fatal("second Acquire succeeded while the key was held")
	}
	if err := locker.Release(ctx, "acct-1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	ok, err = locker.Acquire(ctx, "acct-1", time.Second)
	if err != nil || !ok {
		t.Fatalf("Acquire after Release = (%v, %v), want (true, nil)", ok, err)
	}
}
