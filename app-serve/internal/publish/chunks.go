// Package publish stores upload chunks and proxies them to the TikTok upload window.
//
// @file      internal/publish/chunks.go
// @for       publish_chunks persistence and the PutChunk upload proxy.
// @uses      context, fmt, sync, time, pgx, uuid, internal/apierror, internal/tiktokclient
// @reason    Chunk bookkeeping and chunk forwarding are one flow with one owner.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package publish

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// ChunkRecord is one accepted upload range of an attempt.
type ChunkRecord struct {
	ID        string
	AttemptID string
	Index     int
	ByteStart int64
	ByteEnd   int64
	Range     string
}

// RecordChunk stores one accepted upload range for an attempt.
func (r *Repository) RecordChunk(ctx context.Context, rec ChunkRecord) error {
	id, err := uuid.Parse(rec.ID)
	if err != nil {
		return fmt.Errorf("publish: parsing chunk id: %w", err)
	}
	attempt, err := parseID(rec.AttemptID)
	if err != nil {
		return err
	}
	const insertChunk = `INSERT INTO publish_chunks
		(id, publish_attempt_id, chunk_index, byte_start, byte_end, content_range)
		VALUES ($1, $2, $3, $4, $5, $6)`
	if _, err := r.pool.Exec(ctx, insertChunk, id, attempt, rec.Index, rec.ByteStart, rec.ByteEnd, rec.Range); err != nil {
		return mapWriteErr(err)
	}
	return nil
}

// Chunks returns an attempt's ranges in index order.
func (r *Repository) Chunks(ctx context.Context, attemptID string) ([]ChunkRecord, error) {
	attempt, err := parseID(attemptID)
	if err != nil {
		return nil, err
	}
	const q = `SELECT id, publish_attempt_id, chunk_index, byte_start, byte_end, content_range
		FROM publish_chunks WHERE publish_attempt_id = $1 ORDER BY chunk_index ASC`
	rows, err := r.pool.Query(ctx, q, attempt)
	if err != nil {
		return nil, fmt.Errorf("publish: listing chunks: %w", err)
	}
	out, err := pgx.CollectRows(rows, scanChunk)
	if err != nil {
		return nil, fmt.Errorf("publish: collecting chunks: %w", err)
	}
	return out, nil
}

// scanChunk reads one publish_chunks row in the Chunks projection order.
func scanChunk(row pgx.CollectableRow) (ChunkRecord, error) {
	var (
		rec ChunkRecord
		id  uuid.UUID
	)
	if err := row.Scan(&id, &rec.AttemptID, &rec.Index, &rec.ByteStart, &rec.ByteEnd, &rec.Range); err != nil {
		return ChunkRecord{}, err
	}
	rec.ID = id.String()
	return rec, nil
}

// PutChunk validates one chunk against the stored plan and forwards it.
//
// Limitation: two parallel PUTs for the same index can both pass the order
// check, because that check reads the recorded chunk count without a lock. The
// reconciler of Task 10 owns that race, the unique chunk_index index is the
// only backstop here.
func (s *Service) PutChunk(ctx context.Context, operatorID string, attemptID string, index int, contentType string, data []byte) (ChunkPutResponse, error) {
	var empty ChunkPutResponse
	if index < 0 {
		return empty, apierror.New(apierror.CodeConflict, 0, "Chunk index must not be negative.")
	}
	if s.chunks.store == nil || s.chunks.uploader == nil || s.chunks.attempts == nil {
		return empty, apierror.New(apierror.CodeInternal, 0, "Chunk upload is not configured.")
	}
	rec, err := s.chunks.attempts.Get(ctx, attemptID)
	if err != nil {
		return empty, err
	}
	if rec.Status == StatusPublishComplete || rec.Status == StatusFailed {
		return empty, apierror.New(apierror.CodeConflict, 0, "Publish attempt is already finished.")
	}
	access, err := s.accounts.Owned(ctx, operatorID, rec.AccountID)
	if err != nil {
		return empty, err
	}
	window, ok := s.chunks.plans.Get(ctx, attemptID)
	if !ok || window.ChunkSize <= 0 || window.TotalBytes <= 0 {
		return empty, apierror.New(apierror.CodeConflict, 0, "No upload window is open for this publish attempt.")
	}
	recorded, err := s.chunks.store.Chunks(ctx, attemptID)
	if err != nil {
		return empty, err
	}
	if index != window.FirstChunk+len(recorded) || index >= window.TotalChunks {
		return empty, apierror.New(apierror.CodeValidationFailed, 0, "Chunks must be uploaded in order.")
	}
	chunk, err := buildChunk(window, index, contentType, data)
	if err != nil {
		return empty, err
	}
	if window.Expired(s.chunks.now(), s.chunks.ttl) {
		return s.restartWindow(ctx, access, rec, window, len(recorded))
	}
	if err := s.chunks.uploader.UploadChunk(ctx, window.UploadURL, chunk); err != nil {
		return empty, err
	}
	return s.recordChunk(ctx, rec, window, chunk)
}

