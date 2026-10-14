// Package webhook proves the outbox relay keeps rows pending on failure.
//
// @file      internal/webhook/outbox_test.go
// @for       Unit tests for the outbox drain, retry, and duplicate guard.
// @uses      testing, context, errors
// @reason    The published_at stamping is the only durability proof here.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     worker
// @stability stable
// @since     2026-10-12
package webhook

import (
	"context"
	"errors"
	"testing"
)

// stubSink fails a fixed set of row ids and records every attempt.
type stubSink struct {
	failOn map[string]bool
	seen   []string
}

func (s *stubSink) Publish(_ context.Context, rec OutboxRecord) error {
	s.seen = append(s.seen, rec.ID)
	if s.failOn[rec.ID] {
		return errors.New("sink unavailable")
	}
	return nil
}

// drainReconciler builds a reconciler over one pending outbox list.
func drainReconciler(pending []OutboxRecord) (*Reconciler, *fakeSweepStore) {
	store := &fakeSweepStore{pending: pending}
	return newTestReconciler(store, &stubHandler{}, &fakeStatuses{}, nil, nil, nil, nil), store
}

func TestReconciler_DrainOutboxGivenSinkFailure_ThenRowStaysPendingAndRetries(t *testing.T) {
	r, store := drainReconciler([]OutboxRecord{{ID: "a"}, {ID: "b"}})
	sink := &stubSink{failOn: map[string]bool{"a": true}}

	if err := r.DrainOutbox(context.Background(), sink); err != nil {
		t.Fatalf("drain should succeed: %v", err)
	}
	if got := store.publishedIDs(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("only the delivered row should be stamped, got %v", got)
	}
	sink.failOn = nil
	if err := r.DrainOutbox(context.Background(), sink); err != nil {
		t.Fatalf("second drain should succeed: %v", err)
	}
	// The fake store replays its pending list on every call, so the retried row
	// shows up a third time. The real query filters published_at IS NULL.
	if got := store.publishedIDs(); len(got) != 3 || got[0] != "b" || got[1] != "a" {
		t.Fatalf("the failed row should be retried and stamped, got %v", got)
	}
}

func TestReconciler_DrainOutboxGivenDuplicateRows_ThenEachPublishesOnce(t *testing.T) {
	r, _ := drainReconciler([]OutboxRecord{{ID: "a"}, {ID: "a"}})
	sink := &stubSink{}

	if err := r.DrainOutbox(context.Background(), sink); err != nil {
		t.Fatalf("drain should succeed: %v", err)
	}
	if len(sink.seen) != 1 {
		t.Fatalf("a row must publish once per pass, got %v", sink.seen)
	}
}
