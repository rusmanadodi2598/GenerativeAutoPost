// Package auth_test pins the state and session stores.
//
// @file      internal/auth/store_test.go
// @for       Redis backed tests for OAuth state and session storage.
// @uses      testing, miniredis/v2, redis/go-redis/v9, internal/apierror, internal/platform/redisx
// @reason    Proves single use states, session round trips, and outage handling.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package auth

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// wantCode asserts a typed error carries the expected catalog code.
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

// newStateStore starts miniredis and a state store over it.
func newStateStore(t *testing.T) (*StateStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewStateStore(client), mr
}

// newSessionStore starts miniredis and a session store over it.
func newSessionStore(t *testing.T, ttl time.Duration) (*SessionStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewSessionStore(client, ttl), mr
}

func TestStateStoreIssueThenConsumeIsSingleUse(t *testing.T) {
	store, _ := newStateStore(t)
	ctx := context.Background()

	state, err := store.Issue(ctx, "user.info.basic,video.publish")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if state == "" {
		t.Fatal("Issue returned an empty state")
	}
	scopes, err := store.Consume(ctx, state)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if scopes != "user.info.basic,video.publish" {
		t.Fatalf("scopes = %q, want the requested scopes", scopes)
	}
	if _, err := store.Consume(ctx, state); err == nil {
		t.Fatal("expected the second Consume of one state to fail")
	} else {
		wantCode(t, err, apierror.CodeUnauthorized)
	}
}

// TestStateStoreConcurrentConsumeAllowsOneWinner races two consumers against
// one issued state so the atomic GetDel single use claim is exercised.
func TestStateStoreConcurrentConsumeAllowsOneWinner(t *testing.T) {
	store, _ := newStateStore(t)
	ctx := context.Background()

	state, err := store.Issue(ctx, "user.info.basic")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			_, err := store.Consume(ctx, state)
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		wantCode(t, err, apierror.CodeUnauthorized)
	}
	if successes != 1 {
		t.Fatalf("successes = %d, want exactly 1", successes)
	}
}

func TestStateStoreConsumeUnknownState(t *testing.T) {
	store, _ := newStateStore(t)
	_, err := store.Consume(context.Background(), "state-that-was-never-issued")
	wantCode(t, err, apierror.CodeUnauthorized)
}

// TestStateStoreConsumeEmptyStateSkipsRedis closes Redis first, so an
// unauthorized answer proves the empty state never reached the client.
func TestStateStoreConsumeEmptyStateSkipsRedis(t *testing.T) {
	store, mr := newStateStore(t)
	mr.Close()
	_, err := store.Consume(context.Background(), "")
	wantCode(t, err, apierror.CodeUnauthorized)
}

func TestSessionStoreCreateGetRoundTrip(t *testing.T) {
	store, _ := newSessionStore(t, time.Hour)
	ctx := context.Background()
	createdAt := time.Date(2026, 10, 12, 9, 30, 0, 123456789, time.UTC)
	sess := Session{
		ID:         "sess-abc",
		OperatorID: "op-42",
		CSRFToken:  "csrf-xyz",
		Scopes:     []string{"user.info.basic", "video.publish"},
		CreatedAt:  createdAt,
	}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != sess.ID || got.OperatorID != sess.OperatorID || got.CSRFToken != sess.CSRFToken {
		t.Fatalf("session identity = %+v, want %+v", got, sess)
	}
	if !reflect.DeepEqual(got.Scopes, sess.Scopes) {
		t.Fatalf("scopes = %v, want %v", got.Scopes, sess.Scopes)
	}
	if !got.CreatedAt.Equal(createdAt) {
		t.Fatalf("created at = %s, want %s", got.CreatedAt, createdAt)
	}
}

func TestSessionStoreDeleteIsIdempotent(t *testing.T) {
	store, _ := newSessionStore(t, time.Hour)
	ctx := context.Background()
	sess := Session{ID: "sess-abc", OperatorID: "op-42", CSRFToken: "csrf-xyz"}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Delete(ctx, sess.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, sess.ID); err == nil {
		t.Fatal("expected Get after Delete to fail")
	} else {
		wantCode(t, err, apierror.CodeUnauthorized)
	}
	if err := store.Delete(ctx, sess.ID); err != nil {
		t.Fatalf("second Delete = %v, want nil", err)
	}
}

// TestSessionStoreTouchExtendsTTL moves the clock past the original deadline
// between the touch and the read, so only an extended TTL can explain success.
func TestSessionStoreTouchExtendsTTL(t *testing.T) {
	store, mr := newSessionStore(t, 10*time.Minute)
	ctx := context.Background()
	sess := Session{ID: "sess-abc", OperatorID: "op-42", CSRFToken: "csrf-xyz"}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	mr.FastForward(6 * time.Minute)
	if err := store.Touch(ctx, sess.ID); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	mr.FastForward(6 * time.Minute)
	if _, err := store.Get(ctx, sess.ID); err != nil {
		t.Fatalf("Get after touch: %v", err)
	}
}

func TestSessionStoreTouchMissingSession(t *testing.T) {
	store, _ := newSessionStore(t, time.Hour)
	err := store.Touch(context.Background(), "sess-that-does-not-exist")
	wantCode(t, err, apierror.CodeUnauthorized)
}

func TestSessionStoreGetWithRedisDown(t *testing.T) {
	store, mr := newSessionStore(t, time.Hour)
	mr.Close()
	_, err := store.Get(context.Background(), "sess-abc")
	wantCode(t, err, apierror.CodeServiceUnavailable)
}
