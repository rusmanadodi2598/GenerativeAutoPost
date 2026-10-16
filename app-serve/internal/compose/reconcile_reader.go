// Package compose maps stale publish attempts onto the reconcile reader.
//
// @file      internal/compose/reconcile_reader.go
// @for       OpenAttemptReader adapter over the publish and account stores.
// @uses      context, time, internal/publish, internal/webhook
// @reason    The reconciler sweeps every operator's attempts, so the token it polls with resolves through a system scoped lookup.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-15
package compose

import (
	"context"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/publish"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/webhook"
)

// attemptSweeper lists stale open attempts and persists their failure count.
type attemptSweeper interface {
	OpenAttempts(ctx context.Context, olderThan time.Time, limit int) ([]publish.OpenAttemptRow, error)
	RecordFailure(ctx context.Context, publishID, reason string) error
}

// tokenResolver decrypts the access token of one account with no operator
// session, which the background sweep has no way to present.
type tokenResolver interface {
	AccessForAccount(ctx context.Context, accountID string) (string, error)
}

// reconcileReader adapts the publish store to the webhook sweep port.
type reconcileReader struct {
	attempts attemptSweeper
	tokens   tokenResolver
}

// newReconcileReader wires the sweep reader over the publish store and the
// system scoped token resolver.
func newReconcileReader(attempts attemptSweeper, tokens tokenResolver) reconcileReader {
	return reconcileReader{attempts: attempts, tokens: tokens}
}

// OpenAttempts lists stale attempts with the token each poll needs. A row whose
// token cannot be resolved is skipped, so one revoked account cannot stall the
// whole sweep.
func (r reconcileReader) OpenAttempts(ctx context.Context, olderThan time.Time, limit int) ([]webhook.OpenAttempt, error) {
	rows, err := r.attempts.OpenAttempts(ctx, olderThan, limit)
	if err != nil {
		return nil, err
	}
	out := make([]webhook.OpenAttempt, 0, len(rows))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		access, err := r.tokens.AccessForAccount(ctx, row.AccountID)
		if err != nil {
			continue
		}
		out = append(out, webhook.OpenAttempt{
			PublishID: row.PublishID, AccountID: row.AccountID, AccessToken: access,
			Failures: row.Failures, Status: row.Status.String(), FailReason: row.FailReason,
			PostID: row.PostID, UploadedBytes: row.UploadedBytes,
		})
	}
	return out, nil
}

// RecordFailure stores one more failure for an attempt.
func (r reconcileReader) RecordFailure(ctx context.Context, publishID, reason string) error {
	return r.attempts.RecordFailure(ctx, publishID, reason)
}
