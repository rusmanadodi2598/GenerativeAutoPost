// Package publish applies webhook status events to attempt rows.
//
// @file      internal/publish/status.go
// @for       StatusUpdater implementation over the attempt aggregate.
// @uses      context, errors, internal/apierror
// @reason    Webhook delivery is the primary source of publish state, so these writes go through the same state machine as the request paths.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-15
package publish

import (
	"context"
	"errors"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// errNoAttemptStore names the wiring fault behind an unconfigured attempt store.
var errNoAttemptStore = errors.New("publish: attempt store is not configured")

// MarkPublishComplete settles an attempt with the TikTok post id.
func (s *Service) MarkPublishComplete(ctx context.Context, publishID, postID string) error {
	return s.transition(ctx, publishID, func(a *Attempt) error {
		return a.MarkComplete(postID)
	})
}

// MarkPublishFailed parks an attempt in FAILED with the upstream reason.
func (s *Service) MarkPublishFailed(ctx context.Context, publishID, reason string) error {
	return s.transition(ctx, publishID, func(a *Attempt) error {
		return a.MarkFailed(reason)
	})
}

// MarkInboxDelivered settles an upload that landed in the user inbox.
func (s *Service) MarkInboxDelivered(ctx context.Context, publishID string) error {
	return s.transition(ctx, publishID, func(a *Attempt) error {
		return a.MarkInboxDelivered()
	})
}

// MarkUnpublished acknowledges the no-longer-public event. There is no status
// for it: the attempt stays PUBLISH_COMPLETE and the post id stays, so the
// console keeps the history TikTok reported.
func (s *Service) MarkUnpublished(ctx context.Context, publishID, postID string) error {
	rec, err := s.attemptByPublishID(ctx, publishID)
	if err != nil {
		return err
	}
	if rec.PostID == "" && postID != "" {
		rec.PostID = postID
	}
	return s.chunks.attempts.Update(ctx, rec)
}

// transition loads one attempt, applies the domain rule, and persists the
// result, so a webhook can never write a status the aggregate forbids.
func (s *Service) transition(ctx context.Context, publishID string,
	apply func(*Attempt) error) error {
	rec, err := s.attemptByPublishID(ctx, publishID)
	if err != nil {
		return err
	}
	att, err := Hydrate(rec)
	if err != nil {
		return err
	}
	if err := apply(&att); err != nil {
		return err
	}
	rec.Status = att.Status()
	rec.FailReason = att.FailReason()
	rec.PostID = att.PostID()
	rec.UploadedBytes = att.UploadedBytes()
	return s.chunks.attempts.Update(ctx, rec)
}

// attemptByPublishID loads the stored attempt a webhook event refers to.
func (s *Service) attemptByPublishID(ctx context.Context, publishID string) (Record, error) {
	if s.chunks.attempts == nil {
		return Record{}, apierror.New(apierror.CodeInternal, 0,
			"Publish status storage is not configured.").Wrap(errNoAttemptStore)
	}
	return s.chunks.attempts.GetByPublishID(ctx, publishID)
}
