// Package insight covers the video read and stats snapshot rules.
//
// @file      internal/insight/service_test.go
// @for       Service tests for list, query, stats refresh, and profile.
// @uses      context, testing, internal/apierror, internal/tiktokclient
// @reason    Pins the cursor envelope, the 20 id cap, and the never-zero rule.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package insight

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

func TestListVideosGivenPage_ThenCursorEnvelopeCarriesPage(t *testing.T) {
	h := newServiceHarness(t)
	h.tiktok.page = tiktokclient.VideoPage{
		Videos:  []tiktokclient.Video{{ID: testVideo, Title: "hello", LikeCount: count(int64(12))}},
		Cursor:  1728742800,
		HasMore: true,
	}
	page, err := h.svc.ListVideos(context.Background(), testOperator, testAccount, "", 0)
	if err != nil {
		t.Fatalf("ListVideos returned %v", err)
	}
	if len(page.Videos) != 1 || page.Videos[0].ID != testVideo {
		t.Fatalf("videos = %+v", page.Videos)
	}
	if page.Cursor != "1728742800" || !page.HasMore {
		t.Fatalf("cursor = %q hasMore = %v", page.Cursor, page.HasMore)
	}
	if h.tiktok.listArgs[0] != "access-token" {
		t.Fatalf("access token = %q", h.tiktok.listArgs[0])
	}
	if page.Videos[0].LikeCount == nil || *page.Videos[0].LikeCount != 12 {
		t.Fatalf("like count = %v", page.Videos[0].LikeCount)
	}
}

func TestListVideosGivenAbsentCount_ThenCountIsNilNotZero(t *testing.T) {
	h := newServiceHarness(t)
	h.tiktok.page = tiktokclient.VideoPage{Videos: []tiktokclient.Video{{ID: testVideo}}}
	page, err := h.svc.ListVideos(context.Background(), testOperator, testAccount, "", 0)
	if err != nil {
		t.Fatalf("ListVideos returned %v", err)
	}
	if page.Videos[0].LikeCount != nil || page.Videos[0].ViewCount != nil {
		t.Fatalf("counts = %+v", page.Videos[0])
	}
}

func TestQueryVideosGivenMoreThan20IDs_ThenRejected(t *testing.T) {
	h := newServiceHarness(t)
	ids := make([]string, maxQueryIDs+1)
	for i := range ids {
		ids[i] = testVideo
	}
	_, err := h.svc.QueryVideos(context.Background(), testOperator, testAccount, ids)
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) || apiErr.Code != apierror.CodeValidationFailed {
		t.Fatalf("err = %v", err)
	}
	if len(h.tiktok.queryIDs) != 0 {
		t.Fatalf("upstream was called with %d ids", len(h.tiktok.queryIDs))
	}
}

func TestVideoStatsGivenNoSnapshot_ThenFetchedAndStored(t *testing.T) {
	h := newServiceHarness(t)
	h.tiktok.videos = []tiktokclient.Video{{ID: testVideo, ViewCount: count(int64(30)), LikeCount: count(int64(4))}}
	snap, err := h.svc.VideoStats(context.Background(), testOperator, testAccount, testVideo)
	if err != nil {
		t.Fatalf("VideoStats returned %v", err)
	}
	if snap.ViewCount == nil || *snap.ViewCount != 30 {
		t.Fatalf("view count = %v", snap.ViewCount)
	}
	if snap.CommentCount != nil {
		t.Fatalf("comment count = %v, want absent", *snap.CommentCount)
	}
	if snap.FetchedAt.IsZero() {
		t.Fatal("fetched_at is zero")
	}
	if h.store.writes != 1 {
		t.Fatalf("snapshot writes = %d", h.store.writes)
	}
}

