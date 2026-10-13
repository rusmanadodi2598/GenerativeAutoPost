//go:build integration

// Package postgres_test exercises the migration runner against PostgreSQL.
//
// @file      internal/platform/postgres/migrate_test.go
// @for       Integration tests for migrate.Up and migrate.Down.
// @uses      testing, pgxpool, internal/platform/postgres
// @reason    Proves the schema, indexes, and journal work on a real database.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     util
// @stability stable
// @since     2026-10-12
package postgres_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/postgres"
)

const migrationsDir = "../../../../migrations"

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

// resetSchema returns the test database to an empty public schema.
func resetSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
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

func journalCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count journal: %v", err)
	}
	return count
}

func TestUpCreatesDomainTablesAndJournal(t *testing.T) {
	pool := testPool(t)
	resetSchema(t, pool)

	if err := postgres.Up(context.Background(), pool, migrationsDir); err != nil {
		t.Fatalf("Up: %v", err)
	}

	names := tableNames(t, pool)
	want := []string{
		"users", "sessions", "tiktok_accounts", "publish_attempts",
		"publish_chunks", "webhook_events", "outbox",
		"video_stats", "creator_info_snapshots", "schema_migrations",
	}
	for _, name := range want {
		if !names[name] {
			t.Errorf("table %q is missing after Up", name)
		}
	}
	if got := journalCount(t, pool); got != 3 {
		t.Errorf("journal rows after Up = %d, want 3", got)
	}
}

func TestUpCreatesRequiredIndexes(t *testing.T) {
	pool := testPool(t)
	resetSchema(t, pool)

	if err := postgres.Up(context.Background(), pool, migrationsDir); err != nil {
		t.Fatalf("Up: %v", err)
	}

	rows, err := pool.Query(context.Background(),
		"SELECT indexname, indexdef FROM pg_indexes WHERE schemaname = 'public'")
	if err != nil {
		t.Fatalf("query indexes: %v", err)
	}
	defer rows.Close()
	indexes := make(map[string]string)
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		indexes[name] = def
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate indexes: %v", err)
	}

	want := []string{
		"ux_tiktok_accounts_open_id",
		"ux_publish_attempts_publish_id",
		"ix_publish_attempts_account_status",
		"ux_webhook_events_dedupe_key",
	}
	for _, name := range want {
		if _, ok := indexes[name]; !ok {
			t.Errorf("index %q is missing after Up", name)
		}
	}
	def, ok := indexes["ix_video_stats_video_fetched"]
	if !ok {
		t.Fatalf("index %q is missing after Up", "ix_video_stats_video_fetched")
	}
	if !strings.Contains(def, "fetched_at DESC") {
		t.Errorf("video_stats index definition lacks DESC: %s", def)
	}
}

func TestUpTwiceIsNoOp(t *testing.T) {
	pool := testPool(t)
	resetSchema(t, pool)
	ctx := context.Background()

	if err := postgres.Up(ctx, pool, migrationsDir); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	if got := journalCount(t, pool); got != 3 {
		t.Fatalf("journal rows after first Up = %d, want 3", got)
	}
	if err := postgres.Up(ctx, pool, migrationsDir); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if got := journalCount(t, pool); got != 3 {
		t.Errorf("journal rows after second Up = %d, want 3", got)
	}
	if !tableNames(t, pool)["users"] {
		t.Error("users table disappeared after a repeated Up")
	}
}

func TestDownRollsBackOneStep(t *testing.T) {
	pool := testPool(t)
	resetSchema(t, pool)
	ctx := context.Background()

	if err := postgres.Up(ctx, pool, migrationsDir); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := postgres.Down(ctx, pool, migrationsDir, 1); err != nil {
		t.Fatalf("Down: %v", err)
	}

	names := tableNames(t, pool)
	for _, gone := range []string{"video_stats", "creator_info_snapshots"} {
		if names[gone] {
			t.Errorf("table %q still present after one Down step", gone)
		}
	}
	if !names["users"] {
		t.Error("users table was rolled back by a one-step Down")
	}
	if got := journalCount(t, pool); got != 2 {
		t.Errorf("journal rows after one Down step = %d, want 2", got)
	}
}

func TestDownWithZeroStepsIsNoOp(t *testing.T) {
	pool := testPool(t)
	resetSchema(t, pool)
	ctx := context.Background()

	if err := postgres.Up(ctx, pool, migrationsDir); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := postgres.Down(ctx, pool, migrationsDir, 0); err != nil {
		t.Fatalf("Down with zero steps: %v", err)
	}
	if got := journalCount(t, pool); got != 3 {
		t.Errorf("journal rows after a zero-step Down = %d, want 3", got)
	}
}

// A full Down must return every domain table so the up files can re-apply,
// which is the working rollback AGENTS.md 1.7 asks for.
func TestDownAllStepsAllowsReapply(t *testing.T) {
	pool := testPool(t)
	resetSchema(t, pool)
	ctx := context.Background()

	if err := postgres.Up(ctx, pool, migrationsDir); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := postgres.Down(ctx, pool, migrationsDir, 3); err != nil {
		t.Fatalf("full Down: %v", err)
	}
	for name := range tableNames(t, pool) {
		if name != "schema_migrations" {
			t.Errorf("table %q survived a full Down", name)
		}
	}
	if err := postgres.Up(ctx, pool, migrationsDir); err != nil {
		t.Fatalf("reapply Up: %v", err)
	}
	if !tableNames(t, pool)["users"] {
		t.Error("users table missing after reapply")
	}
}
