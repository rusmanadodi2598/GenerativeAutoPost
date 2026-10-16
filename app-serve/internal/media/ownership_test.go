// Package media enforces per-account ownership on the read and delete paths.
//
// @file      internal/media/ownership_test.go
// @for       Foreign-account 404 coverage for show and delete plus the id table test.
// @uses      context, io, net/http, httptest, strings, testing
// @reason    Locks that one account cannot read or delete another's media.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-14
package media

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

func TestForeignAccountShowIsNotFound(t *testing.T) {
	store := newFakeStore()
	store.objects["obj-1"] = MediaObject{ID: "obj-1", OwnerID: "acct-1", Kind: MediaKindVideo}
	router := testRouter(t, store, "acct-2", 0)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/media/obj-1", nil))

	assertStatus(t, rec, http.StatusNotFound)
	assertCode(t, rec, "NOT_FOUND")
}

func TestForeignAccountDeleteIsNotFoundAndKeepsObject(t *testing.T) {
	store := newFakeStore()
	store.objects["obj-1"] = MediaObject{ID: "obj-1", OwnerID: "acct-1", Kind: MediaKindVideo}
	router := testRouter(t, store, "acct-2", 0)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/media/obj-1", nil))

	assertStatus(t, rec, http.StatusNotFound)
	assertCode(t, rec, "NOT_FOUND")
	if store.deleteCalls != 0 {
		t.Errorf("deleteCalls = %d, want 0", store.deleteCalls)
	}
	if _, ok := store.objects["obj-1"]; !ok {
		t.Error("owner object was removed by a foreign account")
	}

	owner := testRouter(t, store, "acct-1", 0)
	rec = httptest.NewRecorder()
	owner.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/media/obj-1", nil))
	if rec.Code != http.StatusNoContent {
		t.Errorf("owner delete status = %d, want 204", rec.Code)
	}
}

func TestValidIDTable(t *testing.T) {
	cases := []struct {
		id    string
		valid bool
	}{
		{"a?b", false}, {"a#b", false}, {"a b", false}, {"a\nb", false},
		{"..", false}, {".", false}, {"a/./b", false}, {"a/b/..", false},
		{"a/b", false}, {`a\b`, false}, {"", false}, {"a.", true}, {".hidden", true},
		{"550e8400-e29b-41d4-a716-446655440000", true},
		{"obj-1_ABC.xyz", true}, {"abc123", true},
	}
	for _, tc := range cases {
		if got := validID(tc.id); got != tc.valid {
			t.Errorf("validID(%q) = %v, want %v", tc.id, got, tc.valid)
		}
	}
}

func TestServiceOwnedGetDeleteDenyForeign(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	obj, err := svc.Upload(ctx, UploadRequest{AccountID: "acct-1", Kind: MediaKindVideo,
		ContentType: "video/mp4", Size: 4, Body: strings.NewReader("data")})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if obj.OwnerID != "acct-1" {
		t.Errorf("owner = %q, want acct-1", obj.OwnerID)
	}

	if _, _, err := svc.Get(ctx, "acct-2", obj.ID); !isNotFound(err) {
		t.Errorf("foreign get err = %v, want NOT_FOUND", err)
	}
	if err := svc.Delete(ctx, "acct-2", obj.ID); !isNotFound(err) {
		t.Errorf("foreign delete err = %v, want NOT_FOUND", err)
	}
	if _, _, err := svc.Get(ctx, "acct-1", obj.ID); err != nil {
		t.Errorf("owner get err = %v, want nil", err)
	}
}

func TestS3GetMapsOnlyMissingToNotFound(t *testing.T) {
	client := &failingObjectClient{getErr: errors.New("connection reset")}
	store := NewS3(client, "media-bucket", "")

	_, _, err := store.Get(context.Background(), "obj-1")

	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) || apiErr.Code != apierror.CodeMediaNotStored {
		t.Errorf("err = %v, want MEDIA_NOT_STORED", err)
	}
}

type failingObjectClient struct {
	getErr error
}

func (f *failingObjectClient) PutObject(_ context.Context, _, _ string, _ io.Reader, _ int64, _ string) error {
	return nil
}

func (f *failingObjectClient) GetObject(_ context.Context, _, _ string) (io.ReadCloser, error) {
	return nil, f.getErr
}

func (f *failingObjectClient) DeleteObject(_ context.Context, _, _ string) error { return nil }

func TestDiskGetReturnsStoredOwner(t *testing.T) {
	dir := t.TempDir()
	store := NewDisk(dir)
	ctx := context.Background()
	obj := MediaObject{ID: "obj-1", OwnerID: "acct-1", Kind: MediaKindVideo, ContentType: "video/mp4"}

	if err := store.Put(ctx, obj, strings.NewReader("bytes")); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, body, err := store.Get(ctx, "obj-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = body.Close()
	if got.OwnerID != "acct-1" {
		t.Errorf("owner = %q, want acct-1", got.OwnerID)
	}
}

func isNotFound(err error) bool {
	var apiErr *apierror.Error
	return errors.As(err, &apiErr) && apiErr.Code == apierror.CodeNotFound
}