func TestVideoStatsGivenFreshSnapshot_ThenUpstreamIsNotCalled(t *testing.T) {
	h := newServiceHarness(t)
	h.svc.cfg.Now = func() time.Time { return time.Date(2026, 10, 12, 9, 0, 30, 0, time.UTC) }
	h.store.stats = []StatSnapshot{{
		VideoID:   testVideo,
		ViewCount: count(int64(99)),
		FetchedAt: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC),
	}}
	snap, err := h.svc.VideoStats(context.Background(), testOperator, testAccount, testVideo)
	if err != nil {
		t.Fatalf("VideoStats returned %v", err)
	}
	if *snap.ViewCount != 99 || h.store.writes != 0 {
		t.Fatalf("snapshot = %+v writes = %d", snap, h.store.writes)
	}
}

func TestRefreshStatsGivenStaleSnapshot_ThenAppendsANewPoint(t *testing.T) {
	h := newServiceHarness(t)
	h.store.stats = []StatSnapshot{{
		VideoID:   testVideo,
		ViewCount: count(int64(10)),
		FetchedAt: time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC),
	}}
	h.tiktok.videos = []tiktokclient.Video{{ID: testVideo, ViewCount: count(int64(50))}}
	snap, err := h.svc.RefreshStats(context.Background(), testOperator, testAccount, testVideo)
	if err != nil {
		t.Fatalf("RefreshStats returned %v", err)
	}
	if *snap.ViewCount != 50 || len(h.store.stats) != 2 {
		t.Fatalf("snapshot = %+v rows = %d", snap, len(h.store.stats))
	}
}

func TestVideoStatsGivenUpstreamError_ThenErrorPropagates(t *testing.T) {
	h := newServiceHarness(t)
	h.tiktok.err = apierror.New(apierror.CodeTikTokUnavailable, 0, "TikTok is unavailable.")
	if _, err := h.svc.VideoStats(context.Background(), testOperator, testAccount, testVideo); err == nil {
		t.Fatal("expected an error")
	}
	if h.store.writes != 0 {
		t.Fatalf("snapshot writes = %d", h.store.writes)
	}
}

func TestProfileGivenCreatorInfo_ThenCountsAndPrivacyAreMapped(t *testing.T) {
	h := newServiceHarness(t)
	h.tiktok.info = tiktokclient.UserInfo{
		OpenID: "open-1", DisplayName: "Ada", FollowerCount: 120, LikesCount: 900, VideoCount: 7,
	}
	h.tiktok.creator = tiktokclient.CreatorInfo{
		CreatorUsername: "ada", PrivacyLevelOptions: []string{"PUBLIC"},
		CommentDisabled: true, MaxVideoPostDurationSec: 180,
	}
	profile, err := h.svc.Profile(context.Background(), testOperator, testAccount)
	if err != nil {
		t.Fatalf("Profile returned %v", err)
	}
	if profile.FollowerCount != 120 || profile.VideoCount != 7 {
		t.Fatalf("profile = %+v", profile)
	}
	if len(profile.PrivacyLevelOptions) != 1 || !profile.CommentDisabled {
		t.Fatalf("creator record = %+v", profile)
	}
	if profile.CreatorUsername != "ada" || profile.FetchedAt.IsZero() {
		t.Fatalf("profile = %+v", profile)
	}
	if len(h.store.creators) != 1 {
		t.Fatalf("creator snapshot rows = %d", len(h.store.creators))
	}
}

func TestProfileGivenForeignAccount_ThenOwnershipIsCheckedFirst(t *testing.T) {
	h := newServiceHarness(t)
	svc := NewService(Config{
		Videos:   h.tiktok,
		Accounts: &fakeAccounts{err: apierror.New(apierror.CodeForbidden, 0, "Account is not yours.")},
		Store:    h.store,
		Now:      func() time.Time { return time.Now().UTC() },
		StatsTTL: time.Minute,
	})
	_, err := svc.Profile(context.Background(), testOperator, testAccount)
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) || apiErr.Code != apierror.CodeForbidden {
		t.Fatalf("err = %v", err)
	}
	if len(h.store.creators) != 0 {
		t.Fatalf("creator snapshot rows = %d", len(h.store.creators))
	}
}
