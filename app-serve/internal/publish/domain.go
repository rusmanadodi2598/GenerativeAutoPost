// Package publish carries the publish attempt aggregate and its state machine.
//
// @file      internal/publish/domain.go
// @for       PublishAttempt aggregate enforcing the attempt lifecycle.
// @uses      internal/apierror
// @reason    Transition rules belong on the aggregate, not on the service.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     domain
// @stability stable
// @since     2026-10-12
package publish

import (
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// Status is one row value of the publish_attempts status check constraint.
type Status string

// Attempt statuses mirror the publish_attempts CHECK constraint exactly.
const (
	StatusCreated            Status = "CREATED"
	StatusProcessingUpload   Status = "PROCESSING_UPLOAD"
	StatusProcessingDownload Status = "PROCESSING_DOWNLOAD"
	StatusSendToUserInbox    Status = "SEND_TO_USER_INBOX"
	StatusPublishComplete    Status = "PUBLISH_COMPLETE"
	StatusFailed             Status = "FAILED"
)

// FailReasonReconcileExhausted is a fail_reason value, never a status.
const FailReasonReconcileExhausted = "RECONCILE_EXHAUSTED"

// String returns the wire value of the status.
func (s Status) String() string { return string(s) }

// valid reports whether the status is one of the six stored values.
func (s Status) valid() bool {
	switch s {
	case StatusCreated, StatusProcessingUpload, StatusProcessingDownload,
		StatusSendToUserInbox, StatusPublishComplete, StatusFailed:
		return true
	default:
		return false
	}
}

// Attempt is the aggregate root for one publish attempt. Fields stay
// unexported so status only moves through the methods below.
type Attempt struct {
	id            string
	accountID     string
	publishID     string
	status        Status
	failReason    string
	postID        string
	uploadedBytes int64
}

// Record is the minimal stored row shape the repository rehydrates from.
type Record struct {
	ID            string
	AccountID     string
	PublishID     string
	Status        Status
	FailReason    string
	PostID        string
	UploadedBytes int64
}

// NewAttempt starts a video or photo attempt in CREATED.
func NewAttempt(id, accountID, publishID string) Attempt {
	return Attempt{id: id, accountID: accountID, publishID: publishID, status: StatusCreated}
}

// NewDraft starts a draft attempt already delivered to the user inbox.
func NewDraft(id, accountID, publishID string) Attempt {
	return Attempt{id: id, accountID: accountID, publishID: publishID, status: StatusSendToUserInbox}
}

// Hydrate rebuilds an attempt from a stored row for the repository in 9b.
func Hydrate(rec Record) (Attempt, error) {
	if !rec.Status.valid() {
		return Attempt{}, apierror.New(apierror.CodeValidationFailed, 0, "Stored status is not a known publish status.")
	}
	if rec.UploadedBytes < 0 {
		return Attempt{}, apierror.New(apierror.CodeValidationFailed, 0, "Stored uploaded bytes must not be negative.")
	}
	return Attempt{
		id:            rec.ID,
		accountID:     rec.AccountID,
		publishID:     rec.PublishID,
		status:        rec.Status,
		failReason:    rec.FailReason,
		postID:        rec.PostID,
		uploadedBytes: rec.UploadedBytes,
	}, nil
}

// ID returns the attempt identifier.
func (a *Attempt) ID() string { return a.id }

// AccountID returns the owning account identifier.
func (a *Attempt) AccountID() string { return a.accountID }

// PublishID returns the TikTok publish id.
func (a *Attempt) PublishID() string { return a.publishID }

// Status returns the current lifecycle status.
func (a *Attempt) Status() Status { return a.status }

// FailReason returns the failure reason, empty unless the attempt failed.
func (a *Attempt) FailReason() string { return a.failReason }

// PostID returns the completed post id, empty until completion.
func (a *Attempt) PostID() string { return a.postID }

// UploadedBytes returns the confirmed upload progress in bytes.
func (a *Attempt) UploadedBytes() int64 { return a.uploadedBytes }

// MarkProcessingUpload moves CREATED to PROCESSING_UPLOAD.
func (a *Attempt) MarkProcessingUpload() error {
	if a.status != StatusCreated {
		return errIllegalTransition(a.status, "MarkProcessingUpload")
	}
	a.status = StatusProcessingUpload
	return nil
}

// MarkProcessingDownload moves CREATED to PROCESSING_DOWNLOAD.
func (a *Attempt) MarkProcessingDownload() error {
	if a.status != StatusCreated {
		return errIllegalTransition(a.status, "MarkProcessingDownload")
	}
	a.status = StatusProcessingDownload
	return nil
}

// MarkInboxDelivered moves an active upload or download to SEND_TO_USER_INBOX.
func (a *Attempt) MarkInboxDelivered() error {
	if a.status != StatusProcessingUpload && a.status != StatusProcessingDownload {
		return errIllegalTransition(a.status, "MarkInboxDelivered")
	}
	a.status = StatusSendToUserInbox
	return nil
}

// MarkComplete finishes an active attempt with the TikTok post id.
func (a *Attempt) MarkComplete(postID string) error {
	if a.status != StatusProcessingUpload && a.status != StatusProcessingDownload && a.status != StatusSendToUserInbox {
		return errIllegalTransition(a.status, "MarkComplete")
	}
	if postID == "" {
		return apierror.New(apierror.CodeValidationFailed, 0, "Post id is required to complete a publish attempt.")
	}
	a.postID = postID
	a.status = StatusPublishComplete
	return nil
}

// MarkFailed parks a non terminal attempt in FAILED with a reason.
// RECONCILE_EXHAUSTED travels as the reason, never as a status.
func (a *Attempt) MarkFailed(reason string) error {
	if a.status == StatusPublishComplete || a.status == StatusFailed {
		return errIllegalTransition(a.status, "MarkFailed")
	}
	if reason == "" {
		return apierror.New(apierror.CodeValidationFailed, 0, "Fail reason is required to fail a publish attempt.")
	}
	a.failReason = reason
	a.status = StatusFailed
	return nil
}

// MarkUploaded adds confirmed bytes during PROCESSING_UPLOAD only.
func (a *Attempt) MarkUploaded(bytesDelta int64) error {
	if a.status != StatusProcessingUpload {
		return errIllegalTransition(a.status, "MarkUploaded")
	}
	if bytesDelta < 0 {
		return apierror.New(apierror.CodeValidationFailed, 0, "Uploaded bytes delta must not be negative.")
	}
	a.uploadedBytes += bytesDelta
	return nil
}

// errIllegalTransition is the client safe answer for a rejected edge.
func errIllegalTransition(from Status, method string) *apierror.Error {
	return apierror.New(apierror.CodeConflict, 0, "Cannot "+method+" from status "+from.String()+".")
}
