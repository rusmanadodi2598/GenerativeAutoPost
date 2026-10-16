//go:build integration

// Package publish proves the publish store against PostgreSQL.
//
// @file      internal/publish/repository_media_id_test.go
// @for       Integration tests for the attempt media link.
// @uses      testing, context, github.com/google/uuid
// @reason    The link is only observable through a round trip, and older rows must still read cleanly.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-16
package publish

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestRepositoryKeepsTheMediaID(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool(t))
	_, accountID := seedAccount(t, ctx, repo.pool)
	mediaID := uuid.NewString()
	seeded := seedAttempt(t, ctx, repo, accountID, uuid.NewString())
	stored, err := repo.Insert(ctx, Record{
		ID: uuid.NewString(), AccountID: accountID, MediaID: mediaID,
		PublishID: "pub-media-" + seeded.ID, PostMode: "DIRECT_POST",
		MediaType: "VIDEO", Source: "FILE_UPLOAD", Status: StatusCreated,
	})
	if err != nil {
		t.Fatalf("insert with media id: %v", err)
	}

	got, err := repo.Get(ctx, stored.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.MediaID != mediaID {
		t.Fatalf("MediaID = %q, want %q", got.MediaID, mediaID)
	}
}

func TestRepositoryReadsAnEmptyMediaIDForOlderRows(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool(t))
	_, accountID := seedAccount(t, ctx, repo.pool)
	seeded := seedAttempt(t, ctx, repo, accountID, uuid.NewString())
	if _, err := repo.pool.Exec(ctx,
		`UPDATE publish_attempts SET media_id = NULL WHERE id = $1`, seeded.ID); err != nil {
		t.Fatalf("clearing media id: %v", err)
	}

	got, err := repo.Get(ctx, seeded.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.MediaID != "" {
		t.Fatalf("MediaID = %q, want empty for a row recorded before the column", got.MediaID)
	}
}
