//go:build integration

// Package insight proves the snapshot store against PostgreSQL.
//
// @file      internal/insight/repository_test.go
// @for       Integration tests for stats and creator snapshot rows.
// @uses      testing, context, pgxpool, github.com/google/uuid, internal/apierror
// @reason    Nil versus zero counts are easy to silently confuse, so the test pins the split.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package insight

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GENAUTO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("GENAUTO_TEST_POSTGRES_DSN is not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("opening pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	userID := uuid.NewString()
	accountID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, $3)`,
		userID, "insight-9b-"+userID+"@example.com", "hash"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	const q = `INSERT INTO tiktok_accounts
		(id, user_id, open_id, access_token_enc, access_token_expires_at,
		 refresh_token_enc, refresh_expires_at, token_key_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx, q, accountID, userID, "open-"+accountID, []byte("a"), now.Add(time.Hour),
		[]byte("r"), now.Add(24*time.Hour), "k1"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	return accountID
}

func intPtr(v int64) *int64 {
	return &v
}

func wantInsightCode(t *testing.T, err error, code apierror.Code) {
	t.Helper()
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %T %v is not *apierror.Error", err, err)
	}
	if apiErr.Code != code {
		t.Fatalf("code = %s, want %s", apiErr.Code, code)
	}
}

func TestStatsListReturnsTheWindowAscendingWithNilAndZeroKept(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool(t))
	videoID := "video-" + uuid.NewString()
	now := time.Now().UTC()
	zero := int64(0)
	if _, err := repo.InsertStats(ctx, StatSnapshot{VideoID: videoID, LikeCount: &zero, FetchedAt: now.Add(-2 * time.Hour)}); err != nil {
		t.Fatalf("insert zero: %v", err)
	}
	if _, err := repo.InsertStats(ctx, StatSnapshot{VideoID: videoID, FetchedAt: now}); err != nil {
		t.Fatalf("insert nil: %v", err)
	}
	points, truncated, err := repo.ListStats(ctx, videoID, now.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if truncated || len(points) != 1 {
		t.Fatalf("points = %d truncated = %v", len(points), truncated)
	}
	if points[0].LikeCount != nil {
		t.Fatalf("like count = %v, want absent", points[0].LikeCount)
	}
	all, _, err := repo.ListStats(ctx, videoID, now.Add(-3*time.Hour), 10)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 || all[0].LikeCount == nil || *all[0].LikeCount != 0 {
		t.Fatalf("all = %+v, want the zero point first and non-nil", all)
	}
}

func TestCreatorSnapshotRoundTripThenNewerWins(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	repo := NewRepository(pool)
	accountID := seedAccount(t, ctx, pool)
	first, err := repo.InsertCreatorSnapshot(ctx, CreatorSnapshot{
		AccountID: accountID, PrivacyOptions: []string{"PUBLIC_TO_EVERYONE"},
		CommentDisabled: true, MaxVideoPostDurationSec: 60,
		FetchedAt: time.Now().UTC().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("insert first: %v", err)
	}
	second, err := repo.InsertCreatorSnapshot(ctx, CreatorSnapshot{
		AccountID: accountID, PrivacyOptions: []string{"MUTUAL_FOLLOW_FRIENDS", "SELF_ONLY"},
		DuetDisabled: true, StitchDisabled: true, MaxVideoPostDurationSec: 180,
		FetchedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("insert second: %v", err)
	}
	got, err := repo.LatestCreatorSnapshot(ctx, accountID)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if got.ID != second.ID || first.ID == second.ID {
		t.Fatalf("got id %s, want %s", got.ID, second.ID)
	}
	if len(got.PrivacyOptions) != 2 || got.PrivacyOptions[0] != "MUTUAL_FOLLOW_FRIENDS" {
		t.Fatalf("options = %v", got.PrivacyOptions)
	}
	if !got.DuetDisabled || !got.StitchDisabled || got.CommentDisabled {
		t.Fatalf("flags = %+v", got)
	}
	if got.MaxVideoPostDurationSec != 180 {
		t.Fatalf("duration = %d, want 180", got.MaxVideoPostDurationSec)
	}
}

func TestCreatorSnapshotLatestUnknownAccountIsNotFound(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool(t))
	_, err := repo.LatestCreatorSnapshot(ctx, uuid.NewString())
	wantInsightCode(t, err, apierror.CodeNotFound)
}

func TestDeleteStatsBeforeRemovesEveryVideoOlderThanCutoff(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool(t))
	now := time.Now().UTC()
	oldID := "video-" + uuid.NewString()
	freshID := "video-" + uuid.NewString()
	if _, err := repo.InsertStats(ctx, StatSnapshot{VideoID: oldID, ViewCount: intPtr(7), FetchedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatalf("insert old: %v", err)
	}
	if _, err := repo.InsertStats(ctx, StatSnapshot{VideoID: freshID, ViewCount: intPtr(8), FetchedAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("insert fresh: %v", err)
	}
	// The delete is table-wide; unique video ids plus `-p 1` keep it safe.
	removed, err := repo.DeleteStatsBefore(ctx, now)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if removed < 1 {
		t.Fatalf("removed = %d, want at least the old row", removed)
	}
	oldPoints, _, err := repo.ListStats(ctx, oldID, time.Time{}, 10)
	if err != nil {
		t.Fatalf("list old: %v", err)
	}
	if len(oldPoints) != 0 {
		t.Fatalf("old points = %d, want 0", len(oldPoints))
	}
	freshPoints, _, err := repo.ListStats(ctx, freshID, time.Time{}, 10)
	if err != nil {
		t.Fatalf("list fresh: %v", err)
	}
	if len(freshPoints) != 1 {
		t.Fatalf("fresh points = %d, want 1", len(freshPoints))
	}
}
