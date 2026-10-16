// Package idempotency pins the Idempotency-Key store against miniredis.
//
// @file      internal/platform/idempotency/store_test.go
// @for       Claim, replay, reuse, conflict, release, and expiry of the store.
// @uses      testing, miniredis/v2, redis/go-redis/v9, internal/apierror
// @reason    Duplicate requests are the risk this store carries.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package idempotency

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/redisx"
)

func newIdemStore(t *testing.T, ttl time.Duration) (*Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return New(client, ttl), mr
}

func wantCode(t *testing.T, err error, code apierror.Code) {
	t.Helper()
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not *apierror.Error", err)
	}
	if apiErr.Code != code {
		t.Fatalf("code = %s, want %s", apiErr.Code, code)
	}
}

func TestIdempotencyFirstClaimSucceeds(t *testing.T) {
	store, _ := newIdemStore(t, time.Minute)
	replay, err := store.Begin(context.Background(), "k1", BodyHash([]byte("a")))
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if replay != nil {
		t.Fatalf("replay = %q, want nil on a first claim", replay)
	}
}

func TestIdempotencySameKeySameBodyReplays(t *testing.T) {
	store, _ := newIdemStore(t, time.Minute)
	ctx := context.Background()
	hash := BodyHash([]byte("a"))
	if _, err := store.Begin(ctx, "k1", hash); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := store.Complete(ctx, "k1", hash, []byte(`{"status":"accepted"}`)); err != nil {
		t.Fatalf("complete: %v", err)
	}
	replay, err := store.Begin(ctx, "k1", hash)
	if err != nil {
		t.Fatalf("repeat claim: %v", err)
	}
	if string(replay) != `{"status":"accepted"}` {
		t.Fatalf("replay = %q", replay)
	}
}

func TestIdempotencySameKeyDifferentBodyIsReuse(t *testing.T) {
	store, _ := newIdemStore(t, time.Minute)
	ctx := context.Background()
	first := BodyHash([]byte("a"))
	if _, err := store.Begin(ctx, "k1", first); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	_, err := store.Begin(ctx, "k1", BodyHash([]byte("b")))
	if !errors.Is(err, ErrKeyReused) {
		t.Fatalf("err = %v, want ErrKeyReused", err)
	}
}

func TestIdempotencyConcurrentClaimConflicts(t *testing.T) {
	store, _ := newIdemStore(t, time.Minute)
	ctx := context.Background()
	hash := BodyHash([]byte("a"))
	if _, err := store.Begin(ctx, "k1", hash); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	_, err := store.Begin(ctx, "k1", hash)
	wantCode(t, err, apierror.CodeConflict)
}

func TestIdempotencyKeyExpires(t *testing.T) {
	store, mr := newIdemStore(t, time.Minute)
	ctx := context.Background()
	hash := BodyHash([]byte("a"))
	if _, err := store.Begin(ctx, "k1", hash); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	mr.FastForward(2 * time.Minute)
	if mr.Exists(redisx.KeyIdempotency + "k1:hash") {
		t.Fatal("hash key still present after ttl")
	}
	if _, err := store.Begin(ctx, "k1", hash); err != nil {
		t.Fatalf("claim after expiry: %v", err)
	}
}

func TestIdempotencyCompleteRejectsForeignBody(t *testing.T) {
	store, _ := newIdemStore(t, time.Minute)
	ctx := context.Background()
	first := BodyHash([]byte("a"))
	if _, err := store.Begin(ctx, "k1", first); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	err := store.Complete(ctx, "k1", BodyHash([]byte("b")), []byte("{}"))
	if !errors.Is(err, ErrKeyReused) {
		t.Fatalf("err = %v, want ErrKeyReused", err)
	}
}

func TestIdempotencyRejectsEmptyKey(t *testing.T) {
	store, _ := newIdemStore(t, time.Minute)
	_, err := store.Begin(context.Background(), "", BodyHash([]byte("a")))
	wantCode(t, err, apierror.CodeValidationFailed)
}

func TestIdempotencyReleaseFreesTheKeyForACorrectedRetry(t *testing.T) {
	store, _ := newIdemStore(t, time.Minute)
	ctx := context.Background()
	hash := BodyHash([]byte("a"))
	if _, err := store.Begin(ctx, "k1", hash); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := store.Release(ctx, "k1", hash); err != nil {
		t.Fatalf("release: %v", err)
	}
	replay, err := store.Begin(ctx, "k1", hash)
	if err != nil {
		t.Fatalf("claim after release: %v", err)
	}
	if replay != nil {
		t.Fatalf("replay = %q, want nil so the retry stores its own object", replay)
	}
}

func TestIdempotencyReleaseLeavesAForeignClaimAlone(t *testing.T) {
	store, _ := newIdemStore(t, time.Minute)
	ctx := context.Background()
	hash := BodyHash([]byte("a"))
	if _, err := store.Begin(ctx, "k1", hash); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := store.Complete(ctx, "k1", hash, []byte(`{"id":"1"}`)); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := store.Release(ctx, "k1", BodyHash([]byte("other"))); err != nil {
		t.Fatalf("release with a foreign hash: %v", err)
	}
	replay, err := store.Begin(ctx, "k1", hash)
	if err != nil {
		t.Fatalf("claim after a foreign release: %v", err)
	}
	if string(replay) != `{"id":"1"}` {
		t.Fatalf("replay = %q, want the stored response to survive", replay)
	}
}

func TestIdempotencyReleaseRejectsEmptyInput(t *testing.T) {
	store, _ := newIdemStore(t, time.Minute)
	wantCode(t, store.Release(context.Background(), "", "hash"), apierror.CodeValidationFailed)
	wantCode(t, store.Release(context.Background(), "k1", ""), apierror.CodeValidationFailed)
}
