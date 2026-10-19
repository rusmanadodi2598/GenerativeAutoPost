// Package media validates uploads before they reach any object store.
//
// @file      internal/media/service_test.go
// @for       Coverage for upload validation, checksums, and URL allow checks.
// @uses      context, crypto/sha256, io, log/slog, testing
// @reason    Locks the rejection rules a handler relies on.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// newTestService builds a service over a disk store and a permissive config.
func newTestService(t *testing.T) *Service {
	t.Helper()
	return NewService(NewDisk(t.TempDir()), ServiceConfig{
		Driver:        string(MediaStorageDisk),
		MaxBytes:      1024,
		MaxDurationMs: 60_000,
		AllowedTypes:  []string{"video/mp4", "image/jpeg"},
		PublicBaseURL: "https://app-post.kentangtech.com",
	}, slog.New(slog.DiscardHandler))
}

func TestUploadRejectsDisallowedContentType(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.Upload(context.Background(), UploadRequest{AccountID: "acct-1", Kind: MediaKindVideo,
		ContentType: "video/quicktime", Size: 10, DurationMs: 1000, Body: strings.NewReader("payload")})

	if err == nil {
		t.Fatal("expected a rejection for a disallowed content type")
	}
}

func TestUploadRejectsOversizePayload(t *testing.T) {
	svc := newTestService(t)
	body := strings.Repeat("a", 1025)

	_, err := svc.Upload(context.Background(), UploadRequest{AccountID: "acct-1", Kind: MediaKindVideo,
		ContentType: "video/mp4", Size: int64(len(body)), DurationMs: 1000, Body: strings.NewReader(body)})

	if err == nil {
		t.Fatal("expected a rejection for an oversize payload")
	}
}

func TestUploadRejectsEmptySize(t *testing.T) {
	svc := newTestService(t)

	for _, size := range []int64{0, -1} {
		_, err := svc.Upload(context.Background(), UploadRequest{AccountID: "acct-1", Kind: MediaKindPhoto,
			ContentType: "image/jpeg", Size: size, Body: strings.NewReader("x")})
		if err == nil {
			t.Errorf("size %d was accepted, want rejection", size)
		}
	}
}

func TestUploadAcceptsAllowedTypeAndChecksumsBytes(t *testing.T) {
	svc := newTestService(t)
	payload := "exact bytes"

	obj, err := svc.Upload(context.Background(), UploadRequest{AccountID: "acct-1", Kind: MediaKindVideo,
		ContentType: "video/mp4", Size: int64(len(payload)), DurationMs: 1200, Body: strings.NewReader(payload)})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if obj.Kind != MediaKindVideo {
		t.Errorf("kind = %q, want video", obj.Kind)
	}
	if obj.Storage != MediaStorageDisk {
		t.Errorf("storage = %q, want disk", obj.Storage)
	}
	if obj.ByteSize != int64(len(payload)) {
		t.Errorf("byte size = %d, want %d", obj.ByteSize, len(payload))
	}
	if obj.ID == "" {
		t.Error("id is empty")
	}

	sum := sha256.Sum256([]byte(payload))
	if want := hex.EncodeToString(sum[:]); obj.ChecksumSHA256 != want {
		t.Errorf("checksum = %q, want %q", obj.ChecksumSHA256, want)
	}
}

func TestUploadRejectsDurationOverCap(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.Upload(context.Background(), UploadRequest{AccountID: "acct-1", Kind: MediaKindVideo,
		ContentType: "video/mp4", Size: 10, DurationMs: 60_001, Body: strings.NewReader("payload")})

	if err == nil {
		t.Fatal("expected a rejection for a duration over the cap")
	}
}

func TestUploadRejectsUnknownAccount(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.Upload(context.Background(), UploadRequest{Kind: MediaKindVideo,
		ContentType: "video/mp4", Size: 10, DurationMs: 100, Body: strings.NewReader("payload")})

	if err == nil {
		t.Fatal("expected a rejection for an empty account id")
	}
}

