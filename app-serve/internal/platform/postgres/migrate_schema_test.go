//go:build integration

// Package postgres_test asserts migration columns against 001 section 5.
//
// @file      internal/platform/postgres/migrate_schema_test.go
// @for       Column coverage checks for the migrated tables.
// @uses      testing, pgxpool, internal/platform/postgres
// @reason    Locks the schema to the spec column list, not just table names.
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
		"users":                  {"id", "email", "password_hash", "created_at"},
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
