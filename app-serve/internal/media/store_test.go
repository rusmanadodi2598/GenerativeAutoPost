// Package media stores uploaded object bytes behind one interface.
//
// @file      internal/media/store_test.go
// @for       Round trip and error coverage for the disk and object stores.
// @uses      context, io, testing
// @reason    Locks store behavior so the service can trust either backend.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package media

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeObjectClient records every call so tests can assert bucket and key.
type fakeObjectClient struct {
	objects map[string][]byte
	puts    []string
	deletes []string
	buckets []string
}

func newFakeObjectClient() *fakeObjectClient {
	return &fakeObjectClient{objects: map[string][]byte{}}
}

func (f *fakeObjectClient) PutObject(_ context.Context, bucket, key string, body []byte) error {
	f.buckets = append(f.buckets, bucket)
	f.puts = append(f.puts, key)
	f.objects[key] = body
	return nil
}

func (f *fakeObjectClient) GetObject(_ context.Context, bucket, key string) ([]byte, error) {
	f.buckets = append(f.buckets, bucket)
	body, ok := f.objects[key]
	if !ok {
		return nil, errObjectMissing
	}
	return body, nil
}

func (f *fakeObjectClient) DeleteObject(_ context.Context, bucket, key string) error {
	f.buckets = append(f.buckets, bucket)
	f.deletes = append(f.deletes, key)
	delete(f.objects, key)
	return nil
}

func TestDiskPutGetDeleteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewDisk(dir)
	ctx := context.Background()
	payload := []byte("media bytes")

	if err := store.Put(ctx, MediaObject{ID: "obj-1"}, bytes.NewReader(payload)); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, body, err := store.Get(ctx, "obj-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "obj-1" {
		t.Errorf("id = %q, want obj-1", got.ID)
	}
	read, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := body.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}
	if !bytes.Equal(read, payload) {
		t.Errorf("body = %q, want %q", read, payload)
	}

	if err := store.Delete(ctx, "obj-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, err := store.Get(ctx, "obj-1"); err == nil {
		t.Error("get after delete returned no error")
	}
}

func TestDiskGetMissingObjectReturnsNotFound(t *testing.T) {
	store := NewDisk(t.TempDir())

	_, _, err := store.Get(context.Background(), "absent")

	if err == nil {
		t.Fatal("expected an error for a missing object")
	}
}

func TestDiskDeleteMissingObjectIsNotAnError(t *testing.T) {
	store := NewDisk(t.TempDir())

	if err := store.Delete(context.Background(), "absent"); err != nil {
		t.Errorf("delete missing = %v, want nil", err)
	}
}

func TestDiskPutRejectsBadRoot(t *testing.T) {
	// A regular file cannot hold the store root, so every write under it fails.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	store := NewDisk(filepath.Join(blocker, "root"))

	err := store.Put(context.Background(), MediaObject{ID: "obj-1"}, strings.NewReader("x"))

	if err == nil {
		t.Fatal("expected an error when the root cannot be created")
	}
}

func TestDiskPutRejectsIDWithPathTraversal(t *testing.T) {
	store := NewDisk(t.TempDir())

	err := store.Put(context.Background(), MediaObject{ID: "../escape"}, strings.NewReader("x"))

	if err == nil {
		t.Fatal("expected a rejection for an id containing a path separator")
	}
}

func TestDiskSignPublicURLIsEmpty(t *testing.T) {
	store := NewDisk(t.TempDir())

	got, err := store.SignPublicURL(context.Background(), "obj-1")

	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if got != "" {
		t.Errorf("url = %q, want empty: the disk store is private", got)
	}
}

func TestS3RoundTripUsesBucketAndKey(t *testing.T) {
	client := newFakeObjectClient()
	store := NewS3(client, "media-bucket", "https://cdn.example.test")
	ctx := context.Background()
	payload := []byte("remote bytes")

	if err := store.Put(ctx, MediaObject{ID: "obj-9"}, bytes.NewReader(payload)); err != nil {
		t.Fatalf("put: %v", err)
	}
	if len(client.puts) != 1 || client.puts[0] != "obj-9" {
		t.Errorf("puts = %v, want [obj-9]", client.puts)
	}
	if client.buckets[0] != "media-bucket" {
		t.Errorf("bucket = %q, want media-bucket", client.buckets[0])
	}

	_, body, err := store.Get(ctx, "obj-9")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	read, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := body.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}
	if !bytes.Equal(read, payload) {
		t.Errorf("body = %q, want %q", read, payload)
	}

	if err := store.Delete(ctx, "obj-9"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(client.deletes) != 1 || client.deletes[0] != "obj-9" {
		t.Errorf("deletes = %v, want [obj-9]", client.deletes)
	}
}

func TestS3IDMapsToSameKeyEveryTime(t *testing.T) {
	first := NewS3(newFakeObjectClient(), "media-bucket", "https://cdn.example.test")
	second := NewS3(newFakeObjectClient(), "media-bucket", "https://cdn.example.test")

	one, err := first.SignPublicURL(context.Background(), "same-id")
	if err != nil {
		t.Fatalf("sign first: %v", err)
	}
	two, err := second.SignPublicURL(context.Background(), "same-id")
	if err != nil {
		t.Fatalf("sign second: %v", err)
	}
	if one != two {
		t.Errorf("keys differ: %q vs %q", one, two)
	}
}

func TestS3SignPublicURLJoinsBaseAndID(t *testing.T) {
	store := NewS3(newFakeObjectClient(), "media-bucket", "https://cdn.example.test")

	got, err := store.SignPublicURL(context.Background(), "obj-7")

	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if want := "https://cdn.example.test/obj-7"; got != want {
		t.Errorf("url = %q, want %q", got, want)
	}
}

func TestS3SignPublicURLWithoutBaseIsEmpty(t *testing.T) {
	store := NewS3(newFakeObjectClient(), "media-bucket", "")

	got, err := store.SignPublicURL(context.Background(), "obj-7")

	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if got != "" {
		t.Errorf("url = %q, want empty", got)
	}
}

func TestS3GetMissingObjectReturnsNotFound(t *testing.T) {
	store := NewS3(newFakeObjectClient(), "media-bucket", "")

	_, _, err := store.Get(context.Background(), "absent")

	if err == nil {
		t.Fatal("expected an error for a missing key")
	}
}
