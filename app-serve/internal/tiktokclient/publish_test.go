// Package tiktokclient_test pins the publish family wire behavior.
//
// @file      internal/tiktokclient/publish_test.go
// @for       Table tests for creator info, init, chunk upload, and status calls.
// @uses      testing, encoding/json, reflect, context, internal/apierror
// @reason    Proves publish request shapes, chunk headers, and error mapping.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package tiktokclient

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// creatorInfoBody is a full creator info success payload.
const creatorInfoBody = `{"creator_username":"dodi","creator_nickname":"Dodi","creator_avatar_url":"https://cdn.test/a.png",` +
	`"privacy_level_options":["PUBLIC_TO_EVERYONE","SELF_ONLY"],"comment_disabled":true,"duet_disabled":false,` +
	`"stitch_disabled":true,"max_video_post_duration_sec":600}`

// wantCreatorInfo is the expected decode of creatorInfoBody.
func wantCreatorInfo() CreatorInfo {
	return CreatorInfo{
		CreatorUsername: "dodi", CreatorNickname: "Dodi", CreatorAvatarURL: "https://cdn.test/a.png",
		PrivacyLevelOptions: []string{"PUBLIC_TO_EVERYONE", "SELF_ONLY"},
		CommentDisabled:     true, DuetDisabled: false, StitchDisabled: true, MaxVideoPostDurationSec: 600,
	}
}

// wantVideoInit is a representative video init request.
func wantVideoInit() VideoInitRequest {
	return VideoInitRequest{
		PostInfo:   PostInfo{Title: "hello", PrivacyLevel: "SELF_ONLY", DisableComment: true, IsAIGC: true},
		SourceInfo: SourceInfo{Source: "FILE_UPLOAD", VideoSize: 1024, ChunkSize: 512, TotalChunkCount: 2},
	}
}

