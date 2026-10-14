// Package webhook proves the reconciler sweeps and the outbox relay behave.
//
// @file      internal/webhook/reconciler_test.go
// @for       Unit tests for the unprocessed replay, open attempt sweep, and drain.
// @uses      testing, context, errors, log/slog, sync, time
// @reason    The terminal mapping and the published_at stamping are the risks.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     worker
// @stability stable
// @since     2026-10-12
package webhook

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// fakeSweepStore serves the three persistence calls the reconciler drives and
// records the stamps it made.
type fakeSweepStore struct {
	mu        sync.Mutex
	unhandled []Event
	pending   []OutboxRecord
	published []string
}

func (f *fakeSweepStore) Unprocessed(_ context.Context, _ time.Duration, _ int) ([]Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Event(nil), f.unhandled...), nil
}

func (f *fakeSweepStore) OutboxPending(_ context.Context, _ int) ([]OutboxRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]OutboxRecord(nil), f.pending...), nil
}

func (f *fakeSweepStore) MarkOutboxPublished(_ context.Context, id string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, id)
	return nil
}

func (f *fakeSweepStore) publishedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.published...)
}

// fakeAttempts serves the open attempt sweep with fixed rows.
type fakeAttempts struct {
	rows []OpenAttempt
	err  error
}

func (f *fakeAttempts) OpenAttempts(_ context.Context, _ time.Time, _ int) ([]OpenAttempt, error) {
	return f.rows, f.err
}

// fakeFetcher answers with one canned status update or one error.
type fakeFetcher struct {
	update StatusUpdate
	err    error
	calls  int
}

func (f *fakeFetcher) FetchStatus(_ context.Context, _, _ string) (StatusUpdate, error) {
	f.calls++
	return f.update, f.err
}

// fakeGate records limiter refusals without a Redis client.
type fakeGate struct {
	err  error
	seen []string
}

func (f *fakeGate) Allow(_ context.Context, accountID string) error {
	f.seen = append(f.seen, accountID)
	return f.err
}

// fakeProgress records polled byte counts.
type fakeProgress struct {
	writes []int64
}

func (f *fakeProgress) RecordProgress(_ context.Context, _ string, bytes int64) error {
	f.writes = append(f.writes, bytes)
	return nil
}

// newTestReconciler builds a reconciler over fakes with tiny sweep intervals.
func newTestReconciler(store sweepStore, dispatch eventHandler, statuses StatusUpdater,
	attempts OpenAttemptReader, fetch StatusFetcher, gate RateGate, progress ProgressRecorder,
) *Reconciler {
	r := NewReconciler(nil, nil, statuses, attempts, fetch, gate, progress,
		ReconcilerConfig{Interval: time.Millisecond, BatchSize: 10, MaxAttempts: 3}, slog.Default())
	r.store, r.dispatch = store, dispatch
	return r
}

// openRow builds a stale open attempt for the sweep.
func openRow(failures int) OpenAttempt {
	return OpenAttempt{
		PublishID: "pub-1", AccountID: "acc-1", AccessToken: "token",
		Failures: failures,
	}
}

func TestReconciler_GivenStaleUnprocessedRow_ThenItIsReplayed(t *testing.T) {
	store := &fakeSweepStore{unhandled: []Event{
		{DedupeKey: "old", EventType: "post.publish.complete", Payload: []byte(`{"event":"post.publish.complete"}`)},
	}}
	h := &stubHandler{}
	r := newTestReconciler(store, h, &fakeStatuses{}, nil, nil, nil, nil)

	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile should succeed: %v", err)
	}
	if h.calls.Load() != 1 {
		t.Fatalf("stale row should be replayed once, got %d calls", h.calls.Load())
	}
}

func TestReconciler_GivenNoStaleRows_ThenNothingIsReplayed(t *testing.T) {
	h := &stubHandler{}
	r := newTestReconciler(&fakeSweepStore{}, h, &fakeStatuses{}, nil, nil, nil, nil)

	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile should succeed: %v", err)
	}
	if h.calls.Load() != 0 {
		t.Fatalf("a fresh queue must not be replayed, got %d calls", h.calls.Load())
	}
}

