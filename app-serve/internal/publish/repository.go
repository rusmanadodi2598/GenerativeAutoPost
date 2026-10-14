// Package publish persists publish attempts and their chunk bookkeeping.
//
// @file      internal/publish/repository.go
// @for       PostgreSQL access for publish_attempts and publish_chunks.
// @uses      context, time, github.com/jackc/pgx/v5, github.com/google/uuid, internal/apierror
// @reason    Keeps the attempt state machine's writes behind parameterized SQL.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package publish

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// attemptColumns is the shared projection for every attempt read and write.
const attemptColumns = `id, account_id, publish_id, post_mode, media_type, source, status, ` +
	`COALESCE(privacy_level, ''), COALESCE(title, ''), COALESCE(description, ''), ` +
	`COALESCE(fail_reason, ''), COALESCE(post_id, ''), uploaded_bytes, created_at`

// listQuery pages one account's attempts newest first. The status and cursor
// placeholders go inert on empty input, so one statement covers both listings.
const listQuery = `SELECT ` + attemptColumns + ` FROM publish_attempts
	WHERE account_id = $1 AND ($2 = '' OR status = $2) AND ($3::timestamptz IS NULL OR created_at < $3)
	ORDER BY created_at DESC LIMIT $4`

// insertAttempt writes the full attempt row. The unique index on publish_id is
// the concurrency guard for duplicate inits.
const insertAttempt = `INSERT INTO publish_attempts (id, account_id, publish_id, post_mode,
	media_type, source, status, privacy_level, title, description, fail_reason, post_id, uploaded_bytes)
	VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''), NULLIF($10, ''),
		NULLIF($11, ''), NULLIF($12, ''), $13) RETURNING ` + attemptColumns

// Page bounds: 20 by default, 100 at most.
const (
	defaultListLimit = 20
	maxListLimit     = 100
)

// Repository stores publish attempts and their chunks.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wires the publish store over the shared pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func errAttemptNotFound() *apierror.Error {
	return apierror.New(apierror.CodeNotFound, 0, "Publish attempt not found.")
}

// parseID turns a stored or incoming id into a uuid, keeping every parse failure
// on the shared not found path instead of leaking a driver error.
func parseID(id string) (uuid.UUID, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, errAttemptNotFound().Wrap(fmt.Errorf("publish: parsing attempt id: %w", err))
	}
	return uid, nil
}

// byID and byPublishID are the two single row reads for attempts.
const (
	byID        = `SELECT ` + attemptColumns + ` FROM publish_attempts WHERE id = $1`
	byPublishID = `SELECT ` + attemptColumns + ` FROM publish_attempts WHERE publish_id = $1`
)

// scanAttempt reads one row in attemptColumns order.
func scanAttempt(row pgx.Row) (Record, error) {
	var (
		rec       Record
		id, acct  uuid.UUID
		statusRaw string
	)
	if err := row.Scan(&id, &acct, &rec.PublishID, &rec.PostMode, &rec.MediaType, &rec.Source,
		&statusRaw, &rec.PrivacyLevel, &rec.Title, &rec.Description, &rec.FailReason,
		&rec.PostID, &rec.UploadedBytes, &rec.CreatedAt); err != nil {
		return Record{}, err
	}
	rec.ID = id.String()
	rec.AccountID = acct.String()
	rec.Status = Status(statusRaw)
	return rec, nil
}

// scanAttemptRow maps no rows to the shared not found error.
func scanAttemptRow(row pgx.Row) (Record, error) {
	rec, err := scanAttempt(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Record{}, errAttemptNotFound()
	case err != nil:
		return Record{}, fmt.Errorf("publish: loading attempt: %w", err)
	}
	return rec, nil
}