func TestQueryCreatorInfo(t *testing.T) {
	tests := []struct {
		name      string
		responses []fakeResponse
		wantErr   apierror.Code
	}{
		{name: "happy path maps every field", responses: []fakeResponse{successEnvelope(creatorInfoBody)}},
		{name: "domain error maps to its code", responses: []fakeResponse{wireErrorResponse(400, "invalid_param", "bad", "")}, wantErr: apierror.CodeTikTokInvalidParam},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeTikTok(t)
			f.queue(creatorInfoPath, tc.responses...)
			c := newTestClient(t, f)
			got, err := c.QueryCreatorInfo(context.Background(), "act-1")
			if tc.wantErr != "" {
				wantError(t, err, tc.wantErr, "")
			} else {
				if err != nil {
					t.Fatalf("QueryCreatorInfo: %v", err)
				}
				if want := wantCreatorInfo(); !reflect.DeepEqual(got, want) {
					t.Fatalf("creator = %+v, want %+v", got, want)
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

// decodeKeys splits a request body into its top-level raw members.
func decodeKeys(t *testing.T, body string) map[string]json.RawMessage {
	t.Helper()
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &keys); err != nil {
		t.Fatalf("decode body keys: %v (body %s)", err, body)
	}
	return keys
}

func TestInitVideoPost(t *testing.T) {
	tests := []struct {
		name      string
		responses []fakeResponse
		wantErr   apierror.Code
	}{
		{name: "happy path returns publish id and upload url", responses: []fakeResponse{successEnvelope(`{"publish_id":"v-1","upload_url":"https://upload.test/v"}`)}},
		{name: "domain error maps to its code", responses: []fakeResponse{wireErrorResponse(400, "privacy_level_option_mismatch", "no", "")}, wantErr: apierror.CodePublishPrivacyMismatch},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeTikTok(t)
			f.queue(videoInitPath, tc.responses...)
			c := newTestClient(t, f)
			got, err := c.InitVideoPost(context.Background(), "act-1", wantVideoInit())
			if tc.wantErr != "" {
				wantError(t, err, tc.wantErr, "")
			} else {
				if err != nil {
					t.Fatalf("InitVideoPost: %v", err)
				}
				want := VideoInitResponse{PublishID: "v-1", UploadURL: "https://upload.test/v"}
				if got != want {
					t.Fatalf("init = %+v, want %+v", got, want)
				}
			}
			keys := decodeKeys(t, f.lastRequest(t).body)
			if _, ok := keys["post_info"]; !ok {
				t.Fatal("body missing post_info")
			}
			if _, ok := keys["source_info"]; !ok {
				t.Fatal("body missing source_info")
			}
			var post PostInfo
			if err := json.Unmarshal(keys["post_info"], &post); err != nil {
				t.Fatalf("unmarshal post_info: %v", err)
			}
			if post.Title != "hello" || post.PrivacyLevel != "SELF_ONLY" || !post.DisableComment || !post.IsAIGC {
				t.Fatalf("post_info = %+v", post)
			}
			if auth := f.lastRequest(t).headers.Get("Authorization"); auth != "Bearer act-1" {
				t.Fatalf("Authorization = %q, want Bearer act-1", auth)
			}
			wantHits(t, f, 1)
		})
	}
}

func TestInitDraftUpload(t *testing.T) {
	f := newFakeTikTok(t)
	f.handle(draftInitPath, successEnvelope(`{"publish_id":"d-1","upload_url":"https://upload.test/d"}`))
	c := newTestClient(t, f)
	got, err := c.InitDraftUpload(context.Background(), "act-1", DraftInitRequest{SourceInfo: SourceInfo{Source: "FILE_UPLOAD", VideoSize: 10, ChunkSize: 10, TotalChunkCount: 1}})
	if err != nil {
		t.Fatalf("InitDraftUpload: %v", err)
	}
	if got != (DraftInitResponse{PublishID: "d-1", UploadURL: "https://upload.test/d"}) {
		t.Fatalf("init = %+v", got)
	}
	keys := decodeKeys(t, f.lastRequest(t).body)
	if _, ok := keys["post_info"]; ok {
		t.Fatal("draft body must not carry post_info")
	}
	if _, ok := keys["source_info"]; !ok {
		t.Fatal("draft body missing source_info")
	}
}

func TestFetchStatus(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		want     PublishStatus
		wantHits int64
	}{
		{
			name:     "happy path maps public ids and counters",
			body:     `{"status":"PUBLISH_COMPLETE","publicaly_available_post_id":["123","456"],"uploaded_bytes":100,"downloaded_bytes":7}`,
			want:     PublishStatus{Status: "PUBLISH_COMPLETE", PublicalyAvailablePostID: []string{"123", "456"}, UploadedBytes: 100, DownloadedBytes: 7},
			wantHits: 1,
		},
		{
			name:     "fail reason maps",
			body:     `{"status":"FAILED","fail_reason":"spam"}`,
			want:     PublishStatus{Status: "FAILED", FailReason: "spam"},
			wantHits: 1,
		},
		{
			name:     "429 then success retries once",
			body:     `{"status":"PUBLISH_COMPLETE"}`,
			want:     PublishStatus{Status: "PUBLISH_COMPLETE"},
			wantHits: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeTikTok(t)
			if tc.wantHits > 1 {
				f.queue(statusPath, wireErrorResponse(http.StatusTooManyRequests, "rate_limit_exceeded", "slow down", ""), successEnvelope(tc.body))
			} else {
				f.handle(statusPath, successEnvelope(tc.body))
			}
			c := newTestClient(t, f)
			got, err := c.FetchStatus(context.Background(), "act-1", "v-1")
			if err != nil {
				t.Fatalf("FetchStatus: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("status = %+v, want %+v", got, tc.want)
			}
			var sent map[string]json.RawMessage
			if err := json.Unmarshal([]byte(f.lastRequest(t).body), &sent); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if string(sent["publish_id"]) != `"v-1"` {
				t.Fatalf("publish_id = %s", sent["publish_id"])
			}
			wantHits(t, f, tc.wantHits)
		})
	}
}
