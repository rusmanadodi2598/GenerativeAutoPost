// Package publish re-reads the upstream status of one owned attempt.
//
// @file      internal/publish/reconcile_request_test.go
// @for       Reconcile tests for the manual status fetch web path.
// @uses      context, testing, internal/apierror, internal/tiktokclient
// @reason    The manual endpoint is the operator's polling fallback when webhooks and the sweep both missed a delivery.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-16
package publish

import (
	"context"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// statusPoster serves one status verdict for the reconcile flow.
type statusPoster struct {
	status tiktokclient.PublishStatus
	err    error
}

func (s *statusPoster) QueryCreatorInfo(context.Context, string) (tiktokclient.CreatorInfo, error) {
	return tiktokclient.CreatorInfo{}, nil
}

func (s *statusPoster) InitVideoPost(context.Context, string, tiktokclient.VideoInitRequest) (tiktokclient.VideoInitResponse, error) {
	return tiktokclient.VideoInitResponse{}, nil
}

func (s *statusPoster) InitPhotoPost(context.Context, string, tiktokclient.PhotoInitRequest) (tiktokclient.PhotoInitResponse, error) {
	return tiktokclient.PhotoInitResponse{}, nil
}

func (s *statusPoster) InitDraftUpload(context.Context, string, tiktokclient.DraftInitRequest) (tiktokclient.DraftInitResponse, error) {
	return tiktokclient.DraftInitResponse{}, nil
}

func (s *statusPoster) FetchStatus(_ context.Context, access, publishID string) (tiktokclient.PublishStatus, error) {
	if s.err != nil {
		return tiktokclient.PublishStatus{}, s.err
	}
	return s.status, nil
}

// ownerAccounts resolves every account to a fixed access token.
type ownerAccounts struct {
	access string
	err    error
}

func (o *ownerAccounts) Owned(context.Context, string, string) (string, error) {
	if o.err != nil {
		return "", o.err
	}
	return o.access, nil
}

// reconcileService builds a Service with a fetchable poster and an owned account.
func reconcileService(access string, status tiktokclient.PublishStatus) (*Service, *statusStore) {
	store := newStatusStore()
	svc := NewService(Config{
		Poster:      &statusPoster{status: status},
		Accounts:    &ownerAccounts{access: access},
		Creators:    &fakeCreators{},
		Idempotency: newFakeIdempotency(),
		MediaHost:   "media.example",
	})
	svc.chunks.attempts = store
	return svc, store
}

// TestReconcileGivenUpstreamComplete_ThenSettled proves the manual fetch
// settles the attempt when TikTok says so.
func TestReconcileGivenUpstreamComplete_ThenSettled(t *testing.T) {
	svc, store := reconcileService("access-token", tiktokclient.PublishStatus{
		Status:                   "PUBLISH_COMPLETE",
		PublicalyAvailablePostID: []string{"post-reconciled"},
	})
	store.seed(openAttempt(testPublishID, StatusProcessingUpload))
	rec, err := svc.Reconcile(context.Background(), testOperator, "attempt-1")
	if err != nil {
		t.Fatalf("Reconcile returned %v", err)
	}
	if rec.Status != StatusPublishComplete || rec.PostID != "post-reconciled" {
		t.Fatalf("rec = %+v", rec)
	}
}

// TestReconcileGivenForeignAccount_ThenNotFound proves the fetch never runs
// for an account the operator does not own.
func TestReconcileGivenForeignAccount_ThenNotFound(t *testing.T) {
	store := newStatusStore()
	svc := NewService(Config{
		Poster:      &statusPoster{},
		Accounts:    &ownerAccounts{err: apierror.New(apierror.CodeNotFound, 0, "Publish attempt not found.")},
		Creators:    &fakeCreators{},
		Idempotency: newFakeIdempotency(),
		MediaHost:   "media.example",
	})
	store.seed(openAttempt(testPublishID, StatusProcessingUpload))
	svc.chunks.attempts = store
	err := errorCodeForReconcile(svc, testOperator, "attempt-1")
	if err != "NOT_FOUND" {
		t.Fatalf("code = %q, want NOT_FOUND", err)
	}
}

func errorCodeForReconcile(svc *Service, operatorID, attemptID string) string {
	_, err := svc.Reconcile(context.Background(), operatorID, attemptID)
	return errorCode(err)
}

// TestReconcileGivenUpstreamOpen_ThenBytesRecorded proves an open verdict
// stores the confirmed bytes and leaves the attempt unsettled.
func TestReconcileGivenUpstreamOpen_ThenBytesRecorded(t *testing.T) {
	svc, store := reconcileService("access-token", tiktokclient.PublishStatus{
		Status: "PROCESSING_UPLOAD", UploadedBytes: 4096,
	})
	store.seed(openAttempt(testPublishID, StatusProcessingUpload))
	rec, err := svc.Reconcile(context.Background(), testOperator, "attempt-1")
	if err != nil {
		t.Fatalf("Reconcile returned %v", err)
	}
	if rec.Status != StatusProcessingUpload {
		t.Fatalf("status = %q, want still open", rec.Status)
	}
	if rec.UploadedBytes != 4096 {
		t.Fatalf("UploadedBytes = %d", rec.UploadedBytes)
	}
}

// TestReconcileGivenTerminalAttempt_ThenConflict proves reconciling a settled
// attempt answers a conflict rather than moving it again.
func TestReconcileGivenTerminalAttempt_ThenConflict(t *testing.T) {
	svc, store := reconcileService("access-token", tiktokclient.PublishStatus{
		Status: "PUBLISH_COMPLETE", PublicalyAvailablePostID: []string{"post-x"},
	})
	store.seed(openAttempt(testPublishID, StatusFailed))
	_, err := svc.Reconcile(context.Background(), testOperator, "attempt-1")
	if got := errorCode(err); got != string(apierror.CodeConflict) {
		t.Fatalf("code = %q, want CONFLICT (err %v)", got, err)
	}
}

// TestReconcileGivenUnknownAttempt_ThenNotFound proves a missing id answers
// not found rather than an upstream error.
func TestReconcileGivenUnknownAttempt_ThenNotFound(t *testing.T) {
	svc, _ := reconcileService("access-token", tiktokclient.PublishStatus{})
	_, err := svc.Reconcile(context.Background(), testOperator, "attempt-missing")
	if got := errorCode(err); got != string(apierror.CodeNotFound) {
		t.Fatalf("code = %q, want NOT_FOUND (err %v)", got, err)
	}
}
