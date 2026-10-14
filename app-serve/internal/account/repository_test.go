//go:build integration

// Package account holds the TikTok account aggregate and its persistence shapes.
//
// @file      internal/account/repository_test.go
// @for       Integration tests for the pgx account repository against PostgreSQL.
// @uses      testing
// @reason    The upsert conflict rules, ownership deny by default, and ciphertext at rest are only observable against a real database.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package account

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/crypto"
)

func TestUpsertThenGetOwnedReturnsSameRow(t *testing.T) {
	repo, pool := fixture(t)
	ctx := context.Background()
	operatorID := seedUser(t, pool)
	rec, err := repo.UpsertEncrypted(ctx, seedRecord(t, operatorID, "open-upsert-get"))
	if err != nil {
		t.Fatalf("UpsertEncrypted: %v", err)
	}

	got, err := repo.GetOwned(ctx, operatorID, rec.ID)
	if err != nil {
		t.Fatalf("GetOwned: %v", err)
	}
	if got.OpenID != rec.OpenID || got.OperatorID != operatorID || got.KeyID != "key-1" {
		t.Fatalf("identity drift: %+v", got)
	}
	if len(got.Scopes) != 2 || got.Scopes[0] != "user.info.basic" {
		t.Fatalf("scopes drift: %v", got.Scopes)
	}
}

func TestUpsertConflictUpdatesTokensAndKeepsCreatedAt(t *testing.T) {
	repo, pool := fixture(t)
	ctx := context.Background()
	operatorID := seedUser(t, pool)
	first, err := repo.UpsertEncrypted(ctx, seedRecord(t, operatorID, "open-upsert-conflict"))
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	second := seedRecord(t, operatorID, "open-upsert-conflict")
	second.AccessEnc = []byte("access-ciphertext-v2")
	second.KeyID = "key-2"
	updated, err := repo.UpsertEncrypted(ctx, second)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if updated.ID != first.ID {
		t.Fatalf("row id changed: %q -> %q", first.ID, updated.ID)
	}
	if !updated.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("created_at moved: %v -> %v", first.CreatedAt, updated.CreatedAt)
	}
	if updated.KeyID != "key-2" || !bytes.Equal(updated.AccessEnc, []byte("access-ciphertext-v2")) {
		t.Fatalf("token columns did not rotate")
	}
}

func TestUpsertConflictDoesNotUnrevoke(t *testing.T) {
	repo, pool := fixture(t)
	ctx := context.Background()
	operatorID := seedUser(t, pool)
	rec, err := repo.UpsertEncrypted(ctx, seedRecord(t, operatorID, "open-upsert-revoked"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	revokedAt := time.Now().UTC().Truncate(time.Millisecond)
	if err := repo.SetRevoked(ctx, rec.ID, revokedAt); err != nil {
		t.Fatalf("SetRevoked: %v", err)
	}
	if _, err := repo.UpsertEncrypted(ctx, seedRecord(t, operatorID, "open-upsert-revoked")); err != nil {
		t.Fatalf("upsert after revoke: %v", err)
	}

	got, err := repo.GetOwned(ctx, operatorID, rec.ID)
	if err != nil {
		t.Fatalf("GetOwned: %v", err)
	}
	if got.RevokedAt == nil {
		t.Fatal("revoked_at was cleared by a repeat upsert")
	}
	if !got.RevokedAt.UTC().Equal(revokedAt) {
		t.Fatalf("revoked_at moved: want %v, got %v", revokedAt, got.RevokedAt)
	}
}

func TestGetOwnedWithForeignOperatorIsNotFound(t *testing.T) {
	repo, pool := fixture(t)
	ctx := context.Background()
	ownerID := seedUser(t, pool)
	foreignID := seedUser(t, pool)
	rec, err := repo.UpsertEncrypted(ctx, seedRecord(t, ownerID, "open-foreign"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if _, err := repo.GetOwned(ctx, foreignID, rec.ID); err == nil {
		t.Fatal("foreign operator read the account")
	} else {
		wantNotFound(t, err)
	}
}

func TestTokensAreCiphertextAtRest(t *testing.T) {
	repo, pool := fixture(t)
	ctx := context.Background()
	operatorID := seedUser(t, pool)
	keyring, err := crypto.NewKeyring(map[string][]byte{"key-1": bytes.Repeat([]byte{7}, 32)})
	if err != nil {
		t.Fatalf("build keyring: %v", err)
	}

	const accessToken, refreshToken = "access-plaintext-token", "refresh-plaintext-token"
	rec := seedRecord(t, operatorID, "open-ciphertext")
	if rec.AccessEnc, _, err = keyring.Encrypt(ctx, []byte(accessToken), "key-1"); err != nil {
		t.Fatalf("seal access token: %v", err)
	}
	if rec.RefreshEnc, _, err = keyring.Encrypt(ctx, []byte(refreshToken), "key-1"); err != nil {
		t.Fatalf("seal refresh token: %v", err)
	}
	if _, err = repo.UpsertEncrypted(ctx, rec); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var access, refresh []byte
	if err := pool.QueryRow(ctx,
		`SELECT access_token_enc, refresh_token_enc FROM tiktok_accounts WHERE id = $1`, rec.ID).
		Scan(&access, &refresh); err != nil {
		t.Fatalf("raw token read: %v", err)
	}
	for name, blob := range map[string][]byte{"access_token_enc": access, "refresh_token_enc": refresh} {
		if len(blob) == 0 {
			t.Fatalf("%s is empty", name)
		}
		for _, plaintext := range []string{accessToken, refreshToken} {
			if bytes.Contains(blob, []byte(plaintext)) {
				t.Fatalf("%s stores the plaintext %q", name, plaintext)
			}
		}
	}
}

func TestSetNeedsReauthThenReadIsTrue(t *testing.T) {
	repo, pool := fixture(t)
	ctx := context.Background()
	operatorID := seedUser(t, pool)
	rec, err := repo.UpsertEncrypted(ctx, seedRecord(t, operatorID, "open-reauth"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := repo.SetNeedsReauth(ctx, rec.ID, true); err != nil {
		t.Fatalf("SetNeedsReauth: %v", err)
	}
	got, err := repo.GetOwned(ctx, operatorID, rec.ID)
	if err != nil {
		t.Fatalf("GetOwned: %v", err)
	}
	if !got.NeedsReauth {
		t.Fatal("needs_reauth did not stick")
	}
}

func TestWritesOnUnknownAccountAreNotFound(t *testing.T) {
	repo, _ := fixture(t)
	ctx := context.Background()
	unknown, err := NewID()
	if err != nil {
		t.Fatalf("generate id: %v", err)
	}
	expiry := time.Now().UTC().Add(time.Hour)

	writes := map[string]func() error{
		"UpdateTokens": func() error {
			return repo.UpdateTokens(ctx, unknown, []byte("a"), []byte("r"), "key-9", expiry, expiry)
		},
		"SetNeedsReauth": func() error { return repo.SetNeedsReauth(ctx, unknown, true) },
		"SetRevoked":     func() error { return repo.SetRevoked(ctx, unknown, expiry) },
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) { wantNotFound(t, write()) })
	}
}
