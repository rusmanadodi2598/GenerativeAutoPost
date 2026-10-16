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
	"strings"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/postgres"
)

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
	if got, want := journalCount(t, pool), migrationCount(t); got != want {
		t.Errorf("journal rows after Up = %d, want %d", got, want)
	}
	if cols := columnNames(t, pool, "tiktok_accounts"); !cols["needs_reauth"] {
		t.Error("tiktok_accounts is missing needs_reauth after Up")
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

	// The three mandated indexes must be unique, not merely present. Dropping
	// UNIQUE would still leave a row in pg_indexes, so indexdef is asserted.
	unique := []string{
		"ux_tiktok_accounts_open_id",
		"ux_publish_attempts_publish_id",
		"ux_webhook_events_dedupe_key",
	}
	for _, name := range unique {
		def, ok := indexes[name]
		if !ok {
			t.Errorf("index %q is missing after Up", name)
			continue
		}
		if !strings.HasPrefix(def, "CREATE UNIQUE INDEX") {
			t.Errorf("index %q is not unique: %s", name, def)
		}
	}

	if _, ok := indexes["ix_publish_attempts_account_status"]; !ok {
		t.Errorf("index %q is missing after Up", "ix_publish_attempts_account_status")
	}
	def, ok := indexes["ix_video_stats_video_fetched"]
	if !ok {
		t.Fatalf("index %q is missing after Up", "ix_video_stats_video_fetched")
	}
	if strings.HasPrefix(def, "CREATE UNIQUE INDEX") {
		t.Errorf("video_stats index must stay non-unique: %s", def)
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
	want := migrationCount(t)
	if got := journalCount(t, pool); got != want {
		t.Fatalf("journal rows after first Up = %d, want %d", got, want)
	}
	if err := postgres.Up(ctx, pool, migrationsDir); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if got := journalCount(t, pool); got != want {
		t.Errorf("journal rows after second Up = %d, want %d", got, want)
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

	// One Down step reverses the newest pair, the reconcile failure counter;
	// the needs_reauth column and the insight tables belong to older pairs.
	if cols := columnNames(t, pool, "publish_attempts"); cols["reconcile_failures"] {
		t.Error("reconcile_failures survived a one-step Down")
	}
	if cols := columnNames(t, pool, "tiktok_accounts"); !cols["needs_reauth"] {
		t.Error("needs_reauth was rolled back by a one-step Down")
	}
	names := tableNames(t, pool)
	if !names["video_stats"] {
		t.Error("video_stats was rolled back by a one-step Down")
	}
	if !names["users"] {
		t.Error("users table was rolled back by a one-step Down")
	}
	if got, want := journalCount(t, pool), migrationCount(t)-1; got != want {
		t.Errorf("journal rows after one Down step = %d, want %d", got, want)
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
	if got, want := journalCount(t, pool), migrationCount(t); got != want {
		t.Errorf("journal rows after a zero-step Down = %d, want %d", got, want)
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
	if err := postgres.Down(ctx, pool, migrationsDir, migrationCount(t)); err != nil {
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
