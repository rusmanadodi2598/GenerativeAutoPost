// Package account holds the TikTok account aggregate and its persistence shapes.
//
// @file      internal/account/service_listing_test.go
// @for       Tests for the operator account listing and credential clearing.
// @uses      testing, context, errors, time, internal/apierror, internal/tiktokclient
// @reason    The console lists its accounts and deletes them, so both paths need an ownership proof.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-16
package account

import (
	"context"
	"errors"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// tokensFor builds a token set whose open id makes each seeded account unique.
func tokensFor(openID string) tiktokclient.TokenSet {
	tokens := testTokens()
	tokens.OpenID = openID
	return tokens
}

// seedOwned stores one account for operatorID and returns its id.
func seedOwned(t *testing.T, svc *Service, operatorID, openID string) string {
	t.Helper()
	id, err := svc.UpsertAccount(context.Background(), operatorID, tokensFor(openID), []string{"user.info.basic"})
	if err != nil {
		t.Fatalf("UpsertAccount returned %v", err)
	}
	return id
}

// TestListReturnsOnlyTheOperatorsAccounts proves the listing is scoped, since a
// leak here would hand one operator another operator's TikTok accounts.
func TestListReturnsOnlyTheOperatorsAccounts(t *testing.T) {
	svc, _ := newTestService(t)
	mine := seedOwned(t, svc, "operator-a", "open-a")
	seedOwned(t, svc, "operator-b", "open-b")

	views, err := svc.List(context.Background(), "operator-a")
	if err != nil {
		t.Fatalf("List returned %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("want 1 account, got %d", len(views))
	}
	if views[0].ID != mine {
		t.Fatalf("want account %s, got %s", mine, views[0].ID)
	}
	if views[0].Scopes == nil {
		t.Error("scopes must be an empty list rather than null")
	}
}

// TestListIncludesRevokedAccounts proves a revoked account still lists, since
// the console has to render its reconnection state.
func TestListIncludesRevokedAccounts(t *testing.T) {
	svc, _ := newTestService(t)
	id := seedOwned(t, svc, "operator-a", "open-a")
	if err := svc.MarkRevoked(context.Background(), "operator-a", id); err != nil {
		t.Fatalf("MarkRevoked returned %v", err)
	}

	views, err := svc.List(context.Background(), "operator-a")
	if err != nil {
		t.Fatalf("List returned %v", err)
	}
	if len(views) != 1 || !views[0].Revoked {
		t.Fatalf("want one revoked account, got %+v", views)
	}
}

// TestForgetClearsTheStoredCredentials proves the delete path drops the token
// material: keeping it on disk after revoke leaves a usable secret behind.
func TestForgetClearsTheStoredCredentials(t *testing.T) {
	svc, store := newTestService(t)
	id := seedOwned(t, svc, "operator-a", "open-a")

	if err := svc.Forget(context.Background(), "operator-a", id); err != nil {
		t.Fatalf("Forget returned %v", err)
	}
	rec, err := store.GetOwned(context.Background(), "operator-a", id)
	if err != nil {
		t.Fatalf("GetOwned after Forget returned %v", err)
	}
	if len(rec.AccessEnc) != 0 || len(rec.RefreshEnc) != 0 {
		t.Fatalf("token material survived Forget: access %d bytes refresh %d bytes", len(rec.AccessEnc), len(rec.RefreshEnc))
	}
	if rec.RevokedAt == nil {
		t.Fatal("Forget must stamp the revoke time")
	}
}

// TestForgetOnAForeignAccountAnswersNotFound proves the delete path cannot be
// used to probe which account ids exist.
func TestForgetOnAForeignAccountAnswersNotFound(t *testing.T) {
	svc, store := newTestService(t)
	id := seedOwned(t, svc, "operator-b", "open-b")

	err := svc.Forget(context.Background(), "operator-a", id)
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) || apiErr.Code != apierror.CodeNotFound {
		t.Fatalf("want NOT_FOUND, got %v", err)
	}
	if len(store.clearCalls) != 0 {
		t.Fatal("a foreign account must not reach the clear write")
	}
}

// TestForgetIsIdempotent proves a repeat delete still answers success, which is
// what a retried DELETE from app-ui expects.
func TestForgetIsIdempotent(t *testing.T) {
	svc, _ := newTestService(t)
	id := seedOwned(t, svc, "operator-a", "open-a")

	if err := svc.Forget(context.Background(), "operator-a", id); err != nil {
		t.Fatalf("first Forget returned %v", err)
	}
	if err := svc.Forget(context.Background(), "operator-a", id); err != nil {
		t.Fatalf("second Forget returned %v", err)
	}
}
