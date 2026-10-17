//go:build integration

// Package account holds the TikTok account aggregate and its persistence shapes.
//
// @file      internal/account/repository_fixtures_test.go
// @for       Shared fixtures for the account repository integration tests.
// @uses      testing, pgxpool, internal/platform/postgres
// @reason    Holds the pool guard, migration setup, and row builders that every case needs.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     util
// @stability stable
// @since     2026-10-12
package account

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/postgres"
)

// migrationsDir holds the same files the migration tests apply. From
// internal/account it sits three levels up, not four as it does under platform/postgres.
const migrationsDir = "../../../migrations"

// openTestPool connects to the test database named by GENAUTO_TEST_POSTGRES_DSN.
// The integration build tag is the guard; an unset DSN is a hard failure, not a skip.
func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GENAUTO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("GENAUTO_TEST_POSTGRES_DSN is required for integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// fixture applies the migrations once, clears the rows these tests own, and
// returns a repository over the shared pool. The reset keeps test order irrelevant.
func fixture(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := openTestPool(t)
	if err := postgres.Up(ctx, pool, migrationsDir); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM tiktok_accounts`); err != nil {
		t.Fatalf("clear tiktok_accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users`); err != nil {
		t.Fatalf("clear users: %v", err)
	}
	return NewRepository(pool), pool
}

// seedUser inserts the operator row the account foreign key points at.
func seedUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	id, err := NewID()
	if err != nil {
		t.Fatalf("generate user id: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email) VALUES ($1, $2)`,
		id, id+"@example.test"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

// seedRecord is a valid account row; the repository fills nothing implicitly.
func seedRecord(t *testing.T, operatorID, openID string) Record {
	t.Helper()
	id, err := NewID()
	if err != nil {
		t.Fatalf("generate account id: %v", err)
	}
	return Record{
		ID:            id,
		OperatorID:    operatorID,
		OpenID:        openID,
		DisplayName:   "Creator",
		Scopes:        []string{"user.info.basic", "video.publish"},
		AccessEnc:     []byte("access-ciphertext-v1"),
		AccessExpiry:  time.Now().UTC().Add(24 * time.Hour),
		RefreshEnc:    []byte("refresh-ciphertext-v1"),
		RefreshExpiry: time.Now().UTC().Add(30 * 24 * time.Hour),
		KeyID:         "key-1",
	}
}

// wantNotFound asserts the deny by default answer for an unknown or foreign id.
func wantNotFound(t *testing.T, err error) {
	t.Helper()
	var appErr *apierror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("want an apierror, got %T: %v", err, err)
	}
	if appErr.Code != apierror.CodeNotFound {
		t.Fatalf("want NOT_FOUND, got %q", appErr.Code)
	}
}
