// Package account tests the token rotation and revoke rules on the aggregate.
//
// @file      internal/account/account_test.go
// @for       Unit tests for Account rotation, revoke, scope, and view rules.
// @uses      testing, time, slices, internal/apierror
// @reason    Token mutation rules are security relevant and need pinned behavior.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     domain
// @stability stable
// @since     2026-10-12
package account

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// wantCode asserts a typed error carries the expected catalog code.
func wantCode(t *testing.T, err error, code apierror.Code) {
	t.Helper()
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not *apierror.Error", err)
	}
	if apiErr.Code != code {
		t.Fatalf("code = %s, want %s", apiErr.Code, code)
	}
}

// testRecord builds a stored account with valid material and two scopes.
func testRecord(now time.Time) Record {
	return Record{
		ID:            "acct-1",
		OperatorID:    "op-1",
		OpenID:        "open-1",
		UnionID:       "union-1",
		DisplayName:   "Kentang Studio",
		AvatarURL:     "https://cdn.example/avatar.png",
		Scopes:        []string{"user.info.basic", "video.publish"},
		AccessEnc:     []byte("access-old"),
		AccessExpiry:  now.Add(time.Hour),
		RefreshEnc:    []byte("refresh-old"),
		RefreshExpiry: now.Add(48 * time.Hour),
		KeyID:         "key-1",
		CreatedAt:     now.Add(-time.Hour),
	}
}

func TestRotateTokensReplacesMaterialAndClearsReauth(t *testing.T) {
	now := time.Now().UTC()
	rec := testRecord(now)
	rec.NeedsReauth = true
	acc := newAccount(rec)
	err := acc.RotateTokens([]byte("access-new"), []byte("refresh-new"), "key-1", now.Add(2*time.Hour), now.Add(72*time.Hour))
	if err != nil {
		t.Fatalf("RotateTokens: %v", err)
	}

	access, key := acc.EncryptedAccess()
	if string(access) != "access-new" || key != "key-1" {
		t.Fatalf("access = %q key = %q, want new material under key-1", access, key)
	}
	refresh, key := acc.EncryptedRefresh()
	if string(refresh) != "refresh-new" || key != "key-1" {
		t.Fatalf("refresh = %q key = %q, want new material under key-1", refresh, key)
	}
	if acc.NeedsReauth() {
		t.Fatal("NeedsReauth stayed true after a successful rotation")
	}
	if acc.Revoked() {
		t.Fatal("Revoked became true after a successful rotation")
	}
}

func TestRotateTokensRejectsInvalidMaterial(t *testing.T) {
	now := time.Now().UTC()
	future := now.Add(time.Hour)
	later := now.Add(48 * time.Hour)
	records := []struct {
		name       string
		accessEnc  []byte
		refreshEnc []byte
		keyID      string
		accessExp  time.Time
		refreshExp time.Time
	}{
		{"nil access material", nil, []byte("refresh"), "key-1", future, later},
		{"nil refresh material", []byte("access"), nil, "key-1", future, later},
		{"key id differs from stored key", []byte("access"), []byte("refresh"), "key-2", future, later},
		{"access expiry in the past", []byte("access"), []byte("refresh"), "key-1", now.Add(-time.Hour), later},
		{"access expiry not in the future", []byte("access"), []byte("refresh"), "key-1", time.Now().UTC(), later},
		{"refresh expiry before access expiry", []byte("access"), []byte("refresh"), "key-1", future, now.Add(30 * time.Minute)},
	}
	for _, tt := range records {
		t.Run(tt.name, func(t *testing.T) {
			acc := newAccount(testRecord(now))
			err := acc.RotateTokens(tt.accessEnc, tt.refreshEnc, tt.keyID, tt.accessExp, tt.refreshExp)
			wantCode(t, err, apierror.CodeValidationFailed)
			if got, _ := acc.EncryptedAccess(); string(got) != "access-old" {
				t.Fatalf("access material = %q, want the stored value untouched", got)
			}
			if got, _ := acc.EncryptedRefresh(); string(got) != "refresh-old" {
				t.Fatalf("refresh material = %q, want the stored value untouched", got)
			}
		})
	}
}

func TestRotateTokensOnRevokedAccountConflicts(t *testing.T) {
	now := time.Now().UTC()
	acc := newAccount(testRecord(now))
	if err := acc.Revoke(now); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	err := acc.RotateTokens([]byte("access"), []byte("refresh"), "key-1", now.Add(time.Hour), now.Add(time.Hour*2))
	wantCode(t, err, apierror.CodeConflict)
}

