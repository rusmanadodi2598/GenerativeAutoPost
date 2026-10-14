//go:build integration

// Package webhook proves the event and outbox store against PostgreSQL.
//
// @file      internal/webhook/store_test.go
// @for       Integration tests for webhook_events and outbox rows.
// @uses      testing, context, time, pgxpool, github.com/google/uuid
// @reason    Dedupe conflicts and sweep cutoffs are the risks here.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package webhook

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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

func newEvent(prefix string) Event {
	return Event{
		ID:        uuid.NewString(),
		DedupeKey: prefix + "-" + uuid.NewString(),
		EventType: EventComplete,
		PublishID: "pub-" + uuid.NewString()[:8],
		PostID:    "post-1",
		Payload:   []byte(`{"raw":true}`),
	}
}

func TestInsertIfNewSecondWriteIsDuplicate(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	ev := newEvent("dupe")

	first, err := store.InsertIfNew(ctx, ev)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if !first {
		t.Fatal("first insert stored = false, want true")
	}
	again, err := store.InsertIfNew(ctx, Event{
		DedupeKey: ev.DedupeKey, EventType: ev.EventType,
		PublishID: ev.PublishID, PostID: ev.PostID, Payload: ev.Payload,
	})
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if again {
		t.Fatal("second insert stored = true, want false")
	}
}

func TestMarkProcessedExcludesFromUnprocessed(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	ev := newEvent("proc")

	if _, err := store.InsertIfNew(ctx, ev); err != nil {
		t.Fatalf("insert: %v", err)
	}
	before, err := store.Unprocessed(ctx, 0, 50)
	if err != nil {
		t.Fatalf("unprocessed: %v", err)
	}
	found := false
	for _, row := range before {
		if row.DedupeKey == ev.DedupeKey {
			found = true
		}
	}
	if !found {
		t.Fatal("fresh row missing from unprocessed sweep")
	}
	if err := store.MarkProcessed(ctx, ev.DedupeKey, time.Now().UTC()); err != nil {
		t.Fatalf("mark processed: %v", err)
	}
	after, err := store.Unprocessed(ctx, 0, 50)
	if err != nil {
		t.Fatalf("unprocessed after mark: %v", err)
	}
	for _, row := range after {
		if row.DedupeKey == ev.DedupeKey {
			t.Fatal("processed row still present in unprocessed sweep")
		}
	}
}

func TestUnprocessedKeepsFreshRows(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	ev := newEvent("fresh")

	if _, err := store.InsertIfNew(ctx, ev); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := store.Unprocessed(ctx, time.Hour, 50)
	if err != nil {
		t.Fatalf("unprocessed: %v", err)
	}
	for _, row := range rows {
		if row.DedupeKey == ev.DedupeKey {
			t.Fatal("fresh row returned by an older-than sweep")
		}
	}
}

func TestMarkProcessedMissingKeyFails(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	if err := store.MarkProcessed(ctx, "webhook-missing-"+uuid.NewString(), time.Now().UTC()); err == nil {
		t.Fatal("mark processed returned nil, want not found")
	}
}

func TestOutboxLifecycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	rec := OutboxRecord{
		ID:            uuid.NewString(),
		AggregateType: "webhook_event",
		AggregateID:   uuid.NewString(),
		EventType:     EventInboxDelivered,
		Payload:       []byte(`{"publish_id":"p1"}`),
	}
	if err := store.EnqueueOutbox(ctx, rec); err != nil {
		t.Fatalf("enqueue: %v", err)
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
		t.Fatal("enqueued row missing from pending sweep")
	}
	if err := store.MarkOutboxPublished(ctx, rec.ID, time.Now().UTC()); err != nil {
		t.Fatalf("mark published: %v", err)
	}
	pending, err = store.OutboxPending(ctx, 50)
	if err != nil {
		t.Fatalf("pending after publish: %v", err)
	}
	for _, row := range pending {
		if row.ID == rec.ID {
			t.Fatal("published row still present in pending sweep")
		}
	}
}

func TestMarkOutboxPublishedMissingRowFails(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	if err := store.MarkOutboxPublished(ctx, uuid.NewString(), time.Now().UTC()); err == nil {
		t.Fatal("mark published returned nil, want not found")
	}
}
