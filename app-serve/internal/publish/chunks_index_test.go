// Package publish covers the optional chunk index header behaviour.
//
// @file      internal/publish/chunks_index_test.go
// @for       Tests for the default chunk index and the explicit index pinning.
// @uses      context, testing, internal/apierror
// @reason    The header is optional, so its default path needs its own tests.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package publish

import (
	"bytes"
	"context"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// putUnset uploads a payload without an index, letting the service resolve it.
func (h *chunkHarness) putUnset(size int) (ChunkPutResponse, error) {
	payload := bytes.Repeat([]byte{0x7f}, size)
	return h.svc.PutChunk(context.Background(), testOperator, chunkAttemptID, chunkIndexUnset, chunkMediaType, payload)
}

// TestPutChunk_UnsetIndex_ResolvesNextExpectedIndex checks two headerless PUTs
// land on index zero then index one.
func TestPutChunk_UnsetIndex_ResolvesNextExpectedIndex(t *testing.T) {
	h := newChunkHarness()
	first, err := h.putUnset(100)
	if err != nil {
		t.Fatalf("first PutChunk returned error: %v", err)
	}
	if first.UploadedBytes != 100 || first.NextChunkIndex != 1 {
		t.Fatalf("unexpected first response: %+v", first)
	}
	h.recs.rec.UploadedBytes = 100
	second, err := h.putUnset(100)
	if err != nil {
		t.Fatalf("second PutChunk returned error: %v", err)
	}
	if second.UploadedBytes != 200 || second.NextChunkIndex != 2 {
		t.Fatalf("unexpected second response: %+v", second)
	}
	if len(h.putter.calls) != 2 || h.putter.calls[0].Index != 0 || h.putter.calls[1].Index != 1 {
		t.Fatalf("expected indexes 0 then 1, got %+v", h.putter.calls)
	}
}

// TestPutChunk_ExplicitIndex_PinsIndex checks an explicit header still has to
// match the next expected index.
func TestPutChunk_ExplicitIndex_PinsIndex(t *testing.T) {
	h := newChunkHarness()
	if _, err := h.put(0, 100); err != nil {
		t.Fatalf("first PutChunk returned error: %v", err)
	}
	h.recs.rec.UploadedBytes = 100
	_, err := h.put(2, 100)
	wantChunkCode(t, err, string(apierror.CodeValidationFailed))
	if len(h.putter.calls) != 1 {
		t.Fatalf("expected only the pinned first chunk upstream, got %d", len(h.putter.calls))
	}
}

// TestPutChunk_ForeignAttempt_AnswersNotFound checks an ownership failure
// answers 404, the same shape Show returns.
func TestPutChunk_ForeignAttempt_AnswersNotFound(t *testing.T) {
	h := newChunkHarness()
	h.svc.accounts = &fakeAccounts{err: apierror.New(apierror.CodeForbidden, 0, "Not the account owner.")}
	_, err := h.putUnset(100)
	wantChunkCode(t, err, string(apierror.CodeNotFound))
	if len(h.putter.calls) != 0 {
		t.Fatalf("expected no upstream call, got %d", len(h.putter.calls))
	}
}

// TestChunkIndex_HeaderParsing checks the sentinel and the negative rejection.
func TestChunkIndex_HeaderParsing(t *testing.T) {
	if got, ok := chunkIndex(""); got != chunkIndexUnset || !ok {
		t.Fatalf("empty header = %d, %v", got, ok)
	}
	if got, ok := chunkIndex("3"); got != 3 || !ok {
		t.Fatalf("header 3 = %d, %v", got, ok)
	}
	if _, ok := chunkIndex("-1"); ok {
		t.Fatal("expected an explicit negative header to fail")
	}
}

// TestPutChunk_ExplicitNegativeOne_RejectedAtService proves an explicit -1
// reaches the service as a validation fault, not as the unset default.
func TestPutChunk_ExplicitNegativeOne_RejectedAtService(t *testing.T) {
	h := newChunkHarness()
	_, err := h.put(-1, 100)
	wantChunkCode(t, err, string(apierror.CodeValidationFailed))
	if len(h.putter.calls) != 0 {
		t.Fatalf("expected no upstream call, got %d", len(h.putter.calls))
	}
}
