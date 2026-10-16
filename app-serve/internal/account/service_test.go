// Package account holds the TikTok account aggregate and its persistence shapes.
//
// @file      internal/account/service_test.go
// @for       Token vault tests and the in-memory store fake.
// @uses      testing, context, bytes, log/slog, time, errors, sync, internal/apierror, internal/platform/crypto
// @reason    The vault must encrypt at rest and never leak plaintext to logs.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package account

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/crypto"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

const (
	testAccess  = "act.plaintext-access-0123456789"
	testRefresh = "rft.plaintext-refresh-9876543210"
)

// tokenUpdate captures one UpdateTokens call for assertions.
type tokenUpdate struct {
	accountID  string
	accessEnc  []byte
	refreshEnc []byte
	keyID      string
	accessExp  time.Time
	refreshExp time.Time
}

// newTestService builds a vault over a fresh keyring and fake store.
func newTestService(t *testing.T) (*Service, *fakeStore) {
	t.Helper()
	keyring, err := crypto.NewKeyring(map[string][]byte{defaultKeyID: bytes.Repeat([]byte("k"), 32)})
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	store := newFakeStore()
	return NewService(store, keyring, defaultKeyID, discardLogger()), store
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}

func testTokens() tiktokclient.TokenSet {
	return tiktokclient.TokenSet{
		AccessToken:      testAccess,
		RefreshToken:     testRefresh,
		ExpiresIn:        3600,
		RefreshExpiresIn: 86400,
		OpenID:           "open-1",
	}
}

func TestUpsertAccount_StoresCiphertextNotPlaintext(t *testing.T) {
	svc, store := newTestService(t)
	before := time.Now().UTC()

	id, err := svc.UpsertAccount(context.Background(), "op-1", testTokens(), []string{"user.info.basic"})
	if err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	if id == "" {
		t.Fatal("UpsertAccount returned an empty id")
	}
	rec := store.records["open-1"]
	if bytes.Contains(rec.AccessEnc, []byte(testAccess)) {
		t.Error("access ciphertext contains the plaintext token")
	}
	if bytes.Contains(rec.RefreshEnc, []byte(testRefresh)) {
		t.Error("refresh ciphertext contains the plaintext token")
	}
	if bytes.Equal(rec.AccessEnc, []byte(testAccess)) || bytes.Equal(rec.RefreshEnc, []byte(testRefresh)) {
		t.Error("ciphertext equals the plaintext token")
	}
	assertNear(t, "access", rec.AccessExpiry, before.Add(time.Hour))
	assertNear(t, "refresh", rec.RefreshExpiry, before.Add(24*time.Hour))
	if rec.KeyID != defaultKeyID {
		t.Errorf("KeyID = %q, want %q", rec.KeyID, defaultKeyID)
	}
}

func TestUpsertAccount_SameOpenIDKeepsSameID(t *testing.T) {
	svc, _ := newTestService(t)
	first, err := svc.UpsertAccount(context.Background(), "op-1", testTokens(), nil)
	if err != nil {
		t.Fatalf("first UpsertAccount: %v", err)
	}
	second, err := svc.UpsertAccount(context.Background(), "op-1", testTokens(), nil)
	if err != nil {
		t.Fatalf("second UpsertAccount: %v", err)
	}
	if first != second {
		t.Errorf("ids differ: %q then %q", first, second)
	}
}

func TestRefreshMaterial_RoundTripsPlaintext(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.UpsertAccount(context.Background(), "op-1", testTokens(), nil); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	refresh, access, err := svc.RefreshMaterial(context.Background(), "op-1", "acct-open-1")
	if err != nil {
		t.Fatalf("RefreshMaterial: %v", err)
	}
	if access != testAccess || refresh != testRefresh {
		t.Errorf("round trip mismatch: access %q refresh %q", access, refresh)
	}
}

func TestRefreshMaterial_PropagatesStoreDenial(t *testing.T) {
	svc, _ := newTestService(t)
	_, _, err := svc.RefreshMaterial(context.Background(), "op-1", "acct-open-1")
	if err != errTestNotFound {
		t.Fatalf("err = %v, want the store denial unchanged", err)
	}
}

// assertNear fails when a timestamp drifts more than a few seconds from want.
func assertNear(t *testing.T, label string, got, want time.Time) {
	t.Helper()
	if delta := got.Sub(want); delta < -5*time.Second || delta > 5*time.Second {
		t.Errorf("%s expiry off by %s (got %s, want %s)", label, delta, got, want)
	}
}