func TestReconciler_GivenTerminalStatus_ThenItIsAppliedOnce(t *testing.T) {
	statuses := &fakeStatuses{}
	fetch := &fakeFetcher{update: StatusUpdate{Status: "PUBLISH_COMPLETE", PostIDs: []string{"post-9"}}}
	r := newTestReconciler(&fakeSweepStore{}, &stubHandler{}, statuses,
		&fakeAttempts{rows: []OpenAttempt{openRow(0)}}, fetch, nil, nil)

	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile should succeed: %v", err)
	}
	if fetch.calls != 1 {
		t.Fatalf("expected one status fetch, got %d", fetch.calls)
	}
	want := "complete:pub-1:post-9"
	if len(statuses.calls) != 1 || statuses.calls[0] != want {
		t.Fatalf("expected %q, got %v", want, statuses.calls)
	}
}

func TestReconciler_GivenFailedStatus_ThenTerminalFailureIsApplied(t *testing.T) {
	statuses := &fakeStatuses{}
	fetch := &fakeFetcher{update: StatusUpdate{Status: "FAILED", FailReason: "AUDIO_TOO_LONG"}}
	r := newTestReconciler(&fakeSweepStore{}, &stubHandler{}, statuses,
		&fakeAttempts{rows: []OpenAttempt{openRow(0)}}, fetch, nil, nil)

	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile should succeed: %v", err)
	}
	want := "failed:pub-1:AUDIO_TOO_LONG"
	if len(statuses.calls) != 1 || statuses.calls[0] != want {
		t.Fatalf("expected %q, got %v", want, statuses.calls)
	}
}

func TestReconciler_GivenOpenStatus_ThenProgressIsRecorded(t *testing.T) {
	statuses := &fakeStatuses{}
	progress := &fakeProgress{}
	fetch := &fakeFetcher{update: StatusUpdate{Status: "PROCESSING_UPLOAD", UploadedBytes: 4096}}
	r := newTestReconciler(&fakeSweepStore{}, &stubHandler{}, statuses,
		&fakeAttempts{rows: []OpenAttempt{openRow(0)}}, fetch, nil, progress)

	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile should succeed: %v", err)
	}
	if len(statuses.calls) != 0 {
		t.Fatalf("an open verdict must not settle the attempt, got %v", statuses.calls)
	}
	if len(progress.writes) != 1 || progress.writes[0] != 4096 {
		t.Fatalf("expected one 4096 byte progress write, got %v", progress.writes)
	}
}

func TestReconciler_GivenRepeatedFetchFailure_ThenExhaustedReasonIsStamped(t *testing.T) {
	statuses := &fakeStatuses{}
	fetch := &fakeFetcher{err: errors.New("upstream down")}
	r := newTestReconciler(&fakeSweepStore{}, &stubHandler{}, statuses,
		&fakeAttempts{rows: []OpenAttempt{openRow(2)}}, fetch, nil, nil)

	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile should succeed: %v", err)
	}
	want := "failed:pub-1:RECONCILE_EXHAUSTED"
	if len(statuses.calls) != 1 || statuses.calls[0] != want {
		t.Fatalf("expected %q, got %v", want, statuses.calls)
	}
}

func TestReconciler_GivenRateLimitRefusal_ThenNoFetchIsMade(t *testing.T) {
	fetch := &fakeFetcher{update: StatusUpdate{Status: "PUBLISH_COMPLETE"}}
	gate := &fakeGate{err: errors.New("rate limited")}
	r := newTestReconciler(&fakeSweepStore{}, &stubHandler{}, &fakeStatuses{},
		&fakeAttempts{rows: []OpenAttempt{openRow(0)}}, fetch, gate, nil)

	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile should succeed: %v", err)
	}
	if fetch.calls != 0 {
		t.Fatalf("a refused gate must skip the fetch, got %d calls", fetch.calls)
	}
}
