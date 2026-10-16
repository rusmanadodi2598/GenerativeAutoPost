// Package compose maps stale publish attempts onto the reconcile reader.
//
// @file      internal/compose/reconcile_reader_test.go
// @for       OpenAttemptReader adapter tests over the publish and account stores.
// @uses      context, errors, testing, time, internal/account, internal/publish, internal/webhook
// @reason    The worker has no operator session, so the token it polls with resolves through a system scoped lookup.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-15
package compose

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/publish"
)

// stubAttempts serves canned sweep rows and records the failure writes.
type stubAttempts struct {
	rows     []publish.OpenAttemptRow
	failures []string
	err      error
}

func (s *stubAttempts) OpenAttempts(_ context.Context, _ time.Time, _ int) ([]publish.OpenAttemptRow, error) {
	return s.rows, s.err
}

func (s *stubAttempts) RecordFailure(_ context.Context, publishID, reason string) error {
	s.failures = append(s.failures, publishID+":"+reason)
	return s.err
}

// stubTokens resolves a canned access token for one account.
type stubTokens struct {
	token string
	err   error
	calls []string
}

func (s *stubTokens) AccessForAccount(_ context.Context, accountID string) (string, error) {
	s.calls = append(s.calls, accountID)
	return s.token, s.err
}

func TestReconcileReaderMapsRowsAndResolvesTheToken(t *testing.T) {
	attempts := &stubAttempts{rows: []publish.OpenAttemptRow{{
		PublishID: "pub-1", AccountID: "acc-1", Status: publish.StatusProcessingUpload,
		FailReason: "slow", PostID: "post-1", UploadedBytes: 100, Failures: 2,
	}}}
	tokens := &stubTokens{token: "access-token"}
	reader := newReconcileReader(attempts, tokens)
	rows, err := reader.OpenAttempts(context.Background(), time.Now(), 10)
	if err != nil {
		t.Fatalf("OpenAttempts returned %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	row := rows[0]
	if row.PublishID != "pub-1" || row.AccessToken != "access-token" ||
		row.Failures != 2 || row.Status != "PROCESSING_UPLOAD" || row.FailReason != "slow" {
		t.Fatalf("row = %+v", row)
	}
	if len(tokens.calls) != 1 || tokens.calls[0] != "acc-1" {
		t.Fatalf("token lookups = %v", tokens.calls)
	}
}

func TestReconcileReaderSkipsARowWhoseTokenIsGone(t *testing.T) {
	attempts := &stubAttempts{rows: []publish.OpenAttemptRow{
		{PublishID: "pub-1", AccountID: "acc-1"},
		{PublishID: "pub-2", AccountID: "acc-2"},
	}}
	tokens := &stubTokens{err: errors.New("revoked")}
	reader := newReconcileReader(attempts, tokens)
	rows, err := reader.OpenAttempts(context.Background(), time.Now(), 10)
	if err != nil {
		t.Fatalf("OpenAttempts returned %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %+v, want a skipped row rather than a failed sweep", rows)
	}
}

func TestReconcileReaderForwardsRecordFailure(t *testing.T) {
	attempts := &stubAttempts{}
	reader := newReconcileReader(attempts, &stubTokens{})
	if err := reader.RecordFailure(context.Background(), "pub-1", "upstream down"); err != nil {
		t.Fatalf("RecordFailure returned %v", err)
	}
	if len(attempts.failures) != 1 || attempts.failures[0] != "pub-1:upstream down" {
		t.Fatalf("failures = %v", attempts.failures)
	}
}
