// Package publish exercises the chunk proxy with in memory fakes.
//
// @file      internal/publish/chunks_fakes_test.go
// @for       Shared test doubles for the chunk proxy unit tests.
// @uses      bytes, context, errors, testing, time, internal/apierror, internal/tiktokclient
// @reason    Keeps the chunk proxy tests and their fakes in separate files under the line limit.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package publish

import (
	"bytes"
	"context"
	"errors"
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
