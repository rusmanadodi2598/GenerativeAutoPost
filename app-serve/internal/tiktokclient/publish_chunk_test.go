// Package tiktokclient_test pins chunk upload wire behavior.
//
// @file      internal/tiktokclient/publish_chunk_test.go
// @for       Table tests for chunk range validation and PUT headers.
// @uses      testing, context, internal/apierror
// @reason    Keeps the chunk tests readable next to their own fixture.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package tiktokclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

func TestUploadChunk(t *testing.T) {
	data := []byte("abcd")
	f := newFakeTikTok(t)
	f.handle(uploadPath, fakeResponse{status: http.StatusOK, body: `{}`})
	c := newTestClient(t, f)
	err := c.UploadChunk(context.Background(), f.URL()+uploadPath, Chunk{Index: 0, First: 0, Last: 3, Total: 4, ContentType: "video/mp4", Data: data})
	if err != nil {
		t.Fatalf("UploadChunk: %v", err)
	}
	req := f.lastRequest(t)
	if req.method != http.MethodPut {
		t.Fatalf("method = %s, want PUT", req.method)
	}
	if cr := req.headers.Get("Content-Range"); cr != "bytes 0-3/4" {
		t.Fatalf("Content-Range = %q", cr)
	}
	if cl := req.headers.Get("Content-Length"); cl != "4" {
		t.Fatalf("Content-Length = %q", cl)
	}
	if ct := req.headers.Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if req.body != "abcd" {
		t.Fatalf("body = %q, want abcd", req.body)
	}
	wantHits(t, f, 1)
}

// TestUploadChunkRejectsBadRange proves validation runs before any HTTP call.
func TestUploadChunkRejectsBadRange(t *testing.T) {
	tests := []struct {
		name  string
		chunk Chunk
	}{
		{name: "last before first", chunk: Chunk{Index: 0, First: 5, Last: 4, Total: 10, Data: []byte("x")}},
		{name: "last at total", chunk: Chunk{Index: 0, First: 0, Last: 10, Total: 10, Data: []byte("x")}},
		{name: "data length mismatch", chunk: Chunk{Index: 0, First: 0, Last: 3, Total: 4, Data: []byte("abc")}},
		{name: "zero total", chunk: Chunk{Index: 0, First: 0, Last: 0, Total: 0, Data: []byte("a")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeTikTok(t)
			c := newTestClient(t, f)
			err := c.UploadChunk(context.Background(), f.URL()+uploadPath, tc.chunk)
			wantError(t, err, apierror.CodeTikTokInvalidParam, "")
			wantHits(t, f, 0)
		})
	}
}

// TestUploadChunkRelativeURL proves a scheme-less upload URL joins the client base.
func TestUploadChunkRelativeURL(t *testing.T) {
	f := newFakeTikTok(t)
	f.handle(uploadPath, fakeResponse{status: http.StatusOK, body: `{}`})
	c := newTestClient(t, f)
	err := c.UploadChunk(context.Background(), uploadPath, Chunk{Index: 0, First: 0, Last: 3, Total: 4, Data: []byte("abcd")})
	if err != nil {
		t.Fatalf("UploadChunk: %v", err)
	}
	wantHits(t, f, 1)
}

// TestUploadChunkRejectsNonHTTPScheme proves ftp and other schemes never reach the wire.
func TestUploadChunkRejectsNonHTTPScheme(t *testing.T) {
	f := newFakeTikTok(t)
	f.handle(uploadPath, fakeResponse{status: http.StatusOK, body: `{}`})
	c := newTestClient(t, f)
	for _, raw := range []string{"ftp://upload.test/1", "file:///tmp/1"} {
		err := c.UploadChunk(context.Background(), raw, Chunk{Index: 0, First: 0, Last: 3, Total: 4, Data: []byte("abcd")})
		wantError(t, err, apierror.CodeTikTokInvalidParam, "")
	}
	wantHits(t, f, 0)
}

// TestChunkUploadTimeoutScalesWithPayload pins the 60s floor, 1s per MB, and 10m cap.
func TestChunkUploadTimeoutScalesWithPayload(t *testing.T) {
	tests := []struct {
		size int64
		want time.Duration
	}{
		{size: 4, want: 60 * time.Second},
		{size: 5 << 20, want: 65 * time.Second},
		{size: 64 << 20, want: 124 * time.Second},
		{size: 1 << 30, want: 10 * time.Minute},
	}
	for _, tc := range tests {
		if got := chunkUploadTimeout(tc.size); got != tc.want {
			t.Fatalf("chunkUploadTimeout(%d) = %s, want %s", tc.size, got, tc.want)
		}
	}
}

// TestEndpointDeadlineIsTypedUnavailable proves our own per-call timeout maps to
// the retryable unavailable code with the chunk detail, while parent cancellation
// still surfaces as context.Canceled.
func TestEndpointDeadlineIsTypedUnavailable(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(slow.Close)
	c := newTestClient(t, newFakeTikTok(t))
	_, err := c.doEndpoint(context.Background(), http.MethodPut, slow.URL, nil, nil, "video/mp4", 20*time.Millisecond, "The upload to TikTok timed out.")
	wantError(t, err, apierror.CodeTikTokUnavailable, "")
	var apiErr *apierror.Error
	if errors.As(err, &apiErr) && apiErr.Detail != "The upload to TikTok timed out." {
		t.Fatalf("detail = %q", apiErr.Detail)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.doEndpoint(ctx, http.MethodPut, slow.URL, nil, nil, "video/mp4", time.Second, "The upload to TikTok timed out.")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("parent cancellation err = %v, want context.Canceled", err)
	}
}
