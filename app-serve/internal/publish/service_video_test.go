// Package publish tables the happy path and rejection cases for init video.
//
// @file      internal/publish/service_video_test.go
// @for       Table tests for the video init validation order and replay.
// @uses      context, strings, testing, internal/apierror
// @reason    Pins the Review Focus cases behind fakes instead of TikTok network.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package publish

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// TestInitVideoHappyPath returns the 202 shape without the upstream upload URL.
func TestInitVideoHappyPath(t *testing.T) {
	h := newHarness(t)
	res, err := h.svc.InitVideo(context.Background(), testOperator, validVideoRequest(), true, "key-happy")
	if err != nil {
		t.Fatalf("InitVideo() error = %v", err)
	}
	if res.PublishID != testPublishID {
		t.Fatalf("PublishID = %q, want %q", res.PublishID, testPublishID)
	}
	if res.Status != StatusCreated {
		t.Fatalf("Status = %q, want CREATED", res.Status)
	}
	if res.ID == "" {
		t.Fatal("ID is empty")
	}
	if res.UploadPlan.Source != sourceFileUpload {
		t.Fatalf("UploadPlan.Source = %q, want FILE_UPLOAD", res.UploadPlan.Source)
	}
	if len(h.poster.videoCalls) != 1 {
		t.Fatalf("InitVideoPost calls = %d, want 1", len(h.poster.videoCalls))
	}
	if !h.poster.videoCalls[0].PostInfo.IsAIGC {
		t.Fatal("IsAIGC = false, want true for generated media")
	}
	if len(h.attempts.inserted) != 1 {
		t.Fatalf("Insert calls = %d, want 1", len(h.attempts.inserted))
	}
}

// TestInitVideoRejections tables the guards that must fire before TikTok.
func TestInitVideoRejections(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*InitVideoRequest)
		options []string
		want    string
	}{
		{
			name:    "privacy not in options",
			mutate:  func(r *InitVideoRequest) { r.PostInfo.PrivacyLevel = "STRANGERS" },
			options: []string{"SELF_ONLY", "FOLLOWERS", "PUBLIC"},
			want:    string(apierror.CodePublishPrivacyMismatch),
		},
		{
			name:    "title at 2201 fails",
			mutate:  func(r *InitVideoRequest) { r.PostInfo.Title = strings.Repeat("a", 2201) },
			options: []string{"SELF_ONLY", "FOLLOWERS", "PUBLIC"},
			want:    string(apierror.CodeValidationFailed),
		},
		{
			name: "branded with self only",
			mutate: func(r *InitVideoRequest) {
				r.PostInfo.PrivacyLevel = "SELF_ONLY"
				r.PostInfo.BrandContentToggle = true
			},
			options: []string{"SELF_ONLY", "FOLLOWERS", "PUBLIC"},
			want:    string(apierror.CodeValidationFailed),
		},
		{
			name: "pull from unverified host",
			mutate: func(r *InitVideoRequest) {
				r.Source = sourcePullFromURL
				r.MediaURL = "https://evil.example/clip.mp4"
			},
			options: []string{"SELF_ONLY", "FOLLOWERS", "PUBLIC"},
			want:    string(apierror.CodeTikTokURLNotVerified),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.svc.creators = &fakeCreators{options: tc.options}
			req := validVideoRequest()
			tc.mutate(&req)
			_, err := h.svc.InitVideo(context.Background(), testOperator, req, false, "key-"+tc.name)
			if err == nil {
				t.Fatal("InitVideo() error = nil, want rejection")
			}
			if got := errorCode(err); got != tc.want {
				t.Fatalf("code = %q, want %q (err %v)", got, tc.want, err)
			}
			if len(h.poster.videoCalls) != 0 {
				t.Fatalf("InitVideoPost calls = %d, want 0", len(h.poster.videoCalls))
			}
		})
	}
}

// TestInitVideoTitleBoundary2200Passes keeps the 2200 cap inclusive.
func TestInitVideoTitleBoundary2200Passes(t *testing.T) {
	h := newHarness(t)
	req := validVideoRequest()
	req.PostInfo.Title = strings.Repeat("a", 2200)
	if _, err := h.svc.InitVideo(context.Background(), testOperator, req, false, "key-bound"); err != nil {
		t.Fatalf("InitVideo() error = %v, want nil", err)
	}
}

// TestInitVideoGeneratedOverridesClientFlags pins is_aigc to the server value.
func TestInitVideoGeneratedOverridesClientFlags(t *testing.T) {
	for _, generated := range []bool{true, false} {
		h := newHarness(t)
		req := validVideoRequest()
		if _, err := h.svc.InitVideo(context.Background(), testOperator, req, generated, "key-aigc"); err != nil {
			t.Fatalf("InitVideo() error = %v", err)
		}
		if got := h.poster.videoCalls[0].PostInfo.IsAIGC; got != generated {
			t.Fatalf("IsAIGC = %v, want %v", got, generated)
		}
	}
}

// TestInitVideoDuplicateKeyChangedBody returns 409 without a second upstream call.
func TestInitVideoDuplicateKeyChangedBody(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.InitVideo(context.Background(), testOperator, validVideoRequest(), false, "key-dup"); err != nil {
		t.Fatalf("first InitVideo() error = %v", err)
	}
	changed := validVideoRequest()
	changed.PostInfo.Title = "different"
	_, err := h.svc.InitVideo(context.Background(), testOperator, changed, false, "key-dup")
	if err == nil {
		t.Fatal("second InitVideo() error = nil, want 409")
	}
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) || apiErr.Code != apierror.CodeIdempotencyKeyReused {
		t.Fatalf("code = %v, want IDEMPOTENCY_KEY_REUSED", err)
	}
	if apiErr.Status != 409 {
		t.Fatalf("status = %d, want 409", apiErr.Status)
	}
	if len(h.poster.videoCalls) != 1 {
		t.Fatalf("InitVideoPost calls = %d, want 1", len(h.poster.videoCalls))
	}
}

// TestInitVideoSameKeySameBodyReplays returns the stored response.
func TestInitVideoSameKeySameBodyReplays(t *testing.T) {
	h := newHarness(t)
	first, err := h.svc.InitVideo(context.Background(), testOperator, validVideoRequest(), false, "key-replay")
	if err != nil {
		t.Fatalf("first InitVideo() error = %v", err)
	}
	second, err := h.svc.InitVideo(context.Background(), testOperator, validVideoRequest(), false, "key-replay")
	if err != nil {
		t.Fatalf("second InitVideo() error = %v", err)
	}
	if second != first {
		t.Fatalf("replay = %+v, want %+v", second, first)
	}
	if len(h.poster.videoCalls) != 1 {
		t.Fatalf("InitVideoPost calls = %d, want 1", len(h.poster.videoCalls))
	}
}

// TestInitVideoSchemaValidationFailsBeforeLogic rejects a bad payload early.
func TestInitVideoSchemaValidationFailsBeforeLogic(t *testing.T) {
	h := newHarness(t)
	req := validVideoRequest()
	req.AccountID = "not-a-uuid"
	_, err := h.svc.InitVideo(context.Background(), testOperator, req, false, "key-schema")
	if errorCode(err) != string(apierror.CodeValidationFailed) {
		t.Fatalf("code = %q, want VALIDATION_FAILED", errorCode(err))
	}
	if len(h.poster.videoCalls) != 0 {
		t.Fatalf("InitVideoPost calls = %d, want 0", len(h.poster.videoCalls))
	}
}
