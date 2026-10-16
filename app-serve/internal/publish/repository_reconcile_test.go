//go:build integration

// Package publish proves the reconcile sweep reads against PostgreSQL.
//
// @file      internal/publish/repository_reconcile_test.go
// @for       Integration tests for OpenAttempts and RecordFailure.
// @uses      context, testing, time, github.com/google/uuid, internal/apierror
// @reason    The sweep must not poll settled attempts and must persist the failure count between passes.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-15
package publish

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// truncateAttempts clears the sweep table, because the reconcile query is
// deliberately cross-account: it must see every operator's stale attempts.
func truncateAttempts(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `TRUNCATE publish_attempts, tiktok_accounts, users CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// seedOpenAttempt inserts one attempt in an open status, created at the given time.
func seedOpenAttempt(t *testing.T, ctx context.Context, repo *Repository,
	accountID, publishID string, status Status, createdAt time.Time) Record {
	t.Helper()
	id := uuid.NewString()
	const q = `INSERT INTO publish_attempts
		(id, account_id, publish_id, post_mode, media_type, source, status, created_at)
		VALUES ($1, $2, $3, 'DIRECT_POST', 'VIDEO', 'FILE_UPLOAD', $4, $5)`
	if _, err := repo.pool.Exec(ctx, q, id, accountID, publishID, status.String(), createdAt); err != nil {
		t.Fatalf("seed open attempt: %v", err)
	}
	return Record{ID: id, AccountID: accountID, PublishID: publishID, Status: status}
}

func TestOpenAttemptsGivenSettledRows_ThenTheyAreExcluded(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool(t))
	truncateAttempts(t, ctx, repo.pool)
	_, accountID := seedAccount(t, ctx, repo.pool)
	stale := time.Now().UTC().Add(-30 * time.Minute)
	seedOpenAttempt(t, ctx, repo, accountID, "pub-open-"+uuid.NewString(), StatusProcessingUpload, stale)
	seedOpenAttempt(t, ctx, repo, accountID, "pub-complete-"+uuid.NewString(), StatusPublishComplete, stale)
	seedOpenAttempt(t, ctx, repo, accountID, "pub-failed-"+uuid.NewString(), StatusFailed, stale)

	rows, err := repo.OpenAttempts(ctx, time.Now().UTC().Add(-10*time.Minute), 50)
	if err != nil {
		t.Fatalf("OpenAttempts: %v", err)
	}
	if len(rows) != 1 || rows[0].Status != StatusProcessingUpload {
		t.Fatalf("rows = %+v, want only the open attempt", rows)
	}
}

func TestOpenAttemptsGivenFreshRow_ThenItIsLeftToTheWorker(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool(t))
	truncateAttempts(t, ctx, repo.pool)
	_, accountID := seedAccount(t, ctx, repo.pool)
	seedOpenAttempt(t, ctx, repo, accountID, "pub-fresh-"+uuid.NewString(),
		StatusProcessingUpload, time.Now().UTC())

	rows, err := repo.OpenAttempts(ctx, time.Now().UTC().Add(-10*time.Minute), 50)
	if err != nil {
		t.Fatalf("OpenAttempts: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %+v, want none", rows)
	}
}

func TestOpenAttemptsGivenSeveralRows_ThenLimitAndAgeAreApplied(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool(t))
	truncateAttempts(t, ctx, repo.pool)
	_, accountID := seedAccount(t, ctx, repo.pool)
	base := time.Now().UTC().Add(-40 * time.Minute)
	for i := range 4 {
		seedOpenAttempt(t, ctx, repo, accountID, "pub-order-"+uuid.NewString(),
			StatusProcessingUpload, base.Add(time.Duration(i)*time.Minute))
	}
	rows, err := repo.OpenAttempts(ctx, time.Now().UTC().Add(-10*time.Minute), 2)
	if err != nil {
		t.Fatalf("OpenAttempts: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if !rows[0].CreatedAt.Before(rows[1].CreatedAt) {
		t.Fatalf("rows are not oldest first: %v then %v", rows[0].CreatedAt, rows[1].CreatedAt)
	}
}

func TestRecordFailurePersistsTheCountBetweenSweeps(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool(t))
	truncateAttempts(t, ctx, repo.pool)
	_, accountID := seedAccount(t, ctx, repo.pool)
	seeded := seedOpenAttempt(t, ctx, repo, accountID, "pub-fail-"+uuid.NewString(),
		StatusProcessingUpload, time.Now().UTC().Add(-30*time.Minute))

	for range 2 {
		if err := repo.RecordFailure(ctx, seeded.PublishID, "upstream down"); err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}
	}
	rows, err := repo.OpenAttempts(ctx, time.Now().UTC().Add(-10*time.Minute), 50)
	if err != nil {
		t.Fatalf("OpenAttempts: %v", err)
	}
	if len(rows) != 1 || rows[0].Failures != 2 {
		t.Fatalf("rows = %+v, want the persisted count of 2", rows)
	}
	if rows[0].FailReason != "upstream down" {
		t.Fatalf("FailReason = %q, want the stored reason", rows[0].FailReason)
	}
}

func TestRecordFailureGivenUnknownPublishID_ThenNotFound(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool(t))
	err := repo.RecordFailure(ctx, "pub-missing", "upstream down")
	wantPublishCode(t, err, apierror.CodeNotFound)
}
