// Package publish exercises the chunk proxy with in memory fakes.
//
// @file      internal/publish/chunks_test.go
// @for       Unit tests for PutChunk order, length, expiry, and completion.
// @uses      context, testing, strings, time, internal/apierror errors
// @reason    Chunk forwarding is the only path that touches TikTok upload URLs.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package publish

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

const (
	chunkAttemptID = "6f1b7a4e-1c2d-4f3a-9b8c-0d1e2f3a4b5c"
	chunkMediaType = "video/mp4"
)

// fakeChunkStore keeps attempt rows, chunk rows, and progress in memory.
type fakeChunkStore struct {
	rec      Record
	chunks   []ChunkRecord
	progress int64
	updated  []Record
}

// Get returns the canned attempt row.
func (f *fakeChunkStore) Get(context.Context, string) (Record, error) { return f.rec, nil }

// GetByPublishID returns the canned row when its publish id matches.
func (f *fakeChunkStore) GetByPublishID(_ context.Context, publishID string) (Record, error) {
	if f.rec.PublishID != publishID {
		return Record{}, apierror.New(apierror.CodeNotFound, 0, "Publish attempt not found.")
	}
	return f.rec, nil
}

// RecordChunk appends one accepted range.
func (f *fakeChunkStore) RecordChunk(_ context.Context, rec ChunkRecord) error {
	f.chunks = append(f.chunks, rec)
	return nil
}

// Chunks returns the recorded ranges in index order.
func (f *fakeChunkStore) Chunks(context.Context, string) ([]ChunkRecord, error) {
	return append([]ChunkRecord(nil), f.chunks...), nil
}

// UpdateProgress stores the confirmed byte count.
func (f *fakeChunkStore) UpdateProgress(_ context.Context, _ string, n int64) error {
	f.progress = n
	return nil
}

// Update records the refreshed attempt row after a window restart.
func (f *fakeChunkStore) Update(_ context.Context, rec Record) error {
	f.updated = append(f.updated, rec)
	f.rec = rec
	return nil
}

// fakeChunkPutter records the upstream calls it receives.
type fakeChunkPutter struct {
	calls []tiktokclient.Chunk
	urls  []string
	err   error
}

// UploadChunk records one upstream call.
func (f *fakeChunkPutter) UploadChunk(_ context.Context, url string, chunk tiktokclient.Chunk) error {
	f.urls = append(f.urls, url)
	f.calls = append(f.calls, chunk)
	return f.err
}

// chunkHarness wires a Service with chunk collaborators and a fresh plan of
// four 100 byte chunks over 400 bytes.
type chunkHarness struct {
	svc    *Service
	recs   *fakeChunkStore
	putter *fakeChunkPutter
	poster *fakePoster
	plans  *MemoryPlanStore
	now    time.Time
}

// newChunkHarness wires the service and stores the initial window.
func newChunkHarness() *chunkHarness {
	h := &chunkHarness{
		recs: &fakeChunkStore{rec: Record{
			ID: chunkAttemptID, AccountID: testAccountID, PublishID: testPublishID,
			PostMode: modeDirectPost, MediaType: "VIDEO", Source: sourceFileUpload,
			Status: StatusCreated, Title: "clip", PrivacyLevel: "FOLLOWERS",
		}},
		putter: &fakeChunkPutter{},
		poster: &fakePoster{uploadURL: "https://upload.example/next", publishID: "v_pub_next"},
		now:    time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC),
		plans:  NewMemoryPlanStore(),
	}
	if err := h.plans.Save(context.Background(), chunkAttemptID, UploadWindow{
		PublishID: testPublishID, UploadURL: "https://upload.example/v4",
		ChunkSize: 100, TotalChunks: 4, TotalBytes: 400,
		ContentType: chunkMediaType, FirstChunk: 0, InitAt: h.now,
	}); err != nil {
		panic(err)
	}
	h.svc = NewService(Config{
		Poster: h.poster, Accounts: &fakeAccounts{}, Creators: &fakeCreators{}, Attempts: &fakeAttempts{},
		Idempotency: newFakeIdempotency(), MediaHost: "media.example",
	}).WithChunkProxy(ChunkServiceConfig{
		Attempts: h.recs, Chunks: h.recs, Plans: h.plans, Uploader: h.putter,
		TTL: time.Hour, Now: func() time.Time { return h.now },
	})
	return h
}

// put uploads a payload of size bytes at the index.
func (h *chunkHarness) put(index int, size int) (ChunkPutResponse, error) {
	payload := bytes.Repeat([]byte{byte(index + 1)}, size)
	return h.svc.PutChunk(context.Background(), testOperator, chunkAttemptID, index, chunkMediaType, payload)
}

// wantChunkCode fails unless err carries the apierror code.
func wantChunkCode(t *testing.T, err error, want string) {
	t.Helper()
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected a typed error, got %v", err)
	}
	if string(apiErr.Code) != want {
		t.Fatalf("expected %s, got %s", want, apiErr.Code)
	}
}

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
