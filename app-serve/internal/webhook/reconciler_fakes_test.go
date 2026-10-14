// Package webhook holds the fakes the reconciler tests drive.
//
// @file      internal/webhook/reconciler_fakes_test.go
// @for       Store, attempt, fetcher, gate, and progress fakes for the sweep tests.
// @uses      context, log/slog, sync, time
// @reason    Keeps the reconciler test file to its assertions.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     worker
// @stability stable
// @since     2026-10-14
package webhook

import (
	"context"
	"log/slog"
	"sync"
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

// fakeAttempts serves the open attempt sweep with fixed rows. failures
// records the persisted failure reasons per publish id, so a test can prove
// the count survives across sweeps.
type fakeAttempts struct {
	rows     []OpenAttempt
	failures map[string]int
	reasons  []string
	err      error
}

func (f *fakeAttempts) OpenAttempts(_ context.Context, _ time.Time, _ int) ([]OpenAttempt, error) {
	return f.rows, f.err
}

func (f *fakeAttempts) RecordFailure(_ context.Context, publishID string, reason string) error {
	if f.err != nil {
		return f.err
	}
	if f.failures == nil {
		f.failures = map[string]int{}
	}
	f.failures[publishID]++
	f.reasons = append(f.reasons, reason)
	if len(f.rows) > 0 {
		f.rows[0].Failures = f.failures[publishID]
	}
	return nil
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
