// Package account covers the ownership-checked token resolver.
//
// @file      internal/account/service_resolver_test.go
// @for       Resolver tests for the access token lookup publish and insight call.
// @uses      context, strings, testing
// @reason    The resolver is the only place a plaintext access token leaves the vault.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-15
package account

import (
	"context"
	"strings"
	"testing"
)

// seededService builds a vault with one owned account and returns its id.
func seededService(t *testing.T) (*Service, *fakeStore, string) {
	t.Helper()
	svc, store := newTestService(t)
	id, err := svc.UpsertAccount(context.Background(), "op-1", testTokens(), []string{"video.publish"})
	if err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	return svc, store, id
}

// TestOwnedReturnsPlaintextAccessToken proves the resolver returns the access
// token after an ownership check, so publish and insight can call TikTok.
func TestOwnedReturnsPlaintextAccessToken(t *testing.T) {
	svc, _, id := seededService(t)
	got, err := svc.Owned(context.Background(), "op-1", id)
	if err != nil {
		t.Fatalf("Owned returned %v", err)
	}
	if got != testAccess {
		t.Fatalf("Owned = %q, want %q", got, testAccess)
	}
}

// TestOwnedRejectsForeignOperator proves a foreign operator gets the store's
// denial unchanged, so a caller cannot map a guessed id into a token.
func TestOwnedRejectsForeignOperator(t *testing.T) {
	svc, store, id := seededService(t)
	got, err := svc.Owned(context.Background(), "op-2", id)
	if err != errTestNotFound {
		t.Fatalf("err = %v, want the store denial unchanged", err)
	}
	if got != "" {
		t.Fatalf("Owned returned a token alongside the error: %q", got)
	}
	if store.getCalls != 1 || store.getOperator != "op-2" {
		t.Fatalf("store saw operator %q after %d calls, want op-2 once", store.getOperator, store.getCalls)
	}
}

// TestOwnedRejectsUnknownAccount proves an unknown id is denied too.
func TestOwnedRejectsUnknownAccount(t *testing.T) {
	svc, _, _ := seededService(t)
	if _, err := svc.Owned(context.Background(), "op-1", "acct-missing"); err != errTestNotFound {
		t.Fatalf("err = %v, want the store denial unchanged", err)
	}
}

// TestOwnedRejectsRevokedAccount proves a revoked account hands out no token,
// since TikTok has already invalidated it and the console must reconnect.
func TestOwnedRejectsRevokedAccount(t *testing.T) {
	svc, _, id := seededService(t)
	if err := svc.MarkRevoked(context.Background(), "op-1", id); err != nil {
		t.Fatalf("MarkRevoked: %v", err)
	}
	got, err := svc.Owned(context.Background(), "op-1", id)
	if err == nil {
		t.Fatalf("Owned returned %q for a revoked account", got)
	}
	if got != "" {
		t.Fatalf("Owned returned a token alongside the error: %q", got)
	}
}

// TestOwnedRejectsUnreadableCiphertext proves an unreadable row answers a
// typed error carrying no token text.
func TestOwnedRejectsUnreadableCiphertext(t *testing.T) {
	svc, store, id := seededService(t)
	rec := store.records["open-1"]
	rec.AccessEnc = []byte("not ciphertext")
	store.records["open-1"] = rec
	got, err := svc.Owned(context.Background(), "op-1", id)
	if err == nil {
		t.Fatal("Owned succeeded on unreadable ciphertext")
	}
	if got != "" {
		t.Fatalf("Owned returned %q alongside the decrypt error", got)
	}
	if strings.Contains(err.Error(), testAccess) {
		t.Fatal("decrypt error leaked the plaintext token")
	}
}

// TestOwnedRejectsCancelledContext proves the resolver honours cancellation.
func TestOwnedRejectsCancelledContext(t *testing.T) {
	svc, _, id := seededService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Owned(ctx, "op-1", id); err == nil {
		t.Fatal("Owned succeeded on a cancelled context")
	}
}