// buildChunk computes the byte range of the index and checks the payload.
func buildChunk(window UploadWindow, index int, contentType string, data []byte) (tiktokclient.Chunk, error) {
	var out tiktokclient.Chunk
	if window.ContentType != "" && contentType != window.ContentType {
		return out, apierror.New(apierror.CodeValidationFailed, 0, "Chunk content type does not match the media.")
	}
	first := int64(index) * window.ChunkSize
	last := min(first+window.ChunkSize-1, window.TotalBytes-1)
	if int64(len(data)) != last-first+1 {
		return out, apierror.New(apierror.CodeValidationFailed, 0, "Chunk length does not match the planned byte range.")
	}
	return tiktokclient.Chunk{
		Index: index, First: first, Last: last, Total: window.TotalBytes,
		ContentType: contentType, Data: data,
	}, nil
}

// restartWindow re-inits the attempt and stores the fresh plan. The in flight
// bytes are not forwarded; the uploader retries them against the new window.
func (s *Service) restartWindow(ctx context.Context, access string, rec Record, previous UploadWindow, recorded int) (ChunkPutResponse, error) {
	var empty ChunkPutResponse
	upstream, err := s.poster.InitVideoPost(ctx, access, tiktokclient.VideoInitRequest{
		PostInfo: tiktokclient.PostInfo{Title: rec.Title, PrivacyLevel: rec.PrivacyLevel},
		SourceInfo: tiktokclient.SourceInfo{
			Source: rec.Source, VideoSize: previous.TotalBytes,
			ChunkSize: previous.ChunkSize, TotalChunkCount: previous.TotalChunks,
		},
	})
	if err != nil {
		return empty, err
	}
	window := UploadWindow{
		PublishID: upstream.PublishID, UploadURL: upstream.UploadURL,
		ChunkSize: previous.ChunkSize, TotalChunks: previous.TotalChunks,
		TotalBytes: previous.TotalBytes, ContentType: previous.ContentType,
		FirstChunk: previous.FirstChunk + recorded, InitAt: s.chunks.now(),
	}
	if err := s.chunks.plans.Save(ctx, rec.ID, window); err != nil {
		return empty, err
	}
	refreshed := rec
	refreshed.PublishID = upstream.PublishID
	if err := s.chunks.attempts.Update(ctx, refreshed); err != nil {
		return empty, err
	}
	return ChunkPutResponse{
		UploadedBytes: rec.UploadedBytes, TotalBytes: window.TotalBytes,
		NextChunkIndex: window.FirstChunk,
	}, nil
}

// recordChunk stores the accepted range and advances the confirmed byte count.
func (s *Service) recordChunk(ctx context.Context, rec Record, window UploadWindow, chunk tiktokclient.Chunk) (ChunkPutResponse, error) {
	var empty ChunkPutResponse
	id, err := uuid.NewV7()
	if err != nil {
		return empty, fmt.Errorf("publish: generating chunk id: %w", err)
	}
	if err := s.chunks.store.RecordChunk(ctx, ChunkRecord{
		ID: id.String(), AttemptID: rec.ID, Index: chunk.Index,
		ByteStart: chunk.First, ByteEnd: chunk.Last, Range: chunk.ContentRange(),
	}); err != nil {
		return empty, err
	}
	uploaded := rec.UploadedBytes + int64(len(chunk.Data))
	if err := s.chunks.store.UpdateProgress(ctx, rec.ID, uploaded); err != nil {
		return empty, err
	}
	next := chunk.Index + 1
	return ChunkPutResponse{
		UploadedBytes: uploaded, TotalBytes: chunk.Total,
		NextChunkIndex: next, Complete: next >= window.TotalChunks,
	}, nil
}

// defaultUploadURLTTL is the 002 section 4.4 upload window of one hour.
const defaultUploadURLTTL = time.Hour
