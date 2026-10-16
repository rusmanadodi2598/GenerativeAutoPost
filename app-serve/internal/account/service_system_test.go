// Package account covers the system scoped token lookup for background work.
//
// @file      internal/account/service_system_test.go
// @for       AccessForAccount tests for the reconciler sweep resolver.
// @uses      context, testing
// @reason    The sweep has no operator session, so this path must stay narrow and never serve a revoked account.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-15
package account

import (
	"context"
	"testing"
)

func TestAccessForAccountReturnsThePlaintextToken(t *testing.T) {
	svc, _, id := seededService(t)
	got, err := svc.AccessForAccount(context.Background(), id)
	if err != nil {
		t.Fatalf("AccessForAccount returned %v", err)
	}
	if got != testAccess {
		t.Fatalf("token = %q, want %q", got, testAccess)
	}
}

func TestAccessForAccountRejectsARevokedAccount(t *testing.T) {
	svc, _, id := seededService(t)
	if err := svc.MarkRevoked(context.Background(), "op-1", id); err != nil {
		t.Fatalf("MarkRevoked: %v", err)
	}
	got, err := svc.AccessForAccount(context.Background(), id)
	if err == nil {
		t.Fatalf("AccessForAccount returned %q for a revoked account", got)
	}
	if got != "" {
		t.Fatalf("a revoked account returned a token alongside the error: %q", got)
	}
}

func TestAccessForAccountRejectsAnUnknownAccount(t *testing.T) {
	svc, _, _ := seededService(t)
	if _, err := svc.AccessForAccount(context.Background(), "acct-missing"); err != errTestNotFound {
		t.Fatalf("err = %v, want the store denial unchanged", err)
	}
}
