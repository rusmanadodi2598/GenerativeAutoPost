// Package auth_test pins ownership enforcement and detached lock release.
//
// @file      internal/auth/lifecycle_ownership_test.go
// @for       Refresh and Revoke ownership denials plus the detached release.
// @uses      testing, context, errors, internal/apierror
// @reason    A session operator must not refresh or revoke a foreign account.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// errAccountNotFound is the not-found answer a store returns when the caller
// does not own the account; the service must pass it through unchanged.
func errAccountNotFound() *apierror.Error {
	return apierror.New(apierror.CodeNotFound, 0, "Account not found.")
}

func TestRefreshMaterialDenialReturnsErrorAndSkipsTokenCall(t *testing.T) {
	denied := errAccountNotFound()
	refresher := &fakeRefresher{}
	accounts := &fakeUpserter{materialErr: denied}
	svc, _, _ := newLifecycleService(t, refresher, accounts, newFakeLocker(), nil)

	err := svc.Refresh(context.Background(), testOperatorID, "acct-foreign")
	if err != denied {
		t.Fatalf("Refresh error = %v, want the store denial %v", err, denied)
	}
	if refresher.refreshCount() != 0 {
		t.Fatalf("RefreshToken ran %d times on a denied material read", refresher.refreshCount())
	}
	if accounts.materialOperator != testOperatorID {
		t.Fatalf("RefreshMaterial operator = %q, want %q", accounts.materialOperator, testOperatorID)
	}
}

func TestRevokeMaterialDenialReturnsErrorAndSkipsRevoke(t *testing.T) {
	denied := errAccountNotFound()
	refresher := &fakeRefresher{}
	accounts := &fakeUpserter{materialErr: denied}
	svc, _, _ := newLifecycleService(t, refresher, accounts, newFakeLocker(), nil)

	err := svc.Revoke(context.Background(), testOperatorID, "acct-foreign")
	if err != denied {
		t.Fatalf("Revoke error = %v, want the store denial %v", err, denied)
	}
	if refresher.revokeCount() != 0 {
		t.Fatalf("Revoke ran %d times on a denied material read", refresher.revokeCount())
	}
	if accounts.revoked != 0 {
		t.Fatalf("MarkRevoked ran %d times on a denied material read", accounts.revoked)
	}
}

// TestRefreshReleasesLockOnDetachedContext cancels the request context inside
// RefreshToken, right before the deferred release runs, and proves the release
// still lands on a live context. The release failure must not mask the token
// error either, so both are set and only the token error is returned.
func TestRefreshReleasesLockOnDetachedContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tokenErr := apierror.New(apierror.CodeTikTokReauthRequired, 0, "The TikTok connection needs to be renewed.")
	refresher := &fakeRefresher{refreshErr: tokenErr, cancel: cancel}
	accounts := &fakeUpserter{refreshToken: "refresh-1"}
	locker := &recordingLocker{releaseErr: errors.New("redis release down")}
	svc, _, _ := newLifecycleService(t, refresher, accounts, locker, nil)

	err := svc.Refresh(ctx, testOperatorID, "acct-1")
	if err != tokenErr {
		t.Fatalf("Refresh error = %v, want the token error %v", err, tokenErr)
	}
	if locker.releaseCalls != 1 {
		t.Fatalf("Release called %d times, want 1", locker.releaseCalls)
	}
	if locker.releaseCtxErr != nil {
		t.Fatalf("Release context was cancelled: %v", locker.releaseCtxErr)
	}
}
