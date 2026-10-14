// Package publish persists the upload chunk rows of a publish attempt.
//
// @file      internal/publish/chunks.go
// @for       PostgreSQL access for publish_chunks.
// @uses      context, github.com/jackc/pgx/v5, github.com/google/uuid, internal/apierror
// @reason    Chunk bookkeeping is its own table and its own write path.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package publish

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
