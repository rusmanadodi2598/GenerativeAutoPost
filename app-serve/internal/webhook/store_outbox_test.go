//go:build integration

// Package webhook proves the outbox and the atomic commit against PostgreSQL.
//
// @file      internal/webhook/store_outbox_test.go
// @for       Integration tests for outbox rows and CommitApplied atomicity.
// @uses      testing, context, errors, time, pgxpool, github.com/google/uuid
// @reason    The commit transaction is the only durability proof here.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-14
package webhook

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCommitAppliedWritesOutboxAndStampsProcessed(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	ev := newEvent("commit")

	if _, err := store.InsertIfNew(ctx, ev); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rec := OutboxRecord{
		ID:            uuid.NewString(),
		AggregateType: "webhook_event",
		AggregateID:   uuid.NewString(),
		EventType:     EventInboxDelivered,
		Payload:       []byte(`{"publish_id":"p1"}`),
	}
	applied := false
	if err := store.CommitApplied(ctx, ev.DedupeKey, rec, time.Now().UTC(),
		func(_ context.Context, _ Tx) error {
			applied = true
			return nil
		}); err != nil {
		t.Fatalf("commit applied: %v", err)
	}
	if !applied {
		t.Fatal("apply step did not run inside the transaction")
	}
	pending, err := store.OutboxPending(ctx, 50)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	found := false
	for _, row := range pending {
		if row.ID == rec.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("outbox row from CommitApplied missing from pending sweep")
	}
	rows, err := store.Unprocessed(ctx, 0, 500)
	if err != nil {
		t.Fatalf("unprocessed: %v", err)
	}
	for _, row := range rows {
		if row.DedupeKey == ev.DedupeKey {
			t.Fatal("committed event still present in unprocessed sweep")
		}
	}
}

func TestCommitAppliedGivenFailingApply_ThenNothingCommits(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	ev := newEvent("commitfail")

	if _, err := store.InsertIfNew(ctx, ev); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rec := OutboxRecord{
		ID:            uuid.NewString(),
		AggregateType: "webhook_event",
		AggregateID:   uuid.NewString(),
		EventType:     EventInboxDelivered,
		Payload:       []byte(`{"publish_id":"p1"}`),
	}
	fail := errors.New("apply failed")
	if err := store.CommitApplied(ctx, ev.DedupeKey, rec, time.Now().UTC(),
		func(_ context.Context, _ Tx) error { return fail }); err == nil {
		t.Fatal("commit applied returned nil, want apply error")
	}
	pending, err := store.OutboxPending(ctx, 50)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	for _, row := range pending {
		if row.ID == rec.ID {
			t.Fatal("failed apply must not leave an outbox row")
		}
	}
}
