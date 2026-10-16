// Package publish pins ownership before privacy validation on every init.
//
// @file      internal/publish/ownership_privacy_test.go
// @for       Regression tests proving a foreign privacy probe cannot enumerate another operator's options.
// @uses      context, testing, internal/apierror
// @reason    A privacy error before the ownership check distinguishes a foreign account's valid option from an invalid one.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-15
package publish

import (
	"context"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// TestInitVideoGivenForeignAccountWithInvalidPrivacy_ThenDeniedWithoutValidatingPrivacy
// proves a foreign account answers the ownership denial even when the privacy
// level is invalid, so an attacker cannot distinguish valid levels of an
// account they do not own.
func TestInitVideoGivenForeignAccountWithInvalidPrivacy_ThenDeniedWithoutValidatingPrivacy(t *testing.T) {
	h := newHarness(t)
	h.accounts.err = apierror.New(apierror.CodeNotFound, 0, "Publish attempt not found.")
	h.svc.creators = &fakeCreators{options: []string{"SELF_ONLY", "PUBLIC"}}
	req := validVideoRequest()
	req.PostInfo.PrivacyLevel = "TOTALLY_INVALID"
	_, err := h.svc.InitVideo(context.Background(), "op-foreign", req, false, "key-foreign")
	if err == nil {
		t.Fatal("InitVideo() error = nil, want the ownership denial")
	}
	if got := errorCode(err); got != string(apierror.CodeNotFound) {
		t.Fatalf("code = %q, want %q (err %v)", got, apierror.CodeNotFound, err)
	}
	if len(h.poster.videoCalls) != 0 {
		t.Fatalf("InitVideoPost calls = %d, want 0", len(h.poster.videoCalls))
	}
}

// TestInitPhotoGivenForeignAccountWithInvalidPrivacy_ThenDeniedWithoutValidatingPrivacy
// mirrors the video case for the photo init flow.
func TestInitPhotoGivenForeignAccountWithInvalidPrivacy_ThenDeniedWithoutValidatingPrivacy(t *testing.T) {
	h := newHarness(t)
	h.accounts.err = apierror.New(apierror.CodeNotFound, 0, "Publish attempt not found.")
	req := validPhotoRequest()
	req.PostInfo.PrivacyLevel = "TOTALLY_INVALID"
	_, err := h.svc.InitPhoto(context.Background(), "op-foreign", req, false, "key-photo-foreign")
	if err == nil {
		t.Fatal("InitPhoto() error = nil, want the ownership denial")
	}
	if got := errorCode(err); got != string(apierror.CodeNotFound) {
		t.Fatalf("code = %q, want %q (err %v)", got, apierror.CodeNotFound, err)
	}
	if len(h.poster.photoCalls) != 0 {
		t.Fatalf("InitPhotoPost calls = %d, want 0", len(h.poster.photoCalls))
	}
}

// TestInitDraftGivenForeignAccount_ThenDeniedBeforeUpstreamCall proves the
// draft flow keeps the same order.
func TestInitDraftGivenForeignAccount_ThenDeniedBeforeUpstreamCall(t *testing.T) {
	h := newHarness(t)
	h.accounts.err = apierror.New(apierror.CodeNotFound, 0, "Publish attempt not found.")
	req := InitDraftRequest{
		AccountID: testAccountID, MediaID: testMediaID, Source: sourceFileUpload,
	}
	_, err := h.svc.InitDraft(context.Background(), "op-foreign", req, "key-draft-foreign")
	if err == nil {
		t.Fatal("InitDraft() error = nil, want the ownership denial")
	}
	if got := errorCode(err); got != string(apierror.CodeNotFound) {
		t.Fatalf("code = %q, want %q (err %v)", got, apierror.CodeNotFound, err)
	}
	if len(h.poster.draftCalls) != 0 {
		t.Fatalf("InitDraftUpload calls = %d, want 0", len(h.poster.draftCalls))
	}
}
