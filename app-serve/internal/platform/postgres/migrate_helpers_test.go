//go:build integration

// Package postgres_test holds shared fixtures for the migration tests.
//
// @file      internal/platform/postgres/migrate_helpers_test.go
// @for       Pool, schema-reset, and inventory helpers for migration tests.
// @uses      testing, pgxpool
// @reason    Keeps test files under the line limit and holds the reset guard.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     util
// @stability stable
// @since     2026-10-12
package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationsDir = "../../../../migrations"

// testDatabaseName is the only database resetSchema may drop. A mistyped DSN
// pointed elsewhere is rejected before any destructive statement runs.
const testDatabaseName = "genauto_test"

func testPool(t *testing.T) *pgxpool.Pool {
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

// resetSchema returns the test database to an empty public schema. It refuses
// to run unless the connected database is exactly testDatabaseName.
func resetSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	var database string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil {
		t.Fatalf("read current_database: %v", err)
	}
	if database != testDatabaseName {
		t.Fatalf("refusing to reset schema: connected to %q, want %q", database, testDatabaseName)
	}

	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE"); err != nil {
		t.Fatalf("drop schema: %v", err)
	}
	if _, err := pool.Exec(ctx, "CREATE SCHEMA public"); err != nil {
		t.Fatalf("create schema: %v", err)
	}
}

func tableNames(t *testing.T, pool *pgxpool.Pool) map[string]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT table_name FROM information_schema.tables
		  WHERE table_schema = 'public' AND table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()
	names := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		names[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	return names
}

// migrationCount is the number of up pairs in the migrations directory, so a
// new additive pair does not silently change the expected journal size.
func migrationCount(t *testing.T) int {
	t.Helper()
	pairs, err := filepath.Glob(filepath.Join(migrationsDir, "*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	return len(pairs)
}

func columnNames(t *testing.T, pool *pgxpool.Pool, table string) map[string]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT column_name FROM information_schema.columns
		  WHERE table_schema = 'public' AND table_name = $1`, table)
	if err != nil {
		t.Fatalf("query columns of %s: %v", table, err)
	}
	defer rows.Close()
	names := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column name: %v", err)
		}
		names[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns: %v", err)
	}
	return names
}

func journalCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count journal: %v", err)
	}
	return count
}