func TestPullFromURLRejectsHostOutsidePublicBase(t *testing.T) {
	svc := newTestService(t)

	err := svc.PullFromURL(context.Background(), "https://cdn.attacker.test/video.mp4")

	if err == nil {
		t.Fatal("expected a rejection for an unverified host")
	}
}

func TestPullFromURLRejectsNonHTTPScheme(t *testing.T) {
	svc := newTestService(t)

	for _, raw := range []string{
		"file:///etc/passwd",
		"ftp://app-post.kentangtech.com/video.mp4",
	} {
		if err := svc.PullFromURL(context.Background(), raw); err == nil {
			t.Errorf("%q was accepted, want rejection", raw)
		}
	}
}

func TestPullFromURLRejectsSuffixLookalikeHost(t *testing.T) {
	svc := newTestService(t)

	// A host that merely ends with the base domain is a different origin.
	err := svc.PullFromURL(context.Background(),
		"https://evilapp-post.kentangtech.com.attacker.test/video.mp4")

	if err == nil {
		t.Fatal("expected a rejection for a suffix lookalike host")
	}
}

func TestPullFromURLRejectsUnparseableURL(t *testing.T) {
	svc := newTestService(t)

	if err := svc.PullFromURL(context.Background(), "://not a url"); err == nil {
		t.Fatal("expected a rejection for an unparseable URL")
	}
}

func TestPullFromURLAcceptsVerifiedHost(t *testing.T) {
	svc := newTestService(t)

	if err := svc.PullFromURL(context.Background(),
		"https://app-post.kentangtech.com/media/video.mp4"); err != nil {
		t.Errorf("verified host rejected: %v", err)
	}
}

// countingStore records how many bytes the service streamed from the reader.
type countingStore struct {
	read int64
}

func (c *countingStore) Put(_ context.Context, _ MediaObject, r io.Reader) error {
	n, err := io.Copy(io.Discard, r)
	c.read = n
	return err
}

func (c *countingStore) Get(context.Context, string) (MediaObject, io.ReadCloser, error) {
	return MediaObject{}, nil, errObjectMissing
}

func (c *countingStore) Delete(context.Context, string) error { return nil }

func (c *countingStore) SignPublicURL(context.Context, string) (string, error) { return "", nil }

func TestUploadReadsStreamEvenWhenDeclaredSizeIsSmall(t *testing.T) {
	store := &countingStore{}
	svc := NewService(store, ServiceConfig{
		Driver:        string(MediaStorageDisk),
		MaxBytes:      8,
		MaxDurationMs: 1000,
		AllowedTypes:  []string{"video/mp4"},
	}, slog.New(slog.DiscardHandler))

	// The service reads one byte past the cap to catch an under-declared size,
	// so the payload never reaches the store.
	_, err := svc.Upload(context.Background(), UploadRequest{AccountID: "acct-1", Kind: MediaKindVideo,
		ContentType: "video/mp4", Size: 8, DurationMs: 100, Body: strings.NewReader(strings.Repeat("z", 64))})

	if err == nil {
		t.Fatal("expected a rejection for a stream beyond the cap")
	}
	if store.read != 0 {
		t.Errorf("store read %d bytes, want 0: the over-cap stream must not be stored", store.read)
	}
}

func TestUploadReadsAtMostCapPlusOneBytes(t *testing.T) {
	reader := &countingReader{}
	store := &countingStore{}
	svc := NewService(store, ServiceConfig{
		Driver:        string(MediaStorageDisk),
		MaxBytes:      8,
		MaxDurationMs: 1000,
		AllowedTypes:  []string{"video/mp4"},
	}, slog.New(slog.DiscardHandler))

	_, _ = svc.Upload(context.Background(), UploadRequest{AccountID: "acct-1", Kind: MediaKindVideo,
		ContentType: "video/mp4", Size: 8, DurationMs: 100, Body: reader})

	if reader.read != 9 {
		t.Errorf("read %d bytes, want 9: one past the cap before rejection", reader.read)
	}
}

// countingReader records how many bytes the service pulled from the stream.
type countingReader struct {
	read int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.read >= 64 {
		return 0, io.EOF
	}
	c.read += int64(len(p))
	return len(p), nil
}
