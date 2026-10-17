//go:build integration

// Package operator proves the operator row store against PostgreSQL.
//
// @file      internal/operator/repository_test.go
// @for       Integration tests for the operator users row repository.
// @uses      testing, context, os, github.com/google/uuid, github.com/jackc/pgx/v5/pgxpool, internal/platform/postgres
// @reason    The upsert conflict rule only holds against a real database.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-17
package operator

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/postgres"
)

// migrationsDir is the repository migrations directory, three levels up from
// internal/operator.
const migrationsDir = "../../../migrations"

// testDatabaseName is the only database these tests may run against. A
// mistyped DSN pointed elsewhere is rejected before any row is touched.
const testDatabaseName = "genauto_test"

// openTestPool connects to the test database named by GENAUTO_TEST_POSTGRES_DSN.
// The integration build tag is the guard; an unset DSN is a hard failure.
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

	var database string
	if err := pool.QueryRow(context.Background(), "SELECT current_database()").Scan(&database); err != nil {
		t.Fatalf("read current_database: %v", err)
	}
	if database != testDatabaseName {
		t.Fatalf("refusing to run against %q, want %q", database, testDatabaseName)
	}
	return pool
}

// fixture applies the migrations, clears the rows these tests own, and returns
// a repository over the shared pool.
func fixture(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := openTestPool(t)
	if err := postgres.Up(ctx, pool, migrationsDir); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users`); err != nil {
		t.Fatalf("clear users: %v", err)
	}
	return NewRepository(pool), pool
}

func TestEnsure_InsertsTheOperatorRow(t *testing.T) {
	repo, pool := fixture(t)
	ctx := context.Background()
	id := uuid.NewString()

	if err := repo.Ensure(ctx, id, "operator@example.com"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	var gotID, gotEmail string
	if err := pool.QueryRow(ctx, `SELECT id, email FROM users WHERE id = $1`, id).
		Scan(&gotID, &gotEmail); err != nil {
		t.Fatalf("read operator row: %v", err)
	}
	if gotID != id || gotEmail != "operator@example.com" {
		t.Fatalf("row = (%s, %s), want (%s, operator@example.com)", gotID, gotEmail, id)
	}
}

func TestEnsure_IsIdempotentAndUpdatesEmail(t *testing.T) {
	repo, pool := fixture(t)
	ctx := context.Background()
	id := uuid.NewString()

	if err := repo.Ensure(ctx, id, "first@example.com"); err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	if err := repo.Ensure(ctx, id, "second@example.com"); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`, id).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("rows = %d, want 1", count)
	}
	var email string
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, id).Scan(&email); err != nil {
		t.Fatalf("read email: %v", err)
	}
	if email != "second@example.com" {
		t.Fatalf("email = %q, want the updated address", email)
	}
}
