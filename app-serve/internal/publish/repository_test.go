//go:build integration

// Package publish proves the publish store against PostgreSQL.
//
// @file      internal/publish/repository_test.go
// @for       Integration tests for attempt and chunk rows.
// @uses      testing, context, pgxpool, github.com/google/uuid, internal/apierror
// @reason    Terminal guard and unique publish_id are the risks here.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package publish

import (
	"context"
	"errors"
	"fmt"
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

func seedAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (userID, accountID string) {
	t.Helper()
	userID = uuid.NewString()
	accountID = uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, $3)`,
		userID, "publish-9b-"+userID+"@example.com", "hash"); err != nil {
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
	return userID, accountID
}

func seedAttempt(t *testing.T, ctx context.Context, repo *Repository, accountID, suffix string) Record {
	t.Helper()
	rec, err := repo.Insert(ctx, Record{
		ID: uuid.NewString(), AccountID: accountID, PublishID: "pub-" + suffix,
		PostMode: "DIRECT_POST", MediaType: "VIDEO", Source: "FILE_UPLOAD", Status: StatusCreated,
		PrivacyLevel: "SELF_ONLY", Title: "seed " + suffix,
	})
	if err != nil {
		t.Fatalf("seed attempt: %v", err)
	}
	return rec
}

func wantPublishCode(t *testing.T, err error, code apierror.Code) {
	t.Helper()
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not *apierror.Error", err)
	}
	if apiErr.Code != code {
		t.Fatalf("code = %s, want %s", apiErr.Code, code)
	}
}

func TestRepositoryInsertThenGet(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool(t))
	_, accountID := seedAccount(t, ctx, repo.pool)
	seeded := seedAttempt(t, ctx, repo, accountID, uuid.NewString())
	got, err := repo.Get(ctx, seeded.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.PublishID != seeded.PublishID || got.Status != StatusCreated || got.Title != seeded.Title {
		t.Fatalf("got %+v, seeded %+v", got, seeded)
	}
	got, err = repo.GetByPublishID(ctx, seeded.PublishID)
	if err != nil {
		t.Fatalf("get by publish id: %v", err)
	}
	if got.ID != seeded.ID || got.PostMode != "DIRECT_POST" || got.PrivacyLevel != "SELF_ONLY" {
		t.Fatalf("got %+v, seeded %+v", got, seeded)
	}
	if _, err := Hydrate(got); err != nil {
		t.Fatalf("hydrate: %v", err)
	}
}

func TestRepositoryDuplicatePublishIDConflicts(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool(t))
	_, accountID := seedAccount(t, ctx, repo.pool)
	first := seedAttempt(t, ctx, repo, accountID, uuid.NewString())
	_, err := repo.Insert(ctx, Record{
		ID: uuid.NewString(), AccountID: accountID, PublishID: first.PublishID,
		PostMode: "DIRECT_POST", MediaType: "VIDEO", Source: "FILE_UPLOAD", Status: StatusCreated,
	})
	wantPublishCode(t, err, apierror.CodeConflict)
}

func TestRepositoryListFiltersCursorAndLimit(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool(t))
	_, accountID := seedAccount(t, ctx, repo.pool)
	for i := 0; i < 3; i++ {
		seedAttempt(t, ctx, repo, accountID, fmt.Sprintf("list-%s-%d", accountID[:8], i))
		time.Sleep(time.Millisecond)
	}
	recs, err := repo.List(ctx, accountID, "", time.Time{}, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("len = %d, want 3", len(recs))
	}
	for i := 1; i < len(recs); i++ {
		if recs[i-1].ID == recs[i].ID {
			t.Fatal("duplicate rows in list")
		}
	}
	first := recs[0]
	recs[0].Status = StatusProcessingUpload
	if err := repo.Update(ctx, recs[0]); err != nil {
		t.Fatalf("update to processing: %v", err)
	}
	filtered, err := repo.List(ctx, accountID, "PROCESSING_UPLOAD", time.Time{}, 0)
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != first.ID {
		t.Fatalf("filtered = %+v", filtered)
	}
	cursor := recs[0].CreatedAt
	limited, err := repo.List(ctx, accountID, "", time.Time{}, 2)
	if err != nil {
		t.Fatalf("limited list: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("len = %d, want 2", len(limited))
	}
	recent, err := repo.List(ctx, accountID, "", cursor, 10)
	if err != nil {
		t.Fatalf("cursor list: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("len = %d, want 2 after cursor", len(recent))
	}
}

func TestRepositoryUpdateRefusesTerminalRow(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool(t))
	_, accountID := seedAccount(t, ctx, repo.pool)
	seeded := seedAttempt(t, ctx, repo, accountID, uuid.NewString())
	seeded.Status = StatusFailed
	seeded.FailReason = "UPSTREAM_DENIED"
	if err := repo.Update(ctx, seeded); err != nil {
		t.Fatalf("first fail: %v", err)
	}
	seeded.Status = StatusProcessingUpload
	err := repo.Update(ctx, seeded)
	wantPublishCode(t, err, apierror.CodeNotFound)
	if err := repo.UpdateProgress(ctx, seeded.ID, 1024); err != nil {
		t.Fatalf("update progress: %v", err)
	}
	got, err := repo.Get(ctx, seeded.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.UploadedBytes != 1024 || got.Status != StatusFailed {
		t.Fatalf("got %+v", got)
	}
}

func TestRepositoryChunkRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool(t))
	_, accountID := seedAccount(t, ctx, repo.pool)
	seeded := seedAttempt(t, ctx, repo, accountID, uuid.NewString())
	chunks := []ChunkRecord{
		{ID: uuid.NewString(), AttemptID: seeded.ID, Index: 1, ByteStart: 5, ByteEnd: 9, Range: "bytes 5-9/10"},
		{ID: uuid.NewString(), AttemptID: seeded.ID, Index: 0, ByteStart: 0, ByteEnd: 4, Range: "bytes 0-4/10"},
	}
	for _, c := range chunks {
		if err := repo.RecordChunk(ctx, c); err != nil {
			t.Fatalf("record chunk: %v", err)
		}
	}
	stored, err := repo.Chunks(ctx, seeded.ID)
	if err != nil {
		t.Fatalf("chunks: %v", err)
	}
	if len(stored) != 2 || stored[0].Index != 0 || stored[1].Index != 1 {
		t.Fatalf("stored = %+v", stored)
	}
	if stored[0].Range != "bytes 0-4/10" || stored[1].ByteEnd != 9 {
		t.Fatalf("stored = %+v", stored)
	}
}
