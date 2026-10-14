// Package media serves upload, lookup, and delete endpoints over one Service.
//
// @file      internal/media/show_delete_test.go
// @for       Coverage for the media read and delete handlers.
// @uses      context, net/http, httptest, testing, gin
// @reason    Locks the 302, 200, 404, and 204 outcomes clients depend on.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package media

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShowHandlerRedirectsToSignedURL(t *testing.T) {
	store := newFakeStore()
	store.objects["obj-1"] = MediaObject{ID: "obj-1", Kind: MediaKindVideo}
	store.signedURL = "https://cdn.example.test/obj-1?sig=abc"
	router := testRouter(t, store, "acct-1", 0)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/media/obj-1", nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != store.signedURL {
		t.Errorf("location = %q, want %q", got, store.signedURL)
	}
}

func TestShowHandlerReturnsMetadataWithoutSignedURL(t *testing.T) {
	store := newFakeStore()
	store.objects["obj-1"] = MediaObject{ID: "obj-1", Kind: MediaKindPhoto, ByteSize: 12}
	router := testRouter(t, store, "acct-1", 0)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/media/obj-1", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"data"`
		Meta struct {
			ServerTime string `json:"server_time"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if body.Data.ID != "obj-1" || body.Data.Kind != "photo" {
		t.Errorf("data = %+v, want obj-1/photo", body.Data)
	}
	if body.Meta.ServerTime == "" {
		t.Error("meta.server_time is empty")
	}
}

func TestShowHandlerMissingObjectIsNotFound(t *testing.T) {
	router := testRouter(t, newFakeStore(), "acct-1", 0)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/media/absent", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if code := bodyCode(t, rec); code != "NOT_FOUND" {
		t.Errorf("code = %q, want NOT_FOUND", code)
	}
}

func TestDeleteHandlerRemovesObject(t *testing.T) {
	store := newFakeStore()
	store.objects["obj-1"] = MediaObject{ID: "obj-1"}
	router := testRouter(t, store, "acct-1", 0)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/media/obj-1", nil))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if store.deleteCalls != 1 {
		t.Errorf("delete calls = %d, want 1", store.deleteCalls)
	}
	if len(store.deletedIDs) != 1 || store.deletedIDs[0] != "obj-1" {
		t.Errorf("deleted ids = %v, want [obj-1]", store.deletedIDs)
	}
}
