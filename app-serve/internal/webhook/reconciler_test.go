// Package webhook proves the reconciler sweeps and the outbox relay behave.
//
// @file      internal/webhook/reconciler_test.go
// @for       Unit tests for the unprocessed replay, open attempt sweep, and drain.
// @uses      testing, context, errors, time
// @reason    The terminal mapping and the published_at stamping are the risks.
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

func TestReconciler_GivenFetchFailure_ThenFailureCountPersistsAcrossSweeps(t *testing.T) {
	attempts := &fakeAttempts{rows: []OpenAttempt{openRow(0)}}
	fetch := &fakeFetcher{err: errors.New("upstream down")}
	r := newTestReconciler(&fakeSweepStore{}, &stubHandler{}, &fakeStatuses{},
		attempts, fetch, nil, nil)

	for range 2 {
		if err := r.ReconcileOnce(context.Background()); err != nil {
			t.Fatalf("reconcile should succeed: %v", err)
		}
	}
	if attempts.failures["pub-1"] != 2 {
		t.Fatalf("failure count = %d, want 2 persisted across sweeps", attempts.failures["pub-1"])
	}
	if attempts.rows[0].Failures != 2 {
		t.Fatalf("reader must return the stored count, got %d", attempts.rows[0].Failures)
	}
	if len(attempts.reasons) != 2 || attempts.reasons[1] == exhaustReason {
		t.Fatalf("exhaust reason stamped before the cap, got %v", attempts.reasons)
	}
}

func TestReconciler_GivenFailureCountAtCap_ThenExhaustFiresOnNextSweep(t *testing.T) {
	statuses := &fakeStatuses{}
	attempts := &fakeAttempts{rows: []OpenAttempt{openRow(2)}}
	fetch := &fakeFetcher{err: errors.New("upstream down")}
	r := newTestReconciler(&fakeSweepStore{}, &stubHandler{}, statuses, attempts, fetch, nil, nil)

	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile should succeed: %v", err)
	}
	want := "failed:pub-1:RECONCILE_EXHAUSTED"
	if len(statuses.calls) != 1 || statuses.calls[0] != want {
		t.Fatalf("expected %q, got %v", want, statuses.calls)
	}
	if attempts.reasons[len(attempts.reasons)-1] != exhaustReason {
		t.Fatalf("exhaust branch must record the exhausted reason, got %v", attempts.reasons)
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
