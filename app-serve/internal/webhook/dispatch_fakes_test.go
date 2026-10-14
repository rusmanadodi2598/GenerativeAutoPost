// Package webhook holds the fakes the dispatcher tests drive.
//
// @file      internal/webhook/dispatch_fakes_test.go
// @for       Store, transaction binder, and status updater fakes for dispatch tests.
// @uses      context, encoding/json, log/slog, testing, time
// @reason    Keeps the dispatcher test file to its assertions.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-14
package webhook

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"
)

// fakeStore replays insert outcomes without a database. commitErr fails the
// transaction so the apply failure path can be exercised.
type fakeStore struct {
	stored    bool
	inserted  []Event
	outbox    []OutboxRecord
	marked    []string
	deleted   []string
	commitErr error
}

func (f *fakeStore) InsertIfNew(_ context.Context, ev Event) (bool, error) {
	f.inserted = append(f.inserted, ev)
	return f.stored, nil
}

// CommitApplied runs the apply step before recording the outbox and the stamp,
// mirroring the store, so a failed apply leaves neither behind.
func (f *fakeStore) CommitApplied(_ context.Context, dedupeKey string, rec OutboxRecord,
	_ time.Time, apply func(ctx context.Context, tx Tx) error) error {
	if f.commitErr != nil {
		return f.commitErr
	}
	if apply != nil {
		if err := apply(context.Background(), nil); err != nil {
			return err
		}
	}
	f.outbox = append(f.outbox, rec)
	f.marked = append(f.marked, dedupeKey)
	return nil
}

func (f *fakeStore) DeleteEvent(_ context.Context, dedupeKey string) error {
	f.deleted = append(f.deleted, dedupeKey)
	f.stored = true
	return nil
}

// fakeBinder hands the shared fake updater to the apply step, standing in for
// the publish repository's transaction bound repository.
type fakeBinder struct{ up StatusUpdater }

func (b fakeBinder) InTx(_ context.Context, _ Tx) StatusUpdater { return b.up }

// fakeStatuses records the updater calls each event triggers.
type fakeStatuses struct {
	calls    []string
	applyErr error
}

// record appends the call only when it succeeds, so a test can count applied
// transitions rather than attempts.
func (f *fakeStatuses) record(call string) error {
	if f.applyErr != nil {
		return f.applyErr
	}
	f.calls = append(f.calls, call)
	return nil
}

func (f *fakeStatuses) MarkPublishComplete(_ context.Context, p, post string) error {
	return f.record("complete:" + p + ":" + post)
}

func (f *fakeStatuses) MarkPublishFailed(_ context.Context, p, reason string) error {
	return f.record("failed:" + p + ":" + reason)
}

func (f *fakeStatuses) MarkInboxDelivered(_ context.Context, p string) error {
	return f.record("delivered:" + p)
}

func (f *fakeStatuses) MarkUnpublished(_ context.Context, p, post string) error {
	return f.record("unpublished:" + p + ":" + post)
}

// setup wires a dispatcher over the fakes. NewDispatcher takes the concrete
// *Store, so the test swaps the seam the same package owns.
func setup() (*Dispatcher, *fakeStore, *fakeStatuses) {
	store := &fakeStore{stored: true}
	statuses := &fakeStatuses{}
	d := NewDispatcher(nil, statuses, fakeBinder{up: statuses},
		DispatcherConfig{OutboxAggregate: "webhook_event"}, slog.Default())
	d.store = store
	return d, store, statuses
}

// envelopeFor builds an envelope whose content is a JSON encoded string,
// matching the TikTok wire shape.
func envelopeFor(t *testing.T, event, userOpenID string, createTime int64, content string) Envelope {
	t.Helper()
	inner, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	return Envelope{Event: event, UserOpenID: userOpenID, CreateTime: createTime, Content: inner}
}
