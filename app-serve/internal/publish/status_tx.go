// Package publish writes status transitions through an open transaction.
//
// @file      internal/publish/status_tx.go
// @for       Transaction bound writer for the webhook dispatch commit.
// @uses      context, internal/apierror
// @reason    The outbox row and the state change commit in one transaction, so the dispatcher never leaves one without the other.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-15
package publish

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// Tx is the write handle a caller owns, kept here so the bind signature needs
// no webhook import. pgx.Tx satisfies it, so the real commit runs untouched
// and a test can record the statements instead.
type Tx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// StatusUpdater is the transaction bound status writer the webhook dispatcher
// asks for. It is declared here so publish does not import webhook, and the
// composition root wires the two together.
type StatusUpdater interface {
	MarkPublishComplete(ctx context.Context, publishID, postID string) error
	MarkPublishFailed(ctx context.Context, publishID, reason string) error
	MarkInboxDelivered(ctx context.Context, publishID string) error
	MarkUnpublished(ctx context.Context, publishID, postID string) error
}

// InTx binds this repository to an open transaction and returns the status
// writer bound to it, so the dispatcher's state change and outbox row commit
// together.
func (r *Repository) InTx(tx Tx) StatusUpdater {
	return newTxStatusWriter(tx)
}

// txStatuses writes attempt transitions through a caller owned transaction.
type txStatuses struct {
	tx Tx
}

// newTxStatusWriter binds a status writer to an open transaction.
func newTxStatusWriter(tx Tx) *txStatuses {
	return &txStatuses{tx: tx}
}

// MarkPublishComplete settles the attempt with the TikTok post id.
func (u *txStatuses) MarkPublishComplete(ctx context.Context, publishID, postID string) error {
	if postID == "" {
		return apierror.New(apierror.CodeValidationFailed, 0, "Post id is required to complete a publish attempt.")
	}
	const q = `UPDATE publish_attempts SET status = 'PUBLISH_COMPLETE', post_id = NULLIF($2, ''), completed_at = now()
		WHERE publish_id = $1 AND status NOT IN ('PUBLISH_COMPLETE', 'FAILED')`
	return u.exec(ctx, q, publishID, postID)
}

// MarkPublishFailed parks the attempt in FAILED with the upstream reason.
func (u *txStatuses) MarkPublishFailed(ctx context.Context, publishID, reason string) error {
	if reason == "" {
		return apierror.New(apierror.CodeValidationFailed, 0, "Fail reason is required to fail a publish attempt.")
	}
	const q = `UPDATE publish_attempts SET status = 'FAILED', fail_reason = NULLIF($2, ''), completed_at = now()
		WHERE publish_id = $1 AND status NOT IN ('PUBLISH_COMPLETE', 'FAILED')`
	return u.exec(ctx, q, publishID, reason)
}

// MarkInboxDelivered settles an upload that landed in the user inbox.
func (u *txStatuses) MarkInboxDelivered(ctx context.Context, publishID string) error {
	const q = `UPDATE publish_attempts SET status = 'SEND_TO_USER_INBOX'
		WHERE publish_id = $1 AND status NOT IN ('PUBLISH_COMPLETE', 'FAILED')`
	return u.exec(ctx, q, publishID)
}

// MarkUnpublished keeps the settled status and only fills a missing post id.
func (u *txStatuses) MarkUnpublished(ctx context.Context, publishID, postID string) error {
	const q = `UPDATE publish_attempts SET post_id = COALESCE(NULLIF(post_id, ''), NULLIF($2, ''))
		WHERE publish_id = $1`
	return u.exec(ctx, q, publishID, postID)
}

// exec runs one guarded update through the transaction and maps a miss to the
// shared not found error, since a delivery must never silently apply to zero rows.
func (u *txStatuses) exec(ctx context.Context, query, publishID string, extra ...any) error {
	tag, err := u.tx.Exec(ctx, query, append([]any{publishID}, extra...)...)
	if err != nil {
		return fmt.Errorf("publish: applying transition in transaction: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errAttemptNotFound()
	}
	return nil
}
