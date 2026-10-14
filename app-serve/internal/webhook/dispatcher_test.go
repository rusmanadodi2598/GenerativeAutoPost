// Package webhook proves the dispatcher maps events to updater calls.
//
// @file      internal/webhook/dispatcher_test.go
// @for       Dispatcher unit tests with fakes for store and statuses.
// @uses      testing, context, encoding/json, errors, log/slog
// @reason    Event mapping and dedupe behavior are the risks here.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"
)

// fakeStore replays insert outcomes without a database.
type fakeStore struct {
	stored   bool
	inserted []Event
	outbox   []OutboxRecord
	marked   []string
}

func (f *fakeStore) InsertIfNew(_ context.Context, ev Event) (bool, error) {
	f.inserted = append(f.inserted, ev)
	return f.stored, nil
}

func (f *fakeStore) CommitApplied(_ context.Context, dedupeKey string, rec OutboxRecord, _ time.Time) error {
	f.outbox = append(f.outbox, rec)
	f.marked = append(f.marked, dedupeKey)
	return nil
}

// fakeStatuses records the updater calls each event triggers.
type fakeStatuses struct {
	calls    []string
	applyErr error
}

func (f *fakeStatuses) record(call string) error {
	f.calls = append(f.calls, call)
	return f.applyErr
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
	d := NewDispatcher(nil, statuses, DispatcherConfig{OutboxAggregate: "webhook_event"}, slog.Default())
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

func TestDedupeKeyShapes(t *testing.T) {
	t.Parallel()
	d, _, _ := setup()
	cases := []struct {
		name                    string
		env                     Envelope
		publishID, postID, want string
	}{
		{"post id", Envelope{Event: EventComplete, CreateTime: 9}, "pub-1", "post-9", EventComplete + ":pub-1:post-9"},
		{"create time", Envelope{Event: EventInboxDelivered, CreateTime: 9}, "pub-1", "", EventInboxDelivered + ":pub-1:9"},
		{"auth removed", Envelope{Event: EventAuthRemoved, UserOpenID: "u-1", CreateTime: 42}, "", "", EventAuthRemoved + ":u-1:42"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := d.DedupeKey(tc.env, tc.publishID, tc.postID); got != tc.want {
				t.Fatalf("key = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHandleMapsEventsToUpdaterCalls(t *testing.T) {
	t.Parallel()
	cases := []struct {
		event, content, wantCall string
	}{
		{EventComplete, `{"publish_id":"p1","post_id":"post-1"}`, "complete:p1:post-1"},
		{EventPubliclyAvailable, `{"publish_id":"p1","post_id":"post-1"}`, "complete:p1:post-1"},
		{EventFailed, `{"publish_id":"p1","reason":"daily limit"}`, "failed:p1:daily limit"},
		{EventInboxDelivered, `{"publish_id":"p1"}`, "delivered:p1"},
		{EventNoLongerPublicly, `{"publish_id":"p1","post_id":"post-1"}`, "unpublished:p1:post-1"},
	}
	for _, tc := range cases {
		t.Run(tc.event, func(t *testing.T) {
			t.Parallel()
			d, _, statuses := setup()
			env := envelopeFor(t, tc.event, "u-1", 1700000000, tc.content)
			outcome, err := d.Handle(context.Background(), env, []byte(`{"raw":true}`))
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if outcome != OutcomeApplied {
				t.Fatalf("outcome = %v, want applied", outcome)
			}
			if len(statuses.calls) != 1 || statuses.calls[0] != tc.wantCall {
				t.Fatalf("updater calls = %v, want [%s]", statuses.calls, tc.wantCall)
			}
		})
	}
}

func TestHandleAuthorizationRemovedCallsInjectedHandler(t *testing.T) {
	t.Parallel()
	d, _, statuses := setup()
	var seen string
	d.OnAuthRemoved(func(_ context.Context, userOpenID string) error {
		seen = userOpenID
		return nil
	})
	env := envelopeFor(t, EventAuthRemoved, "u-7", 99, `{"reason":1}`)
	outcome, err := d.Handle(context.Background(), env, []byte(`{}`))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if outcome != OutcomeApplied {
		t.Fatalf("outcome = %v, want applied", outcome)
	}
	if seen != "u-7" {
		t.Fatalf("auth handler user = %q, want u-7", seen)
	}
	if len(statuses.calls) != 0 {
		t.Fatal("authorization.removed must not drive the publish updater")
	}
}

func TestHandleIgnoresUnknownAndVideoKitEvents(t *testing.T) {
	t.Parallel()
	for _, event := range []string{"totally.unknown", EventVideoUploadFailed, EventVideoPublishDone} {
		t.Run(event, func(t *testing.T) {
			t.Parallel()
			d, store, statuses := setup()
			outcome, err := d.Handle(context.Background(),
				envelopeFor(t, event, "u-1", 1, "x"), []byte(`{}`))
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if outcome != OutcomeIgnored {
				t.Fatalf("outcome = %v, want ignored", outcome)
			}
			if len(statuses.calls) != 0 || len(store.inserted) != 0 {
				t.Fatal("ignored event must call no updater and store no row")
			}
		})
	}
}

func TestHandleDuplicateCallsNoUpdater(t *testing.T) {
	t.Parallel()
	d, store, statuses := setup()
	store.stored = false
	env := envelopeFor(t, EventComplete, "u-1", 5, `{"publish_id":"p1","post_id":"post-1"}`)
	outcome, err := d.Handle(context.Background(), env, []byte(`{}`))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if outcome != OutcomeDuplicate {
		t.Fatalf("outcome = %v, want duplicate", outcome)
	}
	if len(statuses.calls) != 0 || len(store.outbox) != 0 || len(store.marked) != 0 {
		t.Fatal("duplicate delivery must change no state")
	}
}

func TestHandleWritesOutboxAndMarksProcessed(t *testing.T) {
	t.Parallel()
	d, store, _ := setup()
	env := envelopeFor(t, EventInboxDelivered, "u-1", 5, `{"publish_id":"p1"}`)
	if _, err := d.Handle(context.Background(), env, []byte(`{"raw":true}`)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(store.outbox) != 1 {
		t.Fatalf("outbox rows = %d, want 1", len(store.outbox))
	}
	rec := store.outbox[0]
	if rec.EventType != EventInboxDelivered || rec.AggregateType != "webhook_event" {
		t.Fatalf("outbox record = %+v", rec)
	}
	if len(store.marked) != 1 || store.marked[0] != EventInboxDelivered+":p1:5" {
		t.Fatalf("marked keys = %v, want the delivery key", store.marked)
	}
}

func TestHandleReturnsErrorWhenUpdaterFails(t *testing.T) {
	t.Parallel()
	d, store, statuses := setup()
	statuses.applyErr = errors.New("boom")
	env := envelopeFor(t, EventComplete, "u-1", 5, `{"publish_id":"p1","post_id":"post-1"}`)
	outcome, err := d.Handle(context.Background(), env, []byte(`{}`))
	if err == nil {
		t.Fatal("Handle returned nil, want updater error")
	}
	if outcome != OutcomeStored {
		t.Fatalf("outcome = %v, want stored", outcome)
	}
	if len(store.outbox) != 0 || len(store.marked) != 0 {
		t.Fatal("failed transition must not write the outbox row")
	}
}

func TestHandleRejectsUndecodableContent(t *testing.T) {
	t.Parallel()
	d, _, statuses := setup()
	env := Envelope{Event: EventComplete, CreateTime: 1, Content: []byte(`"not-json"`)}
	if _, err := d.Handle(context.Background(), env, []byte(`{}`)); err == nil {
		t.Fatal("Handle returned nil, want decode error")
	}
	if len(statuses.calls) != 0 {
		t.Fatal("undecodable content must call no updater")
	}
}
