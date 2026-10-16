// Package media stores uploaded object bytes behind one interface.
//
// @file      internal/media/store_test.go
// @for       Round trip and error coverage for the disk store.
// @uses      bytes, context, io, os, path/filepath, strings, testing
// @reason    Locks the disk store behavior so the service can trust the backend.
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
