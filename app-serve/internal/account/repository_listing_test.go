//go:build integration

// Package account holds the TikTok account aggregate and its persistence shapes.
//
// @file      internal/account/repository_listing_test.go
// @for       Integration tests for the listing and credential clearing against PostgreSQL.
// @uses      testing, time
// @reason    Operator scoping and the bytea clearing write only hold against a real database.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-16
package account

import (
	"context"
	"testing"
	"time"
)

// TestListOwnedReturnsOnlyTheOperatorsRows proves the console list never sees a
// foreign account row.
func TestListOwnedReturnsOnlyTheOperatorsRows(t *testing.T) {
	repo, pool := fixture(t)
	ctx := context.Background()
	mine := seedUser(t, pool)
	foreign := seedUser(t, pool)
	if _, err := repo.UpsertEncrypted(ctx, seedRecord(t, mine, "open-list-mine")); err != nil {
		t.Fatalf("UpsertEncrypted: %v", err)
	}
	if _, err := repo.UpsertEncrypted(ctx, seedRecord(t, foreign, "open-list-foreign")); err != nil {
		t.Fatalf("UpsertEncrypted: %v", err)
	}

	rows, err := repo.ListOwned(ctx, mine)
	if err != nil {
		t.Fatalf("ListOwned: %v", err)
	}
	if len(rows) != 1 || rows[0].OperatorID != mine {
		t.Fatalf("want the one owned row, got %d", len(rows))
	}
}

// TestListOwnedAnswersNotFoundForMalformedOperator proves a bad operator id is
// denied rather than selected unfiltered.
func TestListOwnedAnswersNotFoundForMalformedOperator(t *testing.T) {
	repo, _ := fixture(t)

	_, err := repo.ListOwned(context.Background(), "not-a-uuid")
	wantNotFound(t, err)
}

// TestClearCredentialsDropsTheTokenMaterial proves no ciphertext survives the
// delete, which is what makes revoke delete indistinguishable from revoke.
func TestClearCredentialsDropsTheTokenMaterial(t *testing.T) {
	repo, pool := fixture(t)
	ctx := context.Background()
	operatorID := seedUser(t, pool)
	stored, err := repo.UpsertEncrypted(ctx, seedRecord(t, operatorID, "open-clear"))
	if err != nil {
		t.Fatalf("UpsertEncrypted: %v", err)
	}

	if err := repo.ClearCredentials(ctx, stored.ID, time.Now().UTC()); err != nil {
		t.Fatalf("ClearCredentials: %v", err)
	}
	got, err := repo.GetOwned(ctx, operatorID, stored.ID)
	if err != nil {
		t.Fatalf("GetOwned after clear: %v", err)
	}
	if len(got.AccessEnc) != 0 || len(got.RefreshEnc) != 0 {
		t.Fatalf("token material survived: access %d bytes refresh %d bytes", len(got.AccessEnc), len(got.RefreshEnc))
	}
	if got.RevokedAt == nil {
		t.Fatal("the clear must stamp the revoke time")
	}
}

// TestClearCredentialsKeepsTheOriginalRevokeTime proves a repeat delete does
// not move the audit timestamp.
func TestClearCredentialsKeepsTheOriginalRevokeTime(t *testing.T) {
	repo, pool := fixture(t)
	ctx := context.Background()
	operatorID := seedUser(t, pool)
	stored, err := repo.UpsertEncrypted(ctx, seedRecord(t, operatorID, "open-clear-twice"))
	if err != nil {
		t.Fatalf("UpsertEncrypted: %v", err)
	}
	first := time.Now().UTC().Add(-time.Hour)
	if err := repo.ClearCredentials(ctx, stored.ID, first); err != nil {
		t.Fatalf("first ClearCredentials: %v", err)
	}

	if err := repo.ClearCredentials(ctx, stored.ID, time.Now().UTC()); err != nil {
		t.Fatalf("second ClearCredentials: %v", err)
	}
	got, err := repo.GetOwned(ctx, operatorID, stored.ID)
	if err != nil {
		t.Fatalf("GetOwned: %v", err)
	}
	if !got.RevokedAt.Equal(first.Truncate(time.Microsecond)) && !got.RevokedAt.Before(first) {
		t.Fatalf("the revoke time moved: want %v, got %v", first, got.RevokedAt)
	}
}

// TestClearCredentialsOnUnknownIdAnswersNotFound proves the delete keeps the
// deny by default shape for an unknown id.
func TestClearCredentialsOnUnknownIdAnswersNotFound(t *testing.T) {
	repo, _ := fixture(t)

	id, err := NewID()
	if err != nil {
		t.Fatalf("generate id: %v", err)
	}
	if err := repo.ClearCredentials(context.Background(), id, time.Now().UTC()); err == nil {
		t.Fatal("want a not found error for an unknown id")
	} else {
		wantNotFound(t, err)
	}
}
