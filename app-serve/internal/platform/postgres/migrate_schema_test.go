//go:build integration

// Package postgres_test asserts migration columns against 001 section 5.
//
// @file      internal/platform/postgres/migrate_schema_test.go
// @for       Column coverage and type checks for the migrated tables.
// @uses      testing, pgxpool, internal/platform/postgres
// @reason    Locks the schema to the spec column list and types, not just names.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     util
// @stability stable
// @since     2026-10-12
package postgres_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/postgres"
)

func TestUpCreatesSectionFiveColumns(t *testing.T) {
	pool := testPool(t)
	resetSchema(t, pool)

	if err := postgres.Up(context.Background(), pool, migrationsDir); err != nil {
		t.Fatalf("Up: %v", err)
	}

	columns := columnSet(t, pool)
	want := map[string][]string{
		"users":                  {"id", "email", "created_at"},
		"sessions":               {"id", "user_id", "expires_at", "csrf_token"},
		"tiktok_accounts":        {"id", "user_id", "open_id", "union_id", "display_name", "avatar_url", "scopes", "access_token_enc", "access_token_expires_at", "refresh_token_enc", "refresh_expires_at", "token_key_id", "revoked_at"},
		"publish_attempts":       {"id", "account_id", "publish_id", "post_mode", "media_type", "source", "status", "privacy_level", "title", "description", "fail_reason", "post_id", "uploaded_bytes", "created_at", "completed_at"},
		"publish_chunks":         {"id", "publish_attempt_id", "chunk_index", "byte_start", "byte_end", "content_range", "uploaded_at"},
		"webhook_events":         {"id", "dedupe_key", "event_type", "publish_id", "post_id", "payload", "received_at", "processed_at"},
		"video_stats":            {"id", "video_id", "view_count", "like_count", "comment_count", "share_count", "fetched_at"},
		"creator_info_snapshots": {"id", "account_id", "privacy_level_options", "comment_disabled", "duet_disabled", "stitch_disabled", "max_video_post_duration_sec", "fetched_at"},
		"outbox":                 {"id", "aggregate_type", "aggregate_id", "event_type", "payload", "published_at"},
	}
	for table, cols := range want {
		for _, col := range cols {
			if !columns[table][col] {
				t.Errorf("column %s.%s is missing after Up", table, col)
			}
		}
	}
}

// TestUpTokenColumnsAreByteaNotNull locks the token columns to the mandated
// types. A presence-only check would pass if bytea became text or NOT NULL
// were dropped, so data_type and is_nullable are asserted.
func TestUpTokenColumnsAreByteaNotNull(t *testing.T) {
	pool := testPool(t)
	resetSchema(t, pool)

	if err := postgres.Up(context.Background(), pool, migrationsDir); err != nil {
		t.Fatalf("Up: %v", err)
	}

	types := columnTypes(t, pool)
	want := []struct {
		table      string
		column     string
		dataType   string
		isNullable string
	}{
		{"tiktok_accounts", "access_token_enc", "bytea", "NO"},
		{"tiktok_accounts", "refresh_token_enc", "bytea", "NO"},
		{"tiktok_accounts", "token_key_id", "text", "NO"},
	}
	for _, w := range want {
		got, ok := types[w.table][w.column]
		if !ok {
			t.Errorf("column %s.%s is missing after Up", w.table, w.column)
			continue
		}
		if got.dataType != w.dataType {
			t.Errorf("%s.%s data_type = %q, want %q", w.table, w.column, got.dataType, w.dataType)
		}
		if got.isNullable != w.isNullable {
			t.Errorf("%s.%s is_nullable = %q, want %q", w.table, w.column, got.isNullable, w.isNullable)
		}
	}
}

// columnSet lists the public columns of each base table in the test schema.
func columnSet(t *testing.T, pool *pgxpool.Pool) map[string]map[string]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT table_name, column_name FROM information_schema.columns
		  WHERE table_schema = 'public'`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	defer rows.Close()

	columns := make(map[string]map[string]bool)
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		if columns[table] == nil {
			columns[table] = make(map[string]bool)
		}
		columns[table][column] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns: %v", err)
	}
	return columns
}

// columnInfo is the data_type and nullability of one column.
type columnInfo struct {
	dataType   string
	isNullable string
}

// columnTypes reads data_type and is_nullable for every public column.
func columnTypes(t *testing.T, pool *pgxpool.Pool) map[string]map[string]columnInfo {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT table_name, column_name, data_type, is_nullable
		   FROM information_schema.columns
		  WHERE table_schema = 'public'`)
	if err != nil {
		t.Fatalf("query column types: %v", err)
	}
	defer rows.Close()

	types := make(map[string]map[string]columnInfo)
	for rows.Next() {
		var table, column string
		var info columnInfo
		if err := rows.Scan(&table, &column, &info.dataType, &info.isNullable); err != nil {
			t.Fatalf("scan column type: %v", err)
		}
		if types[table] == nil {
			types[table] = make(map[string]columnInfo)
		}
		types[table][column] = info
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate column types: %v", err)
	}
	return types
}
