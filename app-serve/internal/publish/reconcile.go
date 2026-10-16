// Package publish reads stale open attempts for the reconcile sweep.
//
// @file      internal/publish/reconcile.go
// @for       Open attempt sweep queries and the persisted failure counter.
// @uses      context, fmt, time, internal/apierror
// @reason    The polling fallback needs a bounded, oldest first read of attempts that have not settled.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-15
package publish

import (
	"context"
	"fmt"
	"time"
)

// OpenAttemptRow is one stale attempt the sweep must poll. The access token is
// not selected here: the reconciler resolves it through the account store, so
// this query never touches the token columns.
type OpenAttemptRow struct {
	PublishID     string
	AccountID     string
	Status        Status
	FailReason    string
	PostID        string
	UploadedBytes int64
	Failures      int
	CreatedAt     time.Time
}

// openAttemptsQuery lists unsettled attempts older than the threshold, oldest
// first, bounded by the caller's limit. The status filter mirrors the two
// terminal values in the publish_attempts CHECK constraint.
const openAttemptsQuery = `SELECT publish_id, account_id, status, COALESCE(fail_reason, ''),
		COALESCE(post_id, ''), uploaded_bytes, reconcile_failures, created_at
	FROM publish_attempts
	WHERE status NOT IN ('PUBLISH_COMPLETE', 'FAILED') AND created_at <= $1
	ORDER BY created_at ASC LIMIT $2`

// OpenAttempts returns the stale open attempts the reconcile sweep may poll.
func (r *Repository) OpenAttempts(ctx context.Context, olderThan time.Time, limit int) ([]OpenAttemptRow, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	rows, err := r.pool.Query(ctx, openAttemptsQuery, olderThan.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("publish: listing open attempts: %w", err)
	}
	defer rows.Close()
	out := make([]OpenAttemptRow, 0, limit)
	for rows.Next() {
		var (
			rec       OpenAttemptRow
			statusRaw string
		)
		if err := rows.Scan(&rec.PublishID, &rec.AccountID, &statusRaw, &rec.FailReason,
			&rec.PostID, &rec.UploadedBytes, &rec.Failures, &rec.CreatedAt); err != nil {
			return nil, fmt.Errorf("publish: scanning open attempt: %w", err)
		}
		rec.Status = Status(statusRaw)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("publish: iterating open attempts: %w", err)
	}
	return out, nil
}

// RecordFailure increments the reconcile failure counter and stores the latest
// reason, so a cap the sweep reached on one pass is still known on the next.
func (r *Repository) RecordFailure(ctx context.Context, publishID string, reason string) error {
	const q = `UPDATE publish_attempts
		SET reconcile_failures = reconcile_failures + 1, fail_reason = NULLIF($2, '')
		WHERE publish_id = $1`
	tag, err := r.pool.Exec(ctx, q, publishID, reason)
	if err != nil {
		return fmt.Errorf("publish: recording reconcile failure: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errAttemptNotFound()
	}
	return nil
}

// RecordProgress stores confirmed upload bytes for one publish id. It exists
// for the reconcile sweep, which knows the TikTok publish id rather than our
// attempt id.
func (r *Repository) RecordProgress(ctx context.Context, publishID string, uploadedBytes int64) error {
	const q = `UPDATE publish_attempts SET uploaded_bytes = $2 WHERE publish_id = $1`
	tag, err := r.pool.Exec(ctx, q, publishID, uploadedBytes)
	if err != nil {
		return fmt.Errorf("publish: recording reconcile progress: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errAttemptNotFound()
	}
	return nil
}
