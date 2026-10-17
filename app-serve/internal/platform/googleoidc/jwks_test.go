// Package googleoidc_test covers the JWKS cache concurrency behavior.
//
// @file      internal/platform/googleoidc/jwks_test.go
// @for       Prove a cached key verifies while a JWKS fetch is in flight.
// @uses      testing, net/http/httptest, encoding/json, sync, context, time
// @reason    A slow JWKS endpoint must not block verification of cached keys.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-17
package googleoidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestVerifyIDToken_CachedKeyNotBlockedByFetch warms the cache, starts a
// verification that blocks inside the JWKS fetch, then asserts a second
// verification with the cached key still completes.
func TestVerifyIDToken_CachedKeyNotBlockedByFetch(t *testing.T) {
	key := newTestKey(t, "key-1")
	block := make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(block) }) }
	defer unblock()

	started := make(chan struct{})
	var startOnce sync.Once
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if !first {
			startOnce.Do(func() { close(started) })
			<-block
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{key.jwksEntry()}})
	}))
	defer srv.Close()

	client := New(Config{ClientID: "client-1", JWKSURL: srv.URL})
	cached := signToken(t, key, validClaims("client-1", "nonce-1"))
	if _, err := client.VerifyIDToken(context.Background(), cached, "nonce-1"); err != nil {
		t.Fatalf("warming the cache: %v", err)
	}

	unknown := newTestKey(t, "key-2")
	unknownToken := signToken(t, unknown, validClaims("client-1", "nonce-2"))
	fetchDone := make(chan struct{})
	go func() {
		defer close(fetchDone)
		_, _ = client.VerifyIDToken(context.Background(), unknownToken, "nonce-2")
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the JWKS fetch never started")
	}

	cachedDone := make(chan error, 1)
	go func() {
		_, err := client.VerifyIDToken(context.Background(), cached, "nonce-1")
		cachedDone <- err
	}()
	select {
	case err := <-cachedDone:
		if err != nil {
			t.Fatalf("VerifyIDToken(cached) error = %v, want a cache hit during an in-flight fetch", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("VerifyIDToken(cached) blocked on the in-flight JWKS fetch")
	}

	unblock()
	<-fetchDone
}
