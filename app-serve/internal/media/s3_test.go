// Package media stores uploaded object bytes behind one interface.
//
// @file      internal/media/s3_test.go
// @for       Object store round trip, key mapping, and miss mapping tests.
// @uses      bytes, context, crypto/sha256, encoding/hex, io, strings, testing
// @reason    Locks the S3 adapter to the Store contract without a real bucket.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-16
package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
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

func (f *fakeObjectClient) PutObject(_ context.Context, bucket, key string, body io.Reader, _ int64, _ string) error {
	f.buckets = append(f.buckets, bucket)
	f.puts = append(f.puts, key)
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	f.objects[key] = data
	return nil
}

func (f *fakeObjectClient) GetObject(_ context.Context, bucket, key string) (io.ReadCloser, error) {
	f.buckets = append(f.buckets, bucket)
	body, ok := f.objects[key]
	if !ok {
		return nil, errObjectMissing
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (f *fakeObjectClient) DeleteObject(_ context.Context, bucket, key string) error {
	f.buckets = append(f.buckets, bucket)
	f.deletes = append(f.deletes, key)
	delete(f.objects, key)
	return nil
}

func TestS3RoundTripUsesBucketAndKey(t *testing.T) {
	client := newFakeObjectClient()
	store := NewS3(client, "media-bucket", "https://cdn.example.test")
	ctx := context.Background()
	payload := []byte("remote bytes")
	obj := MediaObject{ID: "obj-9", ByteSize: int64(len(payload)), ChecksumSHA256: checksumHex(payload)}

	if err := store.Put(ctx, obj, bytes.NewReader(payload)); err != nil {
		t.Fatalf("put: %v", err)
	}
	if len(client.puts) != 2 || client.puts[0] != "obj-9" || client.puts[1] != "obj-9"+sidecarSuffix {
		t.Errorf("puts = %v, want [obj-9 obj-9.json]", client.puts)
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
	if len(client.deletes) != 2 || client.deletes[0] != "obj-9" || client.deletes[1] != "obj-9"+sidecarSuffix {
		t.Errorf("deletes = %v, want [obj-9 obj-9.json]", client.deletes)
	}
}

func TestS3PutRejectsIncompleteMetadata(t *testing.T) {
	store := NewS3(newFakeObjectClient(), "media-bucket", "")

	err := store.Put(context.Background(), MediaObject{ID: "obj-1"}, strings.NewReader("x"))

	if err == nil {
		t.Fatal("expected an error for an object with no size or checksum")
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

// TestS3TaggedMissMapsToNotFound proves a client error implementing the
// NotFound marker maps to NOT_FOUND even when its text says nothing useful.
func TestS3TaggedMissMapsToNotFound(t *testing.T) {
	store := NewS3(missMarkerClient{}, "media-bucket", "")

	_, _, err := store.Get(context.Background(), "obj-1")

	if !isNotFound(err) {
		t.Fatalf("err = %v, want NOT_FOUND from the NotFound marker", err)
	}
}

// missMarkerClient is an ObjectClient whose Get reports a tagged miss.
type missMarkerClient struct{}

func (missMarkerClient) PutObject(_ context.Context, _, _ string, _ io.Reader, _ int64, _ string) error {
	return nil
}

func (missMarkerClient) GetObject(_ context.Context, _, _ string) (io.ReadCloser, error) {
	return nil, markerMiss{}
}

func (missMarkerClient) DeleteObject(_ context.Context, _, _ string) error { return nil }

// markerMiss carries no useful text, so only the marker can map it.
type markerMiss struct{}

func (markerMiss) Error() string  { return "backend refused" }
func (markerMiss) NotFound() bool { return true }

// checksumHex is the file byte digest the S3 adapter uses for its sidecar and
// the service records for its object.
func checksumHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
