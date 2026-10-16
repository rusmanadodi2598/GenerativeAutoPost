// Package publish proves an attempt keeps the media it posted.
//
// @file      internal/publish/media_id_test.go
// @for       Tests that the init flows store and expose media_id.
// @uses      testing, context
// @reason    The contract requires media_id on every init, so dropping it on the way to the row loses the link the console reads.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-16
package publish

import (
	"context"
	"testing"
)

// TestInitVideoStoresTheMediaID proves the accepted media_id reaches the row,
// since a published attempt with no media link cannot be traced back.
func TestInitVideoStoresTheMediaID(t *testing.T) {
	h := newHarness(t)
	req := validVideoRequest()
	req.MediaID = testMediaID

	if _, err := h.svc.InitVideo(context.Background(), testOperator, req, false, "key-video-media"); err != nil {
		t.Fatalf("InitVideo() error = %v", err)
	}
	if len(h.attempts.inserted) != 1 {
		t.Fatalf("want one inserted attempt, got %d", len(h.attempts.inserted))
	}
	if h.attempts.inserted[0].MediaID != testMediaID {
		t.Fatalf("inserted MediaID = %q, want %q", h.attempts.inserted[0].MediaID, testMediaID)
	}
}

// TestInitPhotoStoresTheMediaID proves the photo flow keeps the same link.
func TestInitPhotoStoresTheMediaID(t *testing.T) {
	h := newHarness(t)

	if _, err := h.svc.InitPhoto(context.Background(), testOperator, validPhotoRequest(), false, "key-photo-media"); err != nil {
		t.Fatalf("InitPhoto() error = %v", err)
	}
	if h.attempts.inserted[0].MediaID != testMediaID {
		t.Fatalf("inserted MediaID = %q, want %q", h.attempts.inserted[0].MediaID, testMediaID)
	}
}

// TestInitDraftStoresTheMediaID proves the draft flow keeps the same link.
func TestInitDraftStoresTheMediaID(t *testing.T) {
	h := newHarness(t)
	req := InitDraftRequest{AccountID: testAccountID, MediaID: testMediaID, Source: sourceFileUpload}

	if _, err := h.svc.InitDraft(context.Background(), testOperator, req, "key-draft-media"); err != nil {
		t.Fatalf("InitDraft() error = %v", err)
	}
	if h.attempts.inserted[0].MediaID != testMediaID {
		t.Fatalf("inserted MediaID = %q, want %q", h.attempts.inserted[0].MediaID, testMediaID)
	}
}

// TestAttemptViewExposesTheMediaID proves the console can read the link back.
func TestAttemptViewExposesTheMediaID(t *testing.T) {
	view := viewAttempt(Record{ID: "attempt-1", MediaID: testMediaID})

	if view.MediaID != testMediaID {
		t.Fatalf("view MediaID = %q, want %q", view.MediaID, testMediaID)
	}
}