func TestRevokeClearsMaterialAndIsIdempotent(t *testing.T) {
	now := time.Now().UTC()
	acc := newAccount(testRecord(now))

	if err := acc.Revoke(now); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if !acc.Revoked() {
		t.Fatal("Revoked = false after Revoke")
	}
	if access, _ := acc.EncryptedAccess(); access != nil {
		t.Fatalf("access material = %q, want nil after revoke", access)
	}
	if refresh, _ := acc.EncryptedRefresh(); refresh != nil {
		t.Fatalf("refresh material = %q, want nil after revoke", refresh)
	}

	if err := acc.Revoke(now.Add(time.Hour)); err != nil {
		t.Fatalf("second Revoke = %v, want nil", err)
	}
}

func TestMarkNeedsReauthSetsFlagAndRejectsRevoked(t *testing.T) {
	now := time.Now().UTC()
	acc := newAccount(testRecord(now))
	if err := acc.MarkNeedsReauth(); err != nil {
		t.Fatalf("MarkNeedsReauth: %v", err)
	}
	if !acc.NeedsReauth() {
		t.Fatal("NeedsReauth = false after MarkNeedsReauth")
	}

	revoked := newAccount(testRecord(now))
	if err := revoked.Revoke(now); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	wantCode(t, revoked.MarkNeedsReauth(), apierror.CodeConflict)
}

func TestHasScopeMatchesExactGrantOnly(t *testing.T) {
	acc := newAccount(testRecord(time.Now().UTC()))
	tests := []struct {
		scope string
		want  bool
	}{
		{"video.publish", true},
		{"user.info.basic", true},
		{"user.info", false},
		{"video", false},
		{"video.publish.extra", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := acc.HasScope(tt.scope); got != tt.want {
			t.Errorf("HasScope(%q) = %v, want %v", tt.scope, got, tt.want)
		}
	}

	partial := newAccount(testRecord(time.Now().UTC()))
	partial.scopes = []string{"video.publish"}
	if !partial.HasScope("video.publish") || partial.HasScope("user.info.basic") {
		t.Fatal("HasScope did not honor a partial grant set")
	}
}

func TestScopesReturnsCopy(t *testing.T) {
	acc := newAccount(testRecord(time.Now().UTC()))
	first := acc.Scopes()
	first[0] = "tampered"

	second := acc.Scopes()
	if second[0] != "user.info.basic" {
		t.Fatalf("Scopes()[0] = %q after mutating an earlier copy, want the stored grant", second[0])
	}
}

func TestViewMapsEveryExportedField(t *testing.T) {
	now := time.Now().UTC()
	rec := testRecord(now)
	acc := newAccount(rec)

	view := acc.View()
	if view.ID != rec.ID || view.OpenID != rec.OpenID {
		t.Fatalf("view identity = %q/%q, want %q/%q", view.ID, view.OpenID, rec.ID, rec.OpenID)
	}
	if view.DisplayName != rec.DisplayName || view.AvatarURL != rec.AvatarURL {
		t.Fatalf("view profile = %q/%q, want %q/%q", view.DisplayName, view.AvatarURL, rec.DisplayName, rec.AvatarURL)
	}
	if !slices.Equal(view.Scopes, rec.Scopes) {
		t.Fatalf("view scopes = %v, want %v", view.Scopes, rec.Scopes)
	}
	if view.NeedsReauth {
		t.Fatal("view NeedsReauth = true for a record without the flag")
	}
	if view.Revoked {
		t.Fatal("view Revoked = true for a record without a revoke timestamp")
	}
	if !view.CreatedAt.Equal(rec.CreatedAt) {
		t.Fatalf("view created at = %s, want %s", view.CreatedAt, rec.CreatedAt)
	}
	if err := acc.Revoke(now); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if !acc.View().Revoked {
		t.Fatal("view Revoked = false after the account was revoked")
	}
}

func TestEncryptedAccessorsReturnStoredCopyAndKey(t *testing.T) {
	now := time.Now().UTC()
	acc := newAccount(testRecord(now))
	access, key := acc.EncryptedAccess()
	if string(access) != "access-old" || key != "key-1" {
		t.Fatalf("EncryptedAccess = %q/%q, want access-old/key-1", access, key)
	}
	refresh, key := acc.EncryptedRefresh()
	if string(refresh) != "refresh-old" || key != "key-1" {
		t.Fatalf("EncryptedRefresh = %q/%q, want refresh-old/key-1", refresh, key)
	}

	access[0] = 'X'
	again, _ := acc.EncryptedAccess()
	if string(again) != "access-old" {
		t.Fatalf("EncryptedAccess = %q after mutating an earlier copy, want the stored bytes", again)
	}
}
