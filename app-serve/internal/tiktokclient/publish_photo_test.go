// Package tiktokclient_test pins the photo post wire shape.
//
// @file      internal/tiktokclient/publish_photo_test.go
// @for       Table-free assertions that PhotoPostInfo marshals flat into post_info.
// @uses      testing, encoding/json, context
// @reason    Keeps the embedded PostInfo flat wire check next to its own fixture.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package tiktokclient

import (
	"context"
	"encoding/json"
	"testing"
)

func TestInitPhotoPost(t *testing.T) {
	f := newFakeTikTok(t)
	f.handle(photoInitPath, successEnvelope(`{"publish_id":"p-1"}`))
	c := newTestClient(t, f)
	req := PhotoInitRequest{
		PostInfo:    PhotoPostInfo{PostInfo: PostInfo{Title: "t", PrivacyLevel: "SELF_ONLY"}, Description: "d", AutoAddMusic: true},
		SourceInfo:  SourceInfo{Source: "FILE_UPLOAD"},
		PostMode:    "DIRECT_POST",
		MediaType:   "PHOTO",
		PhotoImages: []string{"https://cdn.test/1.jpg", "https://cdn.test/2.jpg"},
	}
	got, err := c.InitPhotoPost(context.Background(), "act-1", req)
	if err != nil {
		t.Fatalf("InitPhotoPost: %v", err)
	}
	if got != (PhotoInitResponse{PublishID: "p-1"}) {
		t.Fatalf("init = %+v", got)
	}
	keys := decodeKeys(t, f.lastRequest(t).body)
	if _, ok := keys["post_info"]; !ok {
		t.Fatal("body missing post_info")
	}
	if _, ok := keys["source_info"]; !ok {
		t.Fatal("body missing source_info")
	}
	post := decodeKeys(t, string(keys["post_info"]))
	for _, field := range []string{"title", "privacy_level", "description", "auto_add_music"} {
		if _, ok := post[field]; !ok {
			t.Fatalf("post_info missing flat key %q: %s", field, keys["post_info"])
		}
	}
	if _, ok := post["PostInfo"]; ok {
		t.Fatalf("post_info key nesting leaked into the wire body: %s", keys["post_info"])
	}
	if title := string(post["title"]); title != `"t"` {
		t.Fatalf("title = %s", title)
	}
	if desc := string(post["description"]); desc != `"d"` {
		t.Fatalf("description = %s", desc)
	}
	if music := string(post["auto_add_music"]); music != "true" {
		t.Fatalf("auto_add_music = %s", music)
	}
	if mode := string(keys["post_mode"]); mode != `"DIRECT_POST"` {
		t.Fatalf("post_mode = %s", mode)
	}
	if media := string(keys["media_type"]); media != `"PHOTO"` {
		t.Fatalf("media_type = %s", media)
	}
	var images []string
	if err := json.Unmarshal(keys["photo_images"], &images); err != nil {
		t.Fatalf("unmarshal photo_images: %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("photo_images = %v", images)
	}
	wantHits(t, f, 1)
}
