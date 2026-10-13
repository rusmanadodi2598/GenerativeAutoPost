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
	"net/http"
	"testing"

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
