// Package publish tables the photo and draft init cases.
//
// @file      internal/publish/service_photo_test.go
// @for       Table tests for photo and draft init validation.
// @uses      context, strings, testing, internal/apierror
// @reason    Pins the photo caps and the draft no privacy routing.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package publish

import (
	"context"
	"strings"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// validPhotoRequest returns a direct post request that passes every guard.
func validPhotoRequest() InitPhotoRequest {
	return InitPhotoRequest{
		AccountID: testAccountID,
		MediaID:   testMediaID,
		PostMode:  modeDirectPost,
		PostInfo: PhotoInfoRequest{
			PostInfoRequest: PostInfoRequest{Title: "sunset", PrivacyLevel: "PUBLIC"},
		},
		PhotoURLs: []string{"https://media.example/a.jpg"},
	}
}

// TestInitPhotoHappyPath returns the 202 shape for a direct photo post.
func TestInitPhotoHappyPath(t *testing.T) {
	h := newHarness(t)
	res, err := h.svc.InitPhoto(context.Background(), testOperator, validPhotoRequest(), false, "key-photo-happy")
	if err != nil {
		t.Fatalf("InitPhoto() error = %v", err)
	}
	if res.PublishID != testPublishID || res.Status != StatusCreated || res.ID == "" {
		t.Fatalf("unexpected result %+v", res)
	}
	if len(h.poster.photoCalls) != 1 {
		t.Fatalf("InitPhotoPost calls = %d, want 1", len(h.poster.photoCalls))
	}
}

// TestInitPhotoRejections tables the photo only guards.
func TestInitPhotoRejections(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*InitPhotoRequest)
		want   string
	}{
		{
			name:   "photo title at 91 fails",
			mutate: func(r *InitPhotoRequest) { r.PostInfo.Title = strings.Repeat("a", 91) },
			want:   string(apierror.CodeValidationFailed),
		},
		{
			name:   "description at 4001 fails",
			mutate: func(r *InitPhotoRequest) { r.PostInfo.Description = strings.Repeat("a", 4001) },
			want:   string(apierror.CodeValidationFailed),
		},
		{
			name: "auto add music outside direct post",
			mutate: func(r *InitPhotoRequest) {
				r.PostMode = "MEDIA_UPLOAD"
				r.PostInfo.AutoAddMusic = true
			},
			want: string(apierror.CodeValidationFailed),
		},
		{
			name:   "privacy not in options",
			mutate: func(r *InitPhotoRequest) { r.PostInfo.PrivacyLevel = "STRANGERS" },
			want:   string(apierror.CodePublishPrivacyMismatch),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			req := validPhotoRequest()
			tc.mutate(&req)
			_, err := h.svc.InitPhoto(context.Background(), testOperator, req, false, "key-"+tc.name)
			if err == nil {
				t.Fatal("InitPhoto() error = nil, want rejection")
			}
			if got := errorCode(err); got != tc.want {
				t.Fatalf("code = %q, want %q (err %v)", got, tc.want, err)
			}
			if len(h.poster.photoCalls) != 0 {
				t.Fatalf("InitPhotoPost calls = %d, want 0", len(h.poster.photoCalls))
			}
		})
	}
}

// TestInitPhotoTitleBoundary90Passes keeps the photo title cap inclusive.
func TestInitPhotoTitleBoundary90Passes(t *testing.T) {
	h := newHarness(t)
	req := validPhotoRequest()
	req.PostInfo.Title = strings.Repeat("a", 90)
	if _, err := h.svc.InitPhoto(context.Background(), testOperator, req, false, "key-photo-90"); err != nil {
		t.Fatalf("InitPhoto() error = %v, want nil", err)
	}
}

// TestInitPhotoMediaUploadSkipsPrivacy lets an upload session omit privacy.
func TestInitPhotoMediaUploadSkipsPrivacy(t *testing.T) {
	h := newHarness(t)
	req := validPhotoRequest()
	req.PostMode = "MEDIA_UPLOAD"
	req.PostInfo.PrivacyLevel = ""
	if _, err := h.svc.InitPhoto(context.Background(), testOperator, req, false, "key-photo-upload"); err != nil {
		t.Fatalf("InitPhoto() error = %v, want nil", err)
	}
	if len(h.poster.photoCalls) != 1 {
		t.Fatalf("InitPhotoPost calls = %d, want 1", len(h.poster.photoCalls))
	}
}

// TestInitDraftHappyPath routes a draft with no privacy info to the draft endpoint.
func TestInitDraftHappyPath(t *testing.T) {
	h := newHarness(t)
	res, err := h.svc.InitDraft(context.Background(), testOperator, InitDraftRequest{
		AccountID: testAccountID, MediaID: testMediaID, Source: sourceFileUpload,
	}, "key-draft-happy")
	if err != nil {
		t.Fatalf("InitDraft() error = %v", err)
	}
	if res.Status != StatusSendToUserInbox {
		t.Fatalf("Status = %q, want SEND_TO_USER_INBOX", res.Status)
	}
	if len(h.poster.draftCalls) != 1 {
		t.Fatalf("InitDraftUpload calls = %d, want 1", len(h.poster.draftCalls))
	}
	if len(h.attempts.inserted) != 1 || h.attempts.inserted[0].Status != StatusSendToUserInbox {
		t.Fatalf("stored attempt = %+v, want SEND_TO_USER_INBOX", h.attempts.inserted)
	}
}

// TestInitDraftUnverifiedHostRejected rejects a pull URL outside the media host.
func TestInitDraftUnverifiedHostRejected(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.InitDraft(context.Background(), testOperator, InitDraftRequest{
		AccountID: testAccountID, MediaID: testMediaID,
		Source: sourcePullFromURL, MediaURL: "https://evil.example/clip.mp4",
	}, "key-draft-evil")
	if errorCode(err) != string(apierror.CodeTikTokURLNotVerified) {
		t.Fatalf("code = %q, want TIKTOK_URL_NOT_VERIFIED", errorCode(err))
	}
	if len(h.poster.draftCalls) != 0 {
		t.Fatalf("InitDraftUpload calls = %d, want 0", len(h.poster.draftCalls))
	}
}
