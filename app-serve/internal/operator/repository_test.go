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

// fixture applies the migrations and returns a repository over the shared
// pool. Each test removes only the ids it creates, so a concurrent package
// against the same database is never wiped by a table wide delete.
func fixture(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := openTestPool(t)
	if err := postgres.Up(ctx, pool, migrationsDir); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return NewRepository(pool), pool
}

// cleanupRow removes one operator row after the test, so the shared database
// is left as found without a table wide delete.
func cleanupRow(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id); err != nil {
			t.Logf("cleanup operator row %s: %v", id, err)
		}
	})
}

func TestEnsure_InsertsTheOperatorRow(t *testing.T) {
	repo, pool := fixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	cleanupRow(t, pool, id)
	email := id + "@example.com"

	if err := repo.Ensure(ctx, id, email); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	var gotID, gotEmail string
	if err := pool.QueryRow(ctx, `SELECT id, email FROM users WHERE id = $1`, id).
		Scan(&gotID, &gotEmail); err != nil {
		t.Fatalf("read operator row: %v", err)
	}
	if gotID != id || gotEmail != email {
		t.Fatalf("row = (%s, %s), want (%s, %s)", gotID, gotEmail, id, email)
	}
}

func TestEnsure_IsIdempotentAndUpdatesEmail(t *testing.T) {
	repo, pool := fixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	cleanupRow(t, pool, id)
	first := id + "-first@example.com"
	second := id + "-second@example.com"

	if err := repo.Ensure(ctx, id, first); err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	if err := repo.Ensure(ctx, id, second); err != nil {
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
	if email != second {
		t.Fatalf("email = %q, want the updated address", email)
	}
}
