// Package media locks the client declared checksum against the stored bytes.
//
// @file      internal/media/upload_checksum_test.go
// @for       Coverage for Checksum-SHA256 validation and pre-store verification.
// @uses      context, crypto/sha256, encoding/hex, strings, testing
// @reason    A wrong checksum must fail before anything reaches a store.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-16
package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"testing"
)

// checksumOf returns the hex digest a client would declare for payload.
func checksumOf(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func TestUploadGivenMatchingChecksum_ThenStoresTheObject(t *testing.T) {
	svc := newTestService(t)
	payload := "matching bytes"

	obj, err := svc.Upload(context.Background(), UploadRequest{
		AccountID: "acct-1", Kind: MediaKindVideo, ContentType: "video/mp4",
		Size: int64(len(payload)), ExpectedChecksum: checksumOf(payload),
		Body: strings.NewReader(payload),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if obj.ChecksumSHA256 != checksumOf(payload) {
		t.Errorf("checksum = %q, want %q", obj.ChecksumSHA256, checksumOf(payload))
	}
}

func TestUploadGivenWrongChecksum_ThenNothingIsStored(t *testing.T) {
	store := &countingStore{}
	svc := NewService(store, ServiceConfig{
		Driver:       string(MediaStorageDisk),
		MaxBytes:     1024,
		AllowedTypes: []string{"video/mp4"},
	}, slog.New(slog.DiscardHandler))
	payload := "real bytes"
	wrong := checksumOf("different bytes")

	_, err := svc.Upload(context.Background(), UploadRequest{
		AccountID: "acct-1", Kind: MediaKindVideo, ContentType: "video/mp4",
		Size: int64(len(payload)), ExpectedChecksum: wrong,
		Body: strings.NewReader(payload),
	})
	if err == nil {
		t.Fatal("expected a rejection for a mismatched checksum")
	}
	if store.read != 0 {
		t.Errorf("store read %d bytes, want 0: a mismatch must not be stored", store.read)
	}
}

func TestUploadGivenUppercaseChecksum_ThenStillMatches(t *testing.T) {
	svc := newTestService(t)
	payload := "case bytes"

	_, err := svc.Upload(context.Background(), UploadRequest{
		AccountID: "acct-1", Kind: MediaKindVideo, ContentType: "video/mp4",
		Size: int64(len(payload)), ExpectedChecksum: strings.ToUpper(checksumOf(payload)),
		Body: strings.NewReader(payload),
	})
	if err != nil {
		t.Fatalf("uppercase checksum rejected: %v", err)
	}
}

func TestUploadGivenNoChecksum_ThenTheUploadStillSucceeds(t *testing.T) {
	svc := newTestService(t)

	if _, err := svc.Upload(context.Background(), UploadRequest{
		AccountID: "acct-1", Kind: MediaKindVideo, ContentType: "video/mp4",
		Size: 4, Body: strings.NewReader("data"),
	}); err != nil {
		t.Fatalf("upload without a declared checksum: %v", err)
	}
}

func TestUploadChecksumHeaderShape(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		want  string
		valid bool
	}{
		{name: "absent is legal", raw: "", want: "", valid: true},
		{name: "lowercase hex", raw: checksumOf("x"), want: checksumOf("x"), valid: true},
		{name: "uppercase hex", raw: strings.ToUpper(checksumOf("x")), want: checksumOf("x"), valid: true},
		{name: "too short", raw: "abc", valid: false},
		{name: "not hex", raw: strings.Repeat("z", 64), valid: false},
		{name: "sha256 prefixed", raw: "sha256:" + checksumOf("x"), valid: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := uploadChecksum(tc.raw)
			if ok != tc.valid {
				t.Fatalf("ok = %v, want %v for %q", ok, tc.valid, tc.raw)
			}
			if ok && got != tc.want {
				t.Errorf("checksum = %q, want %q", got, tc.want)
			}
		})
	}
}
