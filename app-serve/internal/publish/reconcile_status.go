// Package publish reconciles one attempt on operator demand.
//
// @file      internal/publish/reconcile_status.go
// @for       Manual status fetch for the reconcile endpoint.
// @uses      context, internal/apierror
// @reason    The manual endpoint is the polling fallback when webhooks and the sweep both missed a delivery.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-16
package publish

import (
	"context"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// reportFailedReason names the verdict when TikTok reports a failure with no reason.
const reportFailedReason = "TIKTOK_REPORTED_FAILED"

// Reconcile re-reads the upstream status of one attempt the operator owns and
// stores the verdict. Ownership is checked before the fetch, so a foreign id
// never spends an upstream call.
func (s *Service) Reconcile(ctx context.Context, operatorID, attemptID string) (Record, error) {
	rec, err := s.attemptByID(ctx, attemptID)
	if err != nil {
		return Record{}, err
	}
	access, err := s.accounts.Owned(ctx, operatorID, rec.AccountID)
	if err != nil {
		return Record{}, err
	}
	status, err := s.poster.FetchStatus(ctx, access, rec.PublishID)
	if err != nil {
		return Record{}, err
	}
	return s.applyVerdict(ctx, rec, status)
}

// attemptByID loads the stored attempt the manual reconcile targets.
func (s *Service) attemptByID(ctx context.Context, attemptID string) (Record, error) {
	if s.chunks.attempts == nil {
		return Record{}, apierror.New(apierror.CodeInternal, 0,
			"Publish status storage is not configured.").Wrap(errNoAttemptStore)
	}
	return s.chunks.attempts.Get(ctx, attemptID)
}

// applyVerdict maps one upstream status onto the attempt. A settled verdict
// travels the aggregate so the state machine stays authoritative; an open
// verdict only refreshes the confirmed byte count.
func (s *Service) applyVerdict(ctx context.Context, rec Record, upstream tiktokclient.PublishStatus) (Record, error) {
	att, err := Hydrate(rec)
	if err != nil {
		return Record{}, err
	}
	switch upstream.Status {
	case string(StatusPublishComplete):
		if err := att.MarkComplete(firstID(upstream.PublicalyAvailablePostID)); err != nil {
			return Record{}, err
		}
	case string(StatusFailed):
		reason := upstream.FailReason
		if reason == "" {
			reason = reportFailedReason
		}
		if err := att.MarkFailed(reason); err != nil {
			return Record{}, err
		}
	default:
		rec.UploadedBytes = upstream.UploadedBytes
		if err := s.chunks.attempts.Update(ctx, rec); err != nil {
			return Record{}, err
		}
		return rec, nil
	}
	rec.Status = att.Status()
	rec.FailReason = att.FailReason()
	rec.PostID = att.PostID()
	if err := s.chunks.attempts.Update(ctx, rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// firstID returns the first non-empty id, or an empty string.
func firstID(ids []string) string {
	for _, id := range ids {
		if id != "" {
			return id
		}
	}
	return ""
}
