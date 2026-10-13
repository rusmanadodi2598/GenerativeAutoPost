// Package auth_test pins the token refresh, revoke, and logout lifecycle.
//
// @file      internal/auth/lifecycle_test.go
// @for       Refresh, Revoke, and Logout behavior and their failure paths.
// @uses      testing, sync, bytes, log/slog, internal/apierror, internal/tiktokclient
// @reason    Refresh must run once per account and a dead token must not linger.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package auth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

func TestRefreshHappyPathStoresTokensAndReleasesLock(t *testing.T) {
	newTokens := tiktokclient.TokenSet{AccessToken: "access-2", RefreshToken: "refresh-2"}
	refresher := &fakeRefresher{tokens: newTokens}
	accounts := &fakeUpserter{refreshToken: "refresh-1", accessToken: "access-1"}
	locker := newFakeLocker()
	svc, _, _ := newLifecycleService(t, refresher, accounts, locker, nil)

	if err := svc.Refresh(context.Background(), "acct-1"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refresher.refreshCount() != 1 {
		t.Fatalf("RefreshToken called %d times, want 1", refresher.refreshCount())
	}
	if refresher.refreshArg != "refresh-1" {
		t.Fatalf("RefreshToken arg = %q, want refresh-1", refresher.refreshArg)
	}
	if accounts.storeCalls != 1 || accounts.storeTokens != newTokens {
		t.Fatalf("StoreTokens = %d calls with %+v, want 1 call with %+v", accounts.storeCalls, accounts.storeTokens, newTokens)
	}
	if locker.releaseCount() != 1 {
		t.Fatalf("lock released %d times, want 1", locker.releaseCount())
	}
}

func TestRefreshWhenLockHeldReturnsConflict(t *testing.T) {
	refresher := &fakeRefresher{}
	accounts := &fakeUpserter{}
	locker := newFakeLocker()
	locker.held[refreshLockKey("acct-1")] = true
	svc, _, _ := newLifecycleService(t, refresher, accounts, locker, nil)

	err := svc.Refresh(context.Background(), "acct-1")
	wantCode(t, err, apierror.CodeConflict)
	if refresher.refreshCount() != 0 {
		t.Fatalf("RefreshToken ran %d times while the lock was held", refresher.refreshCount())
	}
}

// TestRefreshSerializesConcurrentCallers holds the first refresh open in
// RefreshToken while the second caller tries the same account, so exactly one
// acquires and the other reports CONFLICT. Run under -race.
func TestRefreshSerializesConcurrentCallers(t *testing.T) {
	gate := make(chan struct{})
	refresher := &fakeRefresher{tokens: tiktokclient.TokenSet{AccessToken: "access-2"}, gate: gate}
	accounts := &fakeUpserter{refreshToken: "refresh-1"}
	locker := newFakeLocker()
	svc, _, _ := newLifecycleService(t, refresher, accounts, locker, nil)

	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { first <- svc.Refresh(context.Background(), "acct-1") }()
	waitForLockHeld(t, locker)

	go func() { second <- svc.Refresh(context.Background(), "acct-1") }()
	errSecond := <-second
	wantCode(t, errSecond, apierror.CodeConflict)

	close(gate)
	if err := <-first; err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	if refresher.refreshCount() != 1 {
		t.Fatalf("RefreshToken called %d times, want exactly 1", refresher.refreshCount())
	}
}

// waitForLockHeld spins until the fake locker has taken the account key, so
// the second caller is guaranteed to arrive while the first holds it.
func waitForLockHeld(t *testing.T, locker *fakeLocker) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		locker.mu.Lock()
		held := locker.held[refreshLockKey("acct-1")]
		locker.mu.Unlock()
		if held {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("first refresh never acquired the lock")
}

func TestRefreshInvalidTokenMarksNeedsReauthOnce(t *testing.T) {
	refresher := &fakeRefresher{
		refreshErr: apierror.New(apierror.CodeTikTokReauthRequired, 0, "The TikTok connection needs to be renewed."),
	}
	accounts := &fakeUpserter{refreshToken: "refresh-1"}
	svc, _, _ := newLifecycleService(t, refresher, accounts, newFakeLocker(), nil)

	err := svc.Refresh(context.Background(), "acct-1")
	wantCode(t, err, apierror.CodeTikTokReauthRequired)
	if accounts.needsReauth != 1 {
		t.Fatalf("MarkNeedsReauth called %d times, want 1", accounts.needsReauth)
	}
	if refresher.refreshCount() != 1 {
		t.Fatalf("RefreshToken called %d times, want 1 (no retry loop)", refresher.refreshCount())
	}
	if accounts.storeCalls != 0 {
		t.Fatalf("StoreTokens ran %d times on a reauth failure", accounts.storeCalls)
	}
}

func TestRefreshLockAcquireErrorPropagates(t *testing.T) {
	locker := newFakeLocker()
	locker.acqErr = apierror.New(apierror.CodeServiceUnavailable, 0, "Could not secure the refresh lock.")
	refresher := &fakeRefresher{}
	svc, _, _ := newLifecycleService(t, refresher, &fakeUpserter{}, locker, nil)

	err := svc.Refresh(context.Background(), "acct-1")
	wantCode(t, err, apierror.CodeServiceUnavailable)
	if refresher.refreshCount() != 0 {
		t.Fatalf("RefreshToken ran %d times after a lock failure", refresher.refreshCount())
	}
}

func TestRevokeClearsLocalStateWhenTikTokFails(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	refresher := &fakeRefresher{revokeErr: errors.New("tiktok revoke down")}
	accounts := &fakeUpserter{accessToken: "access-1"}
	svc, _, _ := newLifecycleService(t, refresher, accounts, newFakeLocker(), logger)

	if err := svc.Revoke(context.Background(), "acct-1"); err != nil {
		t.Fatalf("Revoke = %v, want nil", err)
	}
	if refresher.revokeCount() != 1 {
		t.Fatalf("Revoke called %d times, want 1", refresher.revokeCount())
	}
	if refresher.revokeArg != "access-1" {
		t.Fatalf("Revoke arg = %q, want access-1", refresher.revokeArg)
	}
	if accounts.revoked != 1 {
		t.Fatalf("MarkRevoked called %d times, want 1", accounts.revoked)
	}
	if !bytes.Contains(logs.Bytes(), []byte("revoking tiktok access token")) {
		t.Fatalf("revoke failure was not logged, got %q", logs.String())
	}
}

func TestRevokeMarkRevokedFailureIsReturned(t *testing.T) {
	refresher := &fakeRefresher{}
	accounts := &fakeUpserter{
		accessToken: "access-1",
		markErr:     apierror.New(apierror.CodeServiceUnavailable, 0, "Account store is unavailable."),
	}
	svc, _, _ := newLifecycleService(t, refresher, accounts, newFakeLocker(), nil)

	err := svc.Revoke(context.Background(), "acct-1")
	wantCode(t, err, apierror.CodeServiceUnavailable)
}

func TestLogoutDeletesSession(t *testing.T) {
	refresher := &fakeRefresher{}
	accounts := &fakeUpserter{}
	svc, sessions, _ := newLifecycleService(t, refresher, accounts, newFakeLocker(), nil)
	ctx := context.Background()

	sess := Session{ID: "sess-1", OperatorID: testOperatorID, CSRFToken: "csrf-1"}
	if err := sessions.Create(ctx, sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Logout(ctx, sess.ID); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := sessions.Get(ctx, sess.ID); err == nil {
		t.Fatal("session still readable after Logout")
	} else {
		wantCode(t, err, apierror.CodeUnauthorized)
	}
}

func TestLogoutStoreErrorPropagates(t *testing.T) {
	refresher := &fakeRefresher{}
	accounts := &fakeUpserter{}
	svc, _, mr := newLifecycleService(t, refresher, accounts, newFakeLocker(), nil)
	mr.Close()

	err := svc.Logout(context.Background(), "sess-1")
	wantCode(t, err, apierror.CodeServiceUnavailable)
}
