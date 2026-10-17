// Package googleoidc_test pins the clock-skew allowance on id token freshness.
//
// @file      internal/platform/googleoidc/skew_test.go
// @for       Accept and reject cases for the exp and iat skew leeway.
// @uses      testing, context, time
// @reason    A host clock a few seconds behind must not lock the only operator out.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-17
package googleoidc

import (
	"context"
	"testing"
	"time"
)

// TestVerifyIDToken_AcceptsSmallClockSkew pins the leeway that keeps an app
// host a few seconds behind from locking the only operator out.
func TestVerifyIDToken_AcceptsSmallClockSkew(t *testing.T) {
	key := newTestKey(t, "key-1")
	client := newTestClient(t, key)

	expired := validClaims("client-1", "nonce-1")
	expired["exp"] = time.Now().Add(-10 * time.Second).Unix()
	if _, err := client.VerifyIDToken(context.Background(), signToken(t, key, expired), "nonce-1"); err != nil {
		t.Fatalf("a token expired by 10s should sit inside the skew leeway, got %v", err)
	}

	future := validClaims("client-1", "nonce-1")
	future["iat"] = time.Now().Add(10 * time.Second).Unix()
	if _, err := client.VerifyIDToken(context.Background(), signToken(t, key, future), "nonce-1"); err != nil {
		t.Fatalf("an iat 10s ahead should sit inside the skew leeway, got %v", err)
	}
}

// TestVerifyIDToken_RejectsSkewBeyondTheLeeway proves the allowance is bounded,
// so a token that is genuinely old still cannot be replayed.
func TestVerifyIDToken_RejectsSkewBeyondTheLeeway(t *testing.T) {
	key := newTestKey(t, "key-1")
	client := newTestClient(t, key)

	stale := validClaims("client-1", "nonce-1")
	stale["exp"] = time.Now().Add(-clockSkew - time.Minute).Unix()
	mustReject(t, client, signToken(t, key, stale), "nonce-1")

	ahead := validClaims("client-1", "nonce-1")
	ahead["iat"] = time.Now().Add(clockSkew + time.Minute).Unix()
	mustReject(t, client, signToken(t, key, ahead), "nonce-1")
}
