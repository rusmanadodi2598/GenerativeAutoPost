// Package account holds the TikTok account aggregate and its persistence shapes.
//
// @file      internal/account/service_lifecycle_test.go
// @for       Ownership, rotation, and life cycle tests for the token vault.
// @uses      testing, context, bytes, errors, log/slog, internal/platform/crypto
// @reason    Ownership checks and lifecycle calls must reach the store exactly once.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package account

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/crypto"
)

func TestGetOwned_BuildsAggregateFromRecord(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.UpsertAccount(context.Background(), "op-1", testTokens(), []string{"user.info.basic", "video.publish"}); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	acct, err := svc.GetOwned(context.Background(), "op-1", "acct-open-1")
	if err != nil {
		t.Fatalf("GetOwned: %v", err)
	}
	if acct.ID() != "acct-open-1" || acct.OperatorID() != "op-1" || acct.OpenID() != "open-1" {
		t.Errorf("aggregate identity mismatch: id=%q op=%q open=%q", acct.ID(), acct.OperatorID(), acct.OpenID())
	}
	if !acct.HasScope("video.publish") || acct.HasScope("unknown.scope") {
		t.Errorf("scope projection mismatch: %v", acct.Scopes())
	}
}

func TestGetOwned_PropagatesForeignDenial(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.UpsertAccount(context.Background(), "op-1", testTokens(), nil); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	_, err := svc.GetOwned(context.Background(), "op-2", "acct-open-1")
	if err != errTestNotFound {
		t.Fatalf("err = %v, want the store denial unchanged", err)
	}
}

func TestMarkNeedsReauth_DelegatesTrue(t *testing.T) {
	svc, store := newTestService(t)
	if err := svc.MarkNeedsReauth(context.Background(), "acct-open-1"); err != nil {
		t.Fatalf("MarkNeedsReauth: %v", err)
	}
	got, ok := store.needsCalls["acct-open-1"]
	if !ok || !got {
		t.Errorf("SetNeedsReauth call = (%v, %v), want (true, true)", got, ok)
	}
}

func TestMarkRevoked_ChecksOwnershipThenStamps(t *testing.T) {
	svc, store := newTestService(t)
	if _, err := svc.UpsertAccount(context.Background(), "op-1", testTokens(), nil); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	if err := svc.MarkRevoked(context.Background(), "op-1", "acct-open-1"); err != nil {
		t.Fatalf("MarkRevoked: %v", err)
	}
	if len(store.revokeCalls) != 1 || store.revokeCalls[0] != "acct-open-1" {
		t.Errorf("SetRevoked calls = %v, want one call for acct-open-1", store.revokeCalls)
	}
}

func TestMarkRevoked_ForeignOperatorNeverTouchesStore(t *testing.T) {
	svc, store := newTestService(t)
	if _, err := svc.UpsertAccount(context.Background(), "op-1", testTokens(), nil); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	if err := svc.MarkRevoked(context.Background(), "op-2", "acct-open-1"); err != errTestNotFound {
		t.Fatalf("err = %v, want the store denial unchanged", err)
	}
	if len(store.revokeCalls) != 0 {
		t.Errorf("SetRevoked ran for a foreign operator: %v", store.revokeCalls)
	}
}

func TestUpsertAccount_StoreFailurePropagates(t *testing.T) {
	svc, store := newTestService(t)
	sentinel := errors.New("store unavailable")
	store.upsertErr = sentinel
	if _, err := svc.UpsertAccount(context.Background(), "op-1", testTokens(), nil); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel store failure", err)
	}
}

func TestStoreTokens_EncryptsBeforeUpdate(t *testing.T) {
	svc, store := newTestService(t)
	if err := svc.StoreTokens(context.Background(), "acct-open-1", testTokens()); err != nil {
		t.Fatalf("StoreTokens: %v", err)
	}
	if len(store.updates) != 1 {
		t.Fatalf("UpdateTokens calls = %d, want 1", len(store.updates))
	}
	got := store.updates[0]
	if bytes.Contains(got.accessEnc, []byte(testAccess)) || bytes.Contains(got.refreshEnc, []byte(testRefresh)) {
		t.Error("stored ciphertext contains the plaintext token")
	}
	if got.keyID != defaultKeyID {
		t.Errorf("keyID = %q, want %q", got.keyID, defaultKeyID)
	}
}

func TestService_LogsNeverContainTokenMaterial(t *testing.T) {
	keyring, err := crypto.NewKeyring(map[string][]byte{defaultKeyID: bytes.Repeat([]byte("k"), 32)})
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := NewService(newFakeStore(), keyring, logger)

	if _, err := svc.UpsertAccount(context.Background(), "op-1", testTokens(), nil); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	if _, _, err := svc.RefreshMaterial(context.Background(), "op-1", "acct-open-1"); err != nil {
		t.Fatalf("RefreshMaterial: %v", err)
	}
	if err := svc.StoreTokens(context.Background(), "acct-open-1", testTokens()); err != nil {
		t.Fatalf("StoreTokens: %v", err)
	}
	if logs := buf.String(); bytes.Contains([]byte(logs), []byte(testAccess)) || bytes.Contains([]byte(logs), []byte(testRefresh)) {
		t.Errorf("log line leaked token material: %s", logs)
	}
}
