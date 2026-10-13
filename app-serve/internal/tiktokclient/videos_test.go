// Package tiktokclient_test pins the video read wire behavior.
//
// @file      internal/tiktokclient/videos_test.go
// @for       Table tests for video list and video query calls.
// @uses      testing, encoding/json, context, internal/apierror
// @reason    Proves page size clamps, fields encoding, and the id cap guard.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package tiktokclient

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// videoBody is a video list payload with one decorated video.
const videoBody = `{"videos":[{"id":"v-1","create_time":1710000000,"cover_image_url":"https://cdn.test/c.jpg",` +
	`"share_url":"https://tiktok.test/v/1","video_description":"hello","duration":15,"height":1920,"width":1080,` +
	`"title":"t","embed_html":"<blockquote>","embed_link":"https://tiktok.test/e/1",` +
	`"like_count":11,"comment_count":2,"share_count":3,"view_count":500,"is_aigc":true}],"cursor":7,"has_more":true}`

func TestListVideos(t *testing.T) {
	tests := []struct {
		name      string
		responses []fakeResponse
		wantErr   apierror.Code
	}{
		{name: "happy path maps the page", responses: []fakeResponse{successEnvelope(videoBody)}},
		{name: "domain error maps to its code", responses: []fakeResponse{wireErrorResponse(400, "invalid_param", "bad", "")}, wantErr: apierror.CodeTikTokInvalidParam},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeTikTok(t)
			f.queue(videoListPath, tc.responses...)
			c := newTestClient(t, f)
			got, err := c.ListVideos(context.Background(), "act-1", "cursor-0", 5)
			if tc.wantErr != "" {
				wantError(t, err, tc.wantErr, "")
			} else {
				if err != nil {
					t.Fatalf("ListVideos: %v", err)
				}
				if len(got.Videos) != 1 || got.Videos[0].ID != "v-1" || got.Videos[0].LikeCount != 11 {
					t.Fatalf("videos = %+v", got.Videos)
				}
				if got.Cursor != 7 || !got.HasMore {
					t.Fatalf("page = cursor %d has_more %v", got.Cursor, got.HasMore)
				}
			}
			req := f.lastRequest(t)
			if req.method != http.MethodPost {
				t.Fatalf("method = %s, want POST", req.method)
			}
			if auth := req.headers.Get("Authorization"); auth != "Bearer act-1" {
				t.Fatalf("Authorization = %q, want Bearer act-1", auth)
			}
			wantHits(t, f, 1)
		})
	}
}

func TestListVideosClampsMaxCount(t *testing.T) {
	tests := []struct {
		name     string
		maxCount int
		want     string
	}{
		{name: "zero falls back to the default", maxCount: 0, want: "10"},
		{name: "above the cap clamps to twenty", maxCount: 25, want: "20"},
		{name: "inside the range is kept", maxCount: 5, want: "5"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeTikTok(t)
			f.handle(videoListPath, successEnvelope(`{"videos":[]}`))
			c := newTestClient(t, f)
			if _, err := c.ListVideos(context.Background(), "act-1", "", tc.maxCount); err != nil {
				t.Fatalf("ListVideos: %v", err)
			}
			keys := decodeKeys(t, f.lastRequest(t).body)
			if got := string(keys["max_count"]); got != tc.want {
				t.Fatalf("max_count = %s, want %s", got, tc.want)
			}
			wantHits(t, f, 1)
		})
	}
}

func TestQueryVideos(t *testing.T) {
	tests := []struct {
		name      string
		responses []fakeResponse
		wantErr   apierror.Code
	}{
		{name: "happy path returns the matching videos", responses: []fakeResponse{successEnvelope(`{"videos":[{"id":"v-1"},{"id":"v-2"}]}`)}},
		{name: "domain error maps to its code", responses: []fakeResponse{wireErrorResponse(400, "invalid_param", "bad", "")}, wantErr: apierror.CodeTikTokInvalidParam},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeTikTok(t)
			f.queue(videoQueryPath, tc.responses...)
			c := newTestClient(t, f)
			got, err := c.QueryVideos(context.Background(), "act-1", []string{"v-1", "v-2"}, []string{"id", "like_count", "share_url"})
			if tc.wantErr != "" {
				wantError(t, err, tc.wantErr, "")
			} else {
				if err != nil {
					t.Fatalf("QueryVideos: %v", err)
				}
				if len(got) != 2 || got[0].ID != "v-1" || got[1].ID != "v-2" {
					t.Fatalf("videos = %+v", got)
				}
			}
			req := f.lastRequest(t)
			if fields := req.query.Get("fields"); fields != "id,like_count,share_url" {
				t.Fatalf("fields = %q, want id,like_count,share_url", fields)
			}
			var ids []string
			if err := json.Unmarshal(decodeKeys(t, req.body)["video_ids"], &ids); err != nil {
				t.Fatalf("decode video_ids: %v", err)
			}
			if len(ids) != 2 || ids[0] != "v-1" {
				t.Fatalf("video_ids = %v", ids)
			}
			if auth := req.headers.Get("Authorization"); auth != "Bearer act-1" {
				t.Fatalf("Authorization = %q, want Bearer act-1", auth)
			}
			wantHits(t, f, 1)
		})
	}
}

// TestQueryVideosRejectsTooManyIDs proves the cap runs before any HTTP call.
func TestQueryVideosRejectsTooManyIDs(t *testing.T) {
	f := newFakeTikTok(t)
	c := newTestClient(t, f)
	ids := make([]string, 21)
	for i := range ids {
		ids[i] = "v-" + string(rune('a'+i%26))
	}
	_, err := c.QueryVideos(context.Background(), "act-1", ids, []string{"id"})
	wantError(t, err, apierror.CodeTikTokInvalidParam, "")
	wantHits(t, f, 0)
}

// TestVideoCountsOmitted proves a missing like_count stays absent after decode,
// because the omitempty tag keeps a gap from becoming a hard zero on the wire.
func TestVideoCountsOmitted(t *testing.T) {
	f := newFakeTikTok(t)
	f.handle(videoQueryPath, successEnvelope(`{"videos":[{"id":"v-1"}]}`))
	c := newTestClient(t, f)
	got, err := c.QueryVideos(context.Background(), "act-1", []string{"v-1"}, []string{"id", "like_count"})
	if err != nil {
		t.Fatalf("QueryVideos: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("videos = %+v", got)
	}
	raw, err := json.Marshal(got[0])
	if err != nil {
		t.Fatalf("marshal video: %v", err)
	}
	if strings.Contains(string(raw), "like_count") {
		t.Fatalf("re-marshalled video kept like_count: %s", raw)
	}
	wantHits(t, f, 1)
}
