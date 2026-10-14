// Package webhook resolves stale open attempts and relays the outbox.
//
// @file      internal/webhook/open_attempts.go
// @for       Open attempt sweep and outbox drain for the webhook reconciler.
// @uses      context, fmt, log/slog, math/rand, time
// @reason    Keeps the polling fallback and the outbox drain beside ReconcileOnce without crowding reconciler.go past the line limit.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     worker
// @stability stable
// @since     2026-10-12
package webhook

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

// EventSink publishes one outbox row to its consumer. The concrete sink is
// wired by the caller; a failure leaves published_at null for the next pass.
type EventSink interface {
	Publish(ctx context.Context, rec OutboxRecord) error
}

// Backoff bounds for retrying one stale attempt inside a sweep. The floor is
// what stops two retries of the same attempt from landing back to back.
const (
	minOpenBackoff = 250 * time.Millisecond
	maxOpenBackoff = 5 * time.Second
)

// openBackoff returns a jittered delay for a retry, growing with the attempt's
// failure count. The floor keeps consecutive retries separated even when the
// jitter draws near zero.
func openBackoff(failures int) time.Duration {
	delay := minOpenBackoff << failures
	if delay > maxOpenBackoff || delay <= 0 {
		delay = maxOpenBackoff
	}
	jittered := time.Duration(rand.Int63n(int64(delay)))
	if jittered < minOpenBackoff {
		jittered = minOpenBackoff
	}
	return jittered
}

// resolveOpen polls the upstream status for stale open attempts through the
// shared limiter, then applies each answer. A nil reader means the wiring is
// not there yet and the sweep is a no-op rather than an error.
func (r *Reconciler) resolveOpen(ctx context.Context) error {
	if r.attempts == nil {
		return nil
	}
	open, err := r.attempts.OpenAttempts(ctx, time.Now().UTC().Add(-r.cfg.OpenAfter), r.cfg.BatchSize)
	if err != nil {
		return err
	}
	for _, att := range open {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.resolveOne(ctx, att)
	}
	return nil
}

// resolveOne polls one attempt and applies the verdict. A limiter refusal is
// counted as a failure, so the sweep backs off instead of hammering the read.
func (r *Reconciler) resolveOne(ctx context.Context, att OpenAttempt) {
	if r.gate != nil {
		if err := r.gate.Allow(ctx, att.AccountID); err != nil {
			r.onOpenFailure(ctx, att, err)
			return
		}
	}
	update, err := r.fetch.FetchStatus(ctx, att.AccessToken, att.PublishID)
	if err != nil {
		r.onOpenFailure(ctx, att, err)
		return
	}
	r.onOpenUpdate(ctx, att, update)
}

// onOpenFailure persists one more failure and stamps the exhausted reason at
// the cap. The status is left FAILED through the shared updater, never
// invented. Both branches record the failure, so the next sweep reads the
// count back instead of re-polling the same attempt forever.
func (r *Reconciler) onOpenFailure(ctx context.Context, att OpenAttempt, cause error) {
	if att.Failures+1 >= r.cfg.MaxAttempts {
		r.recordFailure(ctx, att, exhaustReason)
		if err := r.statuses.MarkPublishFailed(ctx, att.PublishID, exhaustReason); err != nil {
			r.logger.Error("webhook reconcile could not stamp the exhausted attempt",
				"publish_id", att.PublishID, "error", err.Error())
			return
		}
		r.logger.Error("webhook reconcile exhausted, attempt stays failed",
			"publish_id", att.PublishID, "error", cause.Error())
		return
	}
	r.recordFailure(ctx, att, cause.Error())
	r.logger.Warn("webhook reconcile attempt failed, will retry",
		"publish_id", att.PublishID, "failures", att.Failures+1,
		"retry_after", openBackoff(att.Failures), "error", cause.Error())
}

// recordFailure persists the failure count for one attempt. A write failure is
// logged and swallowed: the attempt stays visible to the sweep, which is the
// safe direction compared with settling it on a stale count.
func (r *Reconciler) recordFailure(ctx context.Context, att OpenAttempt, reason string) {
	if err := r.attempts.RecordFailure(ctx, att.PublishID, reason); err != nil {
		r.logger.Error("webhook reconcile could not record the attempt failure",
			"publish_id", att.PublishID, "reason", reason, "error", err.Error())
	}
}

// onOpenUpdate maps a polled verdict onto the publish state machine. Open
// verdicts only move bytes, so the next sweep polls again until the attempt
// settles or the failure cap is reached.
func (r *Reconciler) onOpenUpdate(ctx context.Context, att OpenAttempt, update StatusUpdate) {
	switch update.Status {
	case "PUBLISH_COMPLETE":
		if err := r.statuses.MarkPublishComplete(ctx, att.PublishID, firstPostID(update.PostIDs, att.PostID)); err != nil {
			r.logger.Error("webhook reconcile could not settle the completed attempt",
				"publish_id", att.PublishID, "error", err.Error())
		}
	case "FAILED":
		reason := update.FailReason
		if reason == "" {
			reason = "TIKTOK_REPORTED_FAILED"
		}
		if err := r.statuses.MarkPublishFailed(ctx, att.PublishID, reason); err != nil {
			r.logger.Error("webhook reconcile could not settle the failed attempt",
				"publish_id", att.PublishID, "error", err.Error())
		}
	default:
		r.recordProgress(ctx, att.PublishID, update.UploadedBytes)
	}
}

// recordProgress stores polled bytes when a recorder is wired. An upstream
// verdict with no bytes is not a reason to write a zero over confirmed counts.
func (r *Reconciler) recordProgress(ctx context.Context, publishID string, count int64) {
	if r.progress == nil || count <= 0 {
		return
	}
	if err := r.progress.RecordProgress(ctx, publishID, count); err != nil {
		r.logger.Warn("webhook reconcile progress write failed",
			"publish_id", publishID, "error", err.Error())
	}
}

// firstPostID prefers the fetched ids and falls back to the attempt's own id.
func firstPostID(ids []string, fallback string) string {
	if len(ids) > 0 && ids[0] != "" {
		return ids[0]
	}
	return fallback
}

// DrainOutbox hands each pending row to the sink once per pass and stamps
// published_at only on success. A sink failure leaves the row pending for the
// next pass, so no row is lost. The stamp follows the publish, so a crash
// between the two replays the row on the next pass: the consumer must be
// idempotent on the outbox id.
func (r *Reconciler) DrainOutbox(ctx context.Context, sink EventSink) error {
	if sink == nil {
		return nil
	}
	rows, err := r.store.OutboxPending(ctx, r.cfg.BatchSize)
	if err != nil {
		return fmt.Errorf("webhook: listing outbox rows: %w", err)
	}
	seen := make(map[string]struct{}, len(rows))
	for _, rec := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, dup := seen[rec.ID]; dup {
			continue
		}
		seen[rec.ID] = struct{}{}
		if err := sink.Publish(ctx, rec); err != nil {
			r.logger.Warn("webhook outbox publish failed, row stays pending",
				"outbox_id", rec.ID, "error", err.Error())
			continue
		}
		if err := r.store.MarkOutboxPublished(ctx, rec.ID, time.Now().UTC()); err != nil {
			return fmt.Errorf("webhook: marking outbox published: %w", err)
		}
	}
	return nil
}
