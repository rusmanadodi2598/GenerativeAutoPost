// Package account holds the TikTok account aggregate and its persistence shapes.
//
// @file      internal/account/service_tokens_test.go
// @for       StoreTokens boundary tests and token lifetime validation.
// @uses      testing, context, bytes, time, internal/apierror, internal/tiktokclient
// @reason    A token write must pass the aggregate before it reaches the store.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package account

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// withLifetime copies the standard token set with the two lifetimes replaced.
func withLifetime(access, refresh int) tiktokclient.TokenSet {
	tokens := testTokens()
	tokens.ExpiresIn = access
	tokens.RefreshExpiresIn = refresh
	return tokens
}

// invalidLifetimes are the boundary values tokenExpiries must refuse.
func invalidLifetimes() []struct {
	name    string
	access  int
	refresh int
} {
	return []struct {
		name    string
		access  int
		refresh int
	}{
		{"zero access lifetime", 0, 3600},
		{"negative access lifetime", -1, 3600},
		{"refresh shorter than access", 3600, 60},
		{"zero refresh lifetime", 3600, 0},
		{"access past the ceiling", maxTokenSeconds + 1, maxTokenSeconds},
		{"refresh past the ceiling", 3600, maxTokenSeconds + 1},
	}
}

func TestStoreTokens_ForeignOperatorNeverWrites(t *testing.T) {
	svc, store := newTestService(t)
	if _, err := svc.UpsertAccount(context.Background(), "op-1", testTokens(), nil); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	err := svc.StoreTokens(context.Background(), "op-2", "acct-open-1", testTokens())
	if err != errTestNotFound {
		t.Fatalf("err = %v, want the store denial unchanged", err)
	}
	if len(store.updates) != 0 {
		t.Fatalf("UpdateTokens ran %d times for a foreign operator", len(store.updates))
	}
}

func TestStoreTokens_RevokedAccountConflicts(t *testing.T) {
	svc, store := newTestService(t)
	if _, err := svc.UpsertAccount(context.Background(), "op-1", testTokens(), nil); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	if err := svc.MarkRevoked(context.Background(), "op-1", "acct-open-1"); err != nil {
		t.Fatalf("MarkRevoked: %v", err)
	}
	err := svc.StoreTokens(context.Background(), "op-1", "acct-open-1", testTokens())
	wantCode(t, err, apierror.CodeConflict)
	if len(store.updates) != 0 {
		t.Fatalf("UpdateTokens ran %d times for a revoked account", len(store.updates))
	}
}

func TestStoreTokens_InvalidExpiryNeverWrites(t *testing.T) {
	for _, tt := range invalidLifetimes() {
		t.Run(tt.name, func(t *testing.T) {
			svc, store := newTestService(t)
			if _, err := svc.UpsertAccount(context.Background(), "op-1", testTokens(), nil); err != nil {
				t.Fatalf("UpsertAccount: %v", err)
			}
			err := svc.StoreTokens(context.Background(), "op-1", "acct-open-1", withLifetime(tt.access, tt.refresh))
			wantCode(t, err, apierror.CodeValidationFailed)
			if len(store.updates) != 0 {
				t.Fatalf("UpdateTokens ran %d times for an invalid expiry", len(store.updates))
			}
		})
	}
}

func TestUpsertAccount_InvalidExpiryNeverWrites(t *testing.T) {
	for _, tt := range invalidLifetimes() {
		t.Run(tt.name, func(t *testing.T) {
			svc, store := newTestService(t)
			_, err := svc.UpsertAccount(context.Background(), "op-1", withLifetime(tt.access, tt.refresh), nil)
			wantCode(t, err, apierror.CodeValidationFailed)
			if len(store.records) != 0 {
				t.Fatalf("UpsertEncrypted ran for an invalid expiry: %v", store.records)
			}
		})
	}
}

func TestTokenExpiries_RejectsInvalidLifetimes(t *testing.T) {
	for _, tt := range invalidLifetimes() {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := tokenExpiries(withLifetime(tt.access, tt.refresh))
			wantCode(t, err, apierror.CodeValidationFailed)
		})
	}
}

func TestTokenExpiries_AcceptsValidLifetimes(t *testing.T) {
	before := time.Now().UTC()
	access, refresh, err := tokenExpiries(withLifetime(3600, 86400))
	if err != nil {
		t.Fatalf("tokenExpiries: %v", err)
	}
	assertNear(t, "access", access, before.Add(time.Hour))
	assertNear(t, "refresh", refresh, before.Add(24*time.Hour))
}

func TestStoreTokens_WriteFailurePropagates(t *testing.T) {
	svc, store := newTestService(t)
	if _, err := svc.UpsertAccount(context.Background(), "op-1", testTokens(), nil); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	sentinel := errors.New("store unavailable")
	store.writeErr = sentinel
	if err := svc.StoreTokens(context.Background(), "op-1", "acct-open-1", testTokens()); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel store failure", err)
	}
}

func TestRefreshMaterial_CorruptCiphertextIsInternal(t *testing.T) {
	svc, store := newTestService(t)
	if _, err := svc.UpsertAccount(context.Background(), "op-1", testTokens(), nil); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	store.mu.Lock()
	rec := store.records["open-1"]
	rec.AccessEnc = append([]byte{}, rec.AccessEnc...)
	rec.AccessEnc[len(rec.AccessEnc)-1] ^= 0xff
	store.records["open-1"] = rec
	store.mu.Unlock()

	_, _, err := svc.RefreshMaterial(context.Background(), "op-1", "acct-open-1")
	wantCode(t, err, apierror.CodeInternal)
	msg := err.Error()
	if bytes.Contains([]byte(msg), []byte(testAccess)) || bytes.Contains([]byte(msg), []byte(defaultKeyID)) {
		t.Fatalf("decrypt failure leaked plaintext or key id: %q", msg)
	}
}
