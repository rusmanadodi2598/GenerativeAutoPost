//go:build integration

// Package postgres exercises the migration runner lock against PostgreSQL.
//
// @file      internal/platform/postgres/migrate_lock_test.go
// @for       Integration test for the bounded advisory lock wait.
// @uses      testing, time, pgx, pgxpool
// @reason    A stuck migration holder must fail a booting replica promptly.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     util
// @stability stable
// @since     2026-10-16
package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	lockTestMigrationsDir = "../../../../migrations"
	lockTestDatabase      = "genauto_test"
)

// TestUpFailsFastWhenMigrationLockIsHeld proves lock_timeout bounds the
// advisory lock wait: a second runner returns a named error instead of
// blocking behind the holder.
func TestUpFailsFastWhenMigrationLockIsHeld(t *testing.T) {
	dsn := os.Getenv("GENAUTO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("GENAUTO_TEST_POSTGRES_DSN is required for integration tests")
	}
	ctx := context.Background()

	holder, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect holder: %v", err)
	}
	defer func() { _ = holder.Close(ctx) }()

	// The lock is held for the whole test, so nothing should apply here; the
	// guard keeps a future change from turning that into a real migration run
	// against whatever database the DSN names.
	var database string
	if err := holder.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil {
		t.Fatalf("read current_database: %v", err)
	}
	if database != lockTestDatabase {
		t.Fatalf("refusing to run against %q, want %q", database, lockTestDatabase)
	}

	if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		t.Fatalf("hold migration lock: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	start := time.Now()
	err = up(ctx, pool, lockTestMigrationsDir, 200*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("up acquired a held migration lock; want a lock timeout")
	}
	if !strings.Contains(err.Error(), "migration lock") {
		t.Errorf("error = %v, want it to name the migration lock", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("up waited %s; the lock timeout did not bound the wait", elapsed)
	}
}