// Insert writes a new attempt row and returns the stored shape. The unique index
// on publish_id is the concurrency guard for duplicate inits.
func (r *Repository) Insert(ctx context.Context, rec Record) (Record, error) {
	id, err := uuid.Parse(rec.ID)
	if err != nil {
		return Record{}, fmt.Errorf("publish: parsing attempt id: %w", err)
	}
	account, err := uuid.Parse(rec.AccountID)
	if err != nil {
		return Record{}, fmt.Errorf("publish: parsing account id: %w", err)
	}
	stored, err := scanAttempt(r.pool.QueryRow(ctx, insertAttempt, id, account, rec.PublishID,
		rec.PostMode, rec.MediaType, rec.Source, rec.Status.String(), rec.PrivacyLevel,
		rec.Title, rec.Description, rec.FailReason, rec.PostID, rec.UploadedBytes))
	if err != nil {
		return Record{}, mapWriteErr(err)
	}
	return stored, nil
}

// Get loads one attempt by id.
func (r *Repository) Get(ctx context.Context, id string) (Record, error) {
	uid, err := parseID(id)
	if err != nil {
		return Record{}, err
	}
	return scanAttemptRow(r.pool.QueryRow(ctx, byID, uid))
}

// GetByPublishID loads one attempt by its unique TikTok publish id.
func (r *Repository) GetByPublishID(ctx context.Context, publishID string) (Record, error) {
	return scanAttemptRow(r.pool.QueryRow(ctx, byPublishID, publishID))
}

// List returns one page of an account's attempts, 20 by default and 100 at most.
func (r *Repository) List(ctx context.Context, accountID, status string, cursor time.Time, limit int) ([]Record, error) {
	account, err := parseID(accountID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	var cursorArg *time.Time
	if !cursor.IsZero() {
		utc := cursor.UTC()
		cursorArg = &utc
	}
	rows, err := r.pool.Query(ctx, listQuery, account, status, cursorArg, limit)
	if err != nil {
		return nil, fmt.Errorf("publish: listing attempts: %w", err)
	}
	defer rows.Close()
	out := make([]Record, 0, limit)
	for rows.Next() {
		rec, err := scanAttempt(rows)
		if err != nil {
			return nil, fmt.Errorf("publish: scanning attempt: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("publish: iterating attempts: %w", err)
	}
	return out, nil
}

// Update writes the outcome columns of a non terminal attempt. The guard refuses
// leaving PUBLISH_COMPLETE or FAILED; progress writes go through UpdateProgress.
func (r *Repository) Update(ctx context.Context, rec Record) error {
	id, err := parseID(rec.ID)
	if err != nil {
		return err
	}
	var completed *time.Time
	if rec.Status == StatusPublishComplete || rec.Status == StatusFailed {
		now := time.Now().UTC()
		completed = &now
	}
	const q = `UPDATE publish_attempts SET status = $2, fail_reason = NULLIF($3, ''),
		post_id = NULLIF($4, ''), uploaded_bytes = $5, completed_at = $6
		WHERE id = $1 AND status NOT IN ('PUBLISH_COMPLETE', 'FAILED')`
	tag, err := r.pool.Exec(ctx, q, id, rec.Status.String(), rec.FailReason, rec.PostID,
		rec.UploadedBytes, completed)
	if err != nil {
		return fmt.Errorf("publish: updating attempt: %w", err)
	}
	return expectAttemptRow(tag)
}

// UpdateProgress stores confirmed bytes with no status guard, so a late chunk
// write still lands after the row has settled.
func (r *Repository) UpdateProgress(ctx context.Context, attemptID string, uploadedBytes int64) error {
	id, err := parseID(attemptID)
	if err != nil {
		return err
	}
	const q = `UPDATE publish_attempts SET uploaded_bytes = $2 WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, id, uploadedBytes)
	if err != nil {
		return fmt.Errorf("publish: updating upload progress: %w", err)
	}
	return expectAttemptRow(tag)
}

// expectAttemptRow maps a zero row count to not found.
func expectAttemptRow(tag pgconn.CommandTag) error {
	if tag.RowsAffected() == 0 {
		return errAttemptNotFound()
	}
	return nil
}

// mapWriteErr turns unique violations into a typed conflict for the client.
func mapWriteErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return apierror.New(apierror.CodeConflict, 0, "Publish attempt already exists.").
			Wrap(fmt.Errorf("publish: unique violation on %s: %w", pgErr.ConstraintName, err))
	}
	return fmt.Errorf("publish: inserting row: %w", err)
}
