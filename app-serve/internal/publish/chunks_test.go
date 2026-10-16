// Package publish exercises the chunk proxy with in memory fakes.
//
// @file      internal/publish/chunks_test.go
// @for       Unit tests for PutChunk order, length, expiry, and completion.
// @uses      strings, testing, time, internal/apierror
// @reason    Chunk forwarding is the only path that touches TikTok upload URLs.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package publish

import (
	"strings"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// TestPutChunk_HappyPath_ForwardsPlannedRange checks the first chunk is sent
// with the computed Content-Range and recorded with the right response.
func TestPutChunk_HappyPath_ForwardsPlannedRange(t *testing.T) {
	h := newChunkHarness()
	got, err := h.put(0, 100)
	if err != nil {
		t.Fatalf("PutChunk returned error: %v", err)
	}
	if got.UploadedBytes != 100 || got.TotalBytes != 400 || got.NextChunkIndex != 1 || got.Complete {
		t.Fatalf("unexpected response: %+v", got)
	}
	if len(h.putter.calls) != 1 {
		t.Fatalf("expected one upstream call, got %d", len(h.putter.calls))
	}
	sent := h.putter.calls[0]
	if sent.First != 0 || sent.Last != 99 || sent.Total != 400 {
		t.Fatalf("unexpected chunk range: %+v", sent)
	}
	if sent.ContentRange() != "bytes 0-99/400" || h.putter.urls[0] != "https://upload.example/v4" {
		t.Fatalf("unexpected upstream request: %q", sent.ContentRange())
	}
	if len(h.recs.chunks) != 1 || h.recs.chunks[0].Range != "bytes 0-99/400" || h.recs.progress != 100 {
		t.Fatalf("unexpected chunk bookkeeping: %+v progress %d", h.recs.chunks, h.recs.progress)
	}
}

// TestPutChunk_OutOfOrder_RejectedWithoutUpstream checks index one is rejected
// before anything reaches TikTok.
func TestPutChunk_OutOfOrder_RejectedWithoutUpstream(t *testing.T) {
	h := newChunkHarness()
	_, err := h.put(1, 100)
	wantChunkCode(t, err, string(apierror.CodeValidationFailed))
	if !strings.Contains(err.Error(), "in order") {
		t.Fatalf("expected the order message, got %q", err.Error())
	}
	if len(h.putter.calls) != 0 {
		t.Fatalf("expected no upstream call, got %d", len(h.putter.calls))
	}
}

// TestPutChunk_WrongByteLength_RejectedWithoutUpstream checks a short payload
// never reaches the upload URL.
func TestPutChunk_WrongByteLength_RejectedWithoutUpstream(t *testing.T) {
	h := newChunkHarness()
	_, err := h.put(0, 99)
	wantChunkCode(t, err, string(apierror.CodeValidationFailed))
	if len(h.putter.calls) != 0 {
		t.Fatalf("expected no upstream call, got %d", len(h.putter.calls))
	}
}

// TestPutChunk_ExpiredWindow_RestartsPlan checks a stale window re-inits with
// the stored fields and forwards no chunk bytes.
func TestPutChunk_ExpiredWindow_RestartsPlan(t *testing.T) {
	h := newChunkHarness()
	h.now = h.now.Add(2 * time.Hour)
	got, err := h.put(0, 100)
	if err != nil {
		t.Fatalf("PutChunk returned error: %v", err)
	}
	if len(h.poster.videoCalls) != 1 {
		t.Fatalf("expected one init call, got %d", len(h.poster.videoCalls))
	}
	src := h.poster.videoCalls[0].SourceInfo
	if src.TotalChunkCount != 4 || src.ChunkSize != 100 || src.VideoSize != 400 || src.Source != sourceFileUpload {
		t.Fatalf("init carried the wrong plan: %+v", src)
	}
	if len(h.putter.calls) != 0 {
		t.Fatalf("expected no chunk bytes on restart, got %d", len(h.putter.calls))
	}
	if len(h.recs.updated) != 1 || h.recs.updated[0].PublishID != "v_pub_next" {
		t.Fatalf("expected the new publish id to persist, got %+v", h.recs.updated)
	}
	if got.NextChunkIndex != 0 || got.TotalBytes != 400 {
		t.Fatalf("unexpected restart response: %+v", got)
	}
}

// TestPutChunk_TerminalAttempt_Rejected checks a finished attempt rejects
// chunks with a conflict.
func TestPutChunk_TerminalAttempt_Rejected(t *testing.T) {
	h := newChunkHarness()
	h.recs.rec.Status = StatusPublishComplete
	_, err := h.put(0, 100)
	wantChunkCode(t, err, string(apierror.CodeConflict))
	if len(h.putter.calls) != 0 {
		t.Fatalf("expected no upstream call, got %d", len(h.putter.calls))
	}
}

// TestPutChunk_LastChunk_ReturnsComplete checks the last chunk marks the
// upload complete with next equal to the total chunk count.
func TestPutChunk_LastChunk_ReturnsComplete(t *testing.T) {
	h := newChunkHarness()
	for i := 0; i < 3; i++ {
		if _, err := h.put(i, 100); err != nil {
			t.Fatalf("PutChunk %d returned error: %v", i, err)
		}
		h.recs.rec.UploadedBytes += 100
	}
	got, err := h.put(3, 100)
	if err != nil {
		t.Fatalf("PutChunk returned error: %v", err)
	}
	if !got.Complete || got.NextChunkIndex != 4 {
		t.Fatalf("expected a complete last chunk, got %+v", got)
	}
	if got.TotalBytes != 400 || got.UploadedBytes != 400 {
		t.Fatalf("unexpected byte totals: %+v", got)
	}
}
