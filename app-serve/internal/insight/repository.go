// Package insight reads and writes the insight snapshot tables.
//
// @file      internal/insight/repository.go
// @for       PostgreSQL persistence for video stats and creator snapshots.
// @uses      context, time, github.com/jackc/pgx/v5, github.com/google/uuid, internal/apierror
// @reason    Snapshots are append-only, so writes stay inserts and history is never rewritten.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package insight

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// statsColumns and creatorColumns are the single column lists every read and
// RETURNING clause share, so the scan order cannot drift from the projection.
const (
	statsColumns = `id, video_id, view_count, like_count, comment_count, share_count, fetched_at`

	creatorColumns = `id, account_id, privacy_level_options, comment_disabled, duet_disabled, ` +
		`stitch_disabled, max_video_post_duration_sec, fetched_at`
)

// Repository reads and appends insight snapshot rows.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wires the insight store over the shared pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// errSnapshotNotFound is the answer for a video or account with no snapshot yet.
func errSnapshotNotFound(detail string) *apierror.Error {
	return apierror.New(apierror.CodeNotFound, 0, detail)
}

// scanStats reads one row in statsColumns order. A NULL count scans back into a
// nil pointer, which is what keeps an omitted count distinct from a zero one.
func scanStats(row pgx.Row) (StatSnapshot, error) {
	var (
		snap StatSnapshot
		id   uuid.UUID
	)
	if err := row.Scan(&id, &snap.VideoID, &snap.ViewCount, &snap.LikeCount,
		&snap.CommentCount, &snap.ShareCount, &snap.FetchedAt); err != nil {
		return StatSnapshot{}, err
	}
	snap.ID = id.String()
	return snap, nil
}

// scanCreator reads one row in creatorColumns order.
func scanCreator(row pgx.Row) (CreatorSnapshot, error) {
	var (
		snap     CreatorSnapshot
		id       uuid.UUID
		account  uuid.UUID
		options  []string
		fetched  time.Time
		duration int
	)
	if err := row.Scan(&id, &account, &options, &snap.CommentDisabled, &snap.DuetDisabled,
		&snap.StitchDisabled, &duration, &fetched); err != nil {
		return CreatorSnapshot{}, err
	}
	snap.ID = id.String()
	snap.AccountID = account.String()
	if options == nil {
		options = []string{}
	}
	snap.PrivacyOptions = options
	snap.MaxVideoPostDurationSec = duration
	snap.FetchedAt = fetched
	return snap, nil
}

// InsertStats appends one point of the stats series. A nil count is written as
// SQL NULL, never as zero.
func (r *Repository) InsertStats(ctx context.Context, snap StatSnapshot) (StatSnapshot, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return StatSnapshot{}, fmt.Errorf("insight: generating stats id: %w", err)
	}
	const q = `INSERT INTO video_stats
		(id, video_id, view_count, like_count, comment_count, share_count, fetched_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING ` + statsColumns
	fetched := snap.FetchedAt
	if fetched.IsZero() {
		fetched = time.Now().UTC()
	}
	stored, err := scanStats(r.pool.QueryRow(ctx, q, id, snap.VideoID, snap.ViewCount, snap.LikeCount,
		snap.CommentCount, snap.ShareCount, fetched))
	if err != nil {
		return StatSnapshot{}, fmt.Errorf("insight: inserting stats: %w", err)
	}
	return stored, nil
}

// LatestStats returns the most recently fetched point for a video.
func (r *Repository) LatestStats(ctx context.Context, videoID string) (StatSnapshot, error) {
	const q = `SELECT ` + statsColumns + ` FROM video_stats WHERE video_id = $1 ORDER BY fetched_at DESC LIMIT 1`
	snap, err := scanStats(r.pool.QueryRow(ctx, q, videoID))
	if errors.Is(err, pgx.ErrNoRows) {
		return StatSnapshot{}, errSnapshotNotFound("Video stats not found.")
	}
	if err != nil {
		return StatSnapshot{}, fmt.Errorf("insight: loading latest stats: %w", err)
	}
	return snap, nil
}

// InsertCreatorSnapshot appends one cached creator record.
func (r *Repository) InsertCreatorSnapshot(ctx context.Context, rec CreatorSnapshot) (CreatorSnapshot, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return CreatorSnapshot{}, fmt.Errorf("insight: generating creator snapshot id: %w", err)
	}
	accountID, err := uuid.Parse(rec.AccountID)
	if err != nil {
		return CreatorSnapshot{}, errSnapshotNotFound("Creator account not found.").Wrap(
			fmt.Errorf("insight: parsing account id: %w", err))
	}
	const q = `INSERT INTO creator_info_snapshots
		(id, account_id, privacy_level_options, comment_disabled, duet_disabled, stitch_disabled,
		 max_video_post_duration_sec, fetched_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING ` + creatorColumns
	options := rec.PrivacyOptions
	if options == nil {
		options = []string{}
	}
	fetched := rec.FetchedAt
	if fetched.IsZero() {
		fetched = time.Now().UTC()
	}
	stored, err := scanCreator(r.pool.QueryRow(ctx, q, id, accountID, options, rec.CommentDisabled,
		rec.DuetDisabled, rec.StitchDisabled, rec.MaxVideoPostDurationSec, fetched))
	if err != nil {
		return CreatorSnapshot{}, fmt.Errorf("insight: inserting creator snapshot: %w", err)
	}
	return stored, nil
}

// LatestCreatorSnapshot returns the most recently fetched creator record.
func (r *Repository) LatestCreatorSnapshot(ctx context.Context, accountID string) (CreatorSnapshot, error) {
	id, err := uuid.Parse(accountID)
	if err != nil {
		return CreatorSnapshot{}, errSnapshotNotFound("Creator account not found.").Wrap(
			fmt.Errorf("insight: parsing account id: %w", err))
	}
	const q = `SELECT ` + creatorColumns + ` FROM creator_info_snapshots
		WHERE account_id = $1 ORDER BY fetched_at DESC LIMIT 1`
	snap, err := scanCreator(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return CreatorSnapshot{}, errSnapshotNotFound("Creator snapshot not found.")
	}
	if err != nil {
		return CreatorSnapshot{}, fmt.Errorf("insight: loading latest creator snapshot: %w", err)
	}
	return snap, nil
}
