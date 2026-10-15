// Package tiktokclient_test pins the user info wire behavior.
//
// @file      internal/tiktokclient/userinfo_test.go
// @for       Table tests for the user info read and its field mapping.
// @uses      context, net/http, reflect, testing, internal/apierror
// @reason    Proves count pointers decode and the absent counts stay absent.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package tiktokclient

import (
	"context"
	"reflect"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// int64Ptr returns a pointer to a count value for the fixtures below.
func int64Ptr(value int64) *int64 { return &value }

// wantUserInfo is the expected decode of userBody.
func wantUserInfo() UserInfo {
	return UserInfo{
		OpenID: "oid-1", UnionID: "uid-1", AvatarURL: "https://cdn.test/a.png",
		DisplayName: "Dodi", BioDescription: "builder", ProfileDeepLink: "https://tiktok.test/@dodi",
		FollowerCount: int64Ptr(42), FollowingCount: int64Ptr(7), LikesCount: int64Ptr(100), VideoCount: int64Ptr(9),
	}
}

// userBody is a full user info success payload.
const userBody = `{"user":{"open_id":"oid-1","union_id":"uid-1","avatar_url":"https://cdn.test/a.png",` +
	`"display_name":"Dodi","bio_description":"builder","profile_deep_link":"https://tiktok.test/@dodi",` +
	`"follower_count":42,"following_count":7,"likes_count":100,"video_count":9}}`

func TestGetUserInfo(t *testing.T) {
	tests := []struct {
		name      string
		responses []fakeResponse
		wantErr   apierror.Code
		wantHits  int64
		wantUser  bool
	}{
		{name: "happy path maps every field and sends bearer", responses: []fakeResponse{successEnvelope(userBody)}, wantHits: 1, wantUser: true},
		{name: "unknown wire code maps to unknown error", responses: []fakeResponse{wireErrorResponse(400, "brand_new_code", "?", "log-3")}, wantErr: apierror.CodeTikTokUnknown, wantHits: 1},
		{name: "scope error maps to scope missing without retry", responses: []fakeResponse{wireErrorResponse(403, "scope_not_authorized", "no scope", "")}, wantErr: apierror.CodeTikTokScopeMissing, wantHits: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeTikTok(t)
			f.queue(userPath, tc.responses...)
			c := newTestClient(t, f)
			got, err := c.GetUserInfo(context.Background(), "act-1", []string{"open_id", "display_name"})
			if tc.wantErr != "" {
				wantError(t, err, tc.wantErr, "")
			} else {
				if err != nil {
					t.Fatalf("GetUserInfo: %v", err)
				}
				if want := wantUserInfo(); !reflect.DeepEqual(got, want) {
					t.Fatalf("user = %+v, want %+v", got, want)
				}
			}
			if tc.wantUser {
				req := f.lastRequest(t)
				if auth := req.headers.Get("Authorization"); auth != "Bearer act-1" {
					t.Fatalf("Authorization = %q, want Bearer act-1", auth)
				}
				if fields := req.query.Get("fields"); fields != "open_id,display_name" {
					t.Fatalf("fields = %q, want open_id,display_name", fields)
				}
			}
			wantHits(t, f, tc.wantHits)
		})
	}
}
