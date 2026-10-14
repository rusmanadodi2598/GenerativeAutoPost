// Package publish tests the publish attempt state machine.
//
// @file      internal/publish/domain_test.go
// @for       Table driven coverage of every legal and illegal transition.
// @uses      testing, internal/apierror
// @reason    The state machine guards a user visible publish flow.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     domain
// @stability stable
// @since     2026-10-12
package publish

import (
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// allStatuses is every stored status value, terminal states included.
var allStatuses = []Status{
	StatusCreated,
	StatusProcessingUpload,
	StatusProcessingDownload,
	StatusSendToUserInbox,
	StatusPublishComplete,
	StatusFailed,
}

// transition is one method under test with a fixed non empty argument.
type transition struct {
	name string
	call func(*Attempt) error
}

// transitions lists every transition method on the aggregate.
var transitions = []transition{
	{"MarkProcessingUpload", (*Attempt).MarkProcessingUpload},
	{"MarkProcessingDownload", (*Attempt).MarkProcessingDownload},
	{"MarkInboxDelivered", (*Attempt).MarkInboxDelivered},
	{"MarkUploaded", func(a *Attempt) error { return a.MarkUploaded(1024) }},
	{"MarkComplete", func(a *Attempt) error { return a.MarkComplete("post-9") }},
	{"MarkFailed", func(a *Attempt) error { return a.MarkFailed("boom") }},
}

// attemptAt builds an attempt parked in the given status.
func attemptAt(t *testing.T, status Status) Attempt {
	t.Helper()
	if status == StatusSendToUserInbox {
		return NewDraft("att-1", "acc-1", "pub-1")
	}
	hydrated, err := Hydrate(Record{ID: "att-1", AccountID: "acc-1", PublishID: "pub-1", Status: status})
	if err != nil {
		t.Fatalf("Hydrate returned error: %v", err)
	}
	return hydrated
}

// legalEdges lists every allowed source status per transition.
var legalEdges = map[string][]Status{
	"MarkProcessingUpload":   {StatusCreated},
	"MarkProcessingDownload": {StatusCreated},
	"MarkInboxDelivered":     {StatusProcessingUpload, StatusProcessingDownload},
	"MarkUploaded":           {StatusProcessingUpload},
	"MarkComplete":           {StatusProcessingUpload, StatusProcessingDownload, StatusSendToUserInbox},
	"MarkFailed":             {StatusCreated, StatusProcessingUpload, StatusProcessingDownload, StatusSendToUserInbox},
}

// TestLegalTransitions checks every allowed edge reaches its target status.
func TestLegalTransitions(t *testing.T) {
	wantStatus := map[string]Status{
		"MarkProcessingUpload":   StatusProcessingUpload,
		"MarkProcessingDownload": StatusProcessingDownload,
		"MarkInboxDelivered":     StatusSendToUserInbox,
		"MarkUploaded":           StatusProcessingUpload,
		"MarkComplete":           StatusPublishComplete,
		"MarkFailed":             StatusFailed,
	}
	for _, tr := range transitions {
		for _, from := range legalEdges[tr.name] {
			t.Run(from.String()+" -> "+tr.name, func(t *testing.T) {
				a := attemptAt(t, from)
				if err := tr.call(&a); err != nil {
					t.Fatalf("transition %s returned error: %v", tr.name, err)
				}
				if a.Status() != wantStatus[tr.name] {
					t.Fatalf("status = %s, want %s", a.Status(), wantStatus[tr.name])
				}
				if tr.name == "MarkComplete" && a.PostID() != "post-9" {
					t.Fatalf("post id = %q, want post-9", a.PostID())
				}
				if tr.name == "MarkFailed" && a.FailReason() != "boom" {
					t.Fatalf("fail reason = %q, want boom", a.FailReason())
				}
			})
		}
	}
}

// TestIllegalTransitionsReject checks every rejected edge returns CONFLICT,
// including both terminal states and the reconcile exhaustion ruling.
func TestIllegalTransitionsReject(t *testing.T) {
	for _, tr := range transitions {
		for _, from := range allStatuses {
			if containsStatus(legalEdges[tr.name], from) {
				continue
			}
			t.Run(from.String()+" -> "+tr.name, func(t *testing.T) {
				a := attemptAt(t, from)
				assertCode(t, tr.call(&a), apierror.CodeConflict)
				if a.Status() != from {
					t.Fatalf("status = %s, want the attempt to stay at %s", a.Status(), from)
				}
			})
		}
	}
}

// TestMarkUploadedAccumulates checks partial upload progress adds up.
func TestMarkUploadedAccumulates(t *testing.T) {
	a := NewAttempt("att-1", "acc-1", "pub-1")
	if err := a.MarkProcessingUpload(); err != nil {
		t.Fatalf("MarkProcessingUpload returned error: %v", err)
	}
	for _, want := range []int64{1024, 2048} {
		if err := a.MarkUploaded(1024); err != nil {
			t.Fatalf("MarkUploaded returned error: %v", err)
		}
		if a.UploadedBytes() != want {
			t.Fatalf("uploaded bytes = %d, want %d", a.UploadedBytes(), want)
		}
	}
}

// TestMarkUploadedRejectsNegativeDelta checks the counter never decreases.
func TestMarkUploadedRejectsNegativeDelta(t *testing.T) {
	a := NewAttempt("att-1", "acc-1", "pub-1")
	if err := a.MarkProcessingUpload(); err != nil {
		t.Fatalf("MarkProcessingUpload returned error: %v", err)
	}
	if err := a.MarkUploaded(4096); err != nil {
		t.Fatalf("MarkUploaded returned error: %v", err)
	}
	assertCode(t, a.MarkUploaded(-1), apierror.CodeValidationFailed)
	if a.UploadedBytes() != 4096 {
		t.Fatalf("uploaded bytes = %d, want 4096 after the rejected delta", a.UploadedBytes())
	}
}

// TestMarkCompleteRequiresPostID checks the post id guard.
func TestMarkCompleteRequiresPostID(t *testing.T) {
	a := NewAttempt("att-1", "acc-1", "pub-1")
	if err := a.MarkProcessingDownload(); err != nil {
		t.Fatalf("MarkProcessingDownload returned error: %v", err)
	}
	assertCode(t, a.MarkComplete(""), apierror.CodeValidationFailed)
	if a.Status() != StatusProcessingDownload {
		t.Fatalf("status = %s, want the attempt to stay put", a.Status())
	}
}

// TestMarkFailedRequiresReason checks the fail reason guard.
func TestMarkFailedRequiresReason(t *testing.T) {
	a := NewAttempt("att-1", "acc-1", "pub-1")
	assertCode(t, a.MarkFailed(""), apierror.CodeValidationFailed)
	if a.Status() != StatusCreated {
		t.Fatalf("status = %s, want the attempt to stay put", a.Status())
	}
}

// TestNewAttemptStartsCreated checks the initial state of a media attempt.
func TestNewAttemptStartsCreated(t *testing.T) {
	a := NewAttempt("att-1", "acc-1", "pub-1")
	if a.Status() != StatusCreated {
		t.Fatalf("status = %s, want CREATED", a.Status())
	}
	if a.ID() != "att-1" || a.AccountID() != "acc-1" || a.PublishID() != "pub-1" {
		t.Fatalf("unexpected identity: %s %s %s", a.ID(), a.AccountID(), a.PublishID())
	}
}

// TestNewDraftStartsInInbox checks drafts skip the processing stages.
func TestNewDraftStartsInInbox(t *testing.T) {
	a := NewDraft("att-2", "acc-1", "pub-2")
	if a.Status() != StatusSendToUserInbox {
		t.Fatalf("status = %s, want SEND_TO_USER_INBOX", a.Status())
	}
	assertCode(t, a.MarkProcessingUpload(), apierror.CodeConflict)
}

// TestHydrateRestoresEveryField checks reconstruction from a stored row.
func TestHydrateRestoresEveryField(t *testing.T) {
	rec := Record{
		ID:            "att-3",
		AccountID:     "acc-1",
		PublishID:     "pub-3",
		Status:        StatusFailed,
		FailReason:    FailReasonReconcileExhausted,
		UploadedBytes: 2048,
	}
	a, err := Hydrate(rec)
	if err != nil {
		t.Fatalf("Hydrate returned error: %v", err)
	}
	if a.ID() != rec.ID || a.AccountID() != rec.AccountID || a.PublishID() != rec.PublishID {
		t.Fatalf("hydrated identity mismatch: %+v", a)
	}
	if a.Status() != StatusFailed || a.FailReason() != FailReasonReconcileExhausted {
		t.Fatalf("hydrated status and reason mismatch: %s %s", a.Status(), a.FailReason())
	}
	if a.UploadedBytes() != 2048 {
		t.Fatalf("uploaded bytes = %d, want 2048", a.UploadedBytes())
	}
}

// TestHydrateRejectsUnknownStatus keeps the CHECK constraint authoritative.
func TestHydrateRejectsUnknownStatus(t *testing.T) {
	_, err := Hydrate(Record{ID: "att-4", Status: "RECONCILE_EXHAUSTED"})
	assertCode(t, err, apierror.CodeValidationFailed)
}

// containsStatus reports whether the list holds the status.
func containsStatus(list []Status, status Status) bool {
	for _, s := range list {
		if s == status {
			return true
		}
	}
	return false
}

// assertCode fails unless err is an apierror carrying the wanted code.
func assertCode(t *testing.T, err error, want apierror.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with code %s, got nil", want)
	}
	typed, ok := err.(*apierror.Error)
	if !ok {
		t.Fatalf("want *apierror.Error, got %T", err)
	}
	if typed.Code != want {
		t.Fatalf("code = %s, want %s", typed.Code, want)
	}
}
