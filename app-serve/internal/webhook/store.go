// Package webhook persists webhook events and the outbox.
//
// @file      internal/webhook/store.go
// @for       PostgreSQL access for webhook_events and outbox.
// @uses      context, time, github.com/jackc/pgx/v5, github.com/google/uuid, internal/apierror
// @reason    Keeps duplicate suppression and outbox writes behind parameterized SQL.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package webhook

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// Shared projections, one per table, so reads and writes cannot drift.
const (
	eventColumns    = `id, dedupe_key, event_type, COALESCE(publish_id, ''), COALESCE(post_id, ''), payload, received_at, processed_at`
	outboxColumns   = `id, aggregate_type, aggregate_id, event_type, payload, published_at`
	insertEventStmt = `INSERT INTO webhook_events (id, dedupe_key, event_type, publish_id, post_id, payload)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6) ON CONFLICT (dedupe_key) DO NOTHING`
	markEventStmt = `UPDATE webhook_events SET processed_at = $2 WHERE dedupe_key = $1`
	enqueueStmt   = `INSERT INTO outbox (id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5)`
)

// Page bounds for the two sweep queries.
const (
	defaultSweepLimit = 50
	maxSweepLimit     = 500
)

// Event is one stored webhook delivery.
type Event struct {
	ID          string
	DedupeKey   string
	EventType   string
	PublishID   string
	PostID      string
	Payload     []byte
	ReceivedAt  time.Time
	ProcessedAt *time.Time
}

// OutboxRecord is one pending transition awaiting a publisher.
type OutboxRecord struct {
	ID            string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       []byte
	PublishedAt   *time.Time
}

// Store reads and writes webhook_events and outbox.
type Store struct{ pool *pgxpool.Pool }

// NewStore wires the webhook store over the shared pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func errEventNotFound() *apierror.Error {
	return apierror.New(apierror.CodeNotFound, 0, "Webhook event not found.")
}

func errOutboxNotFound() *apierror.Error {
	return apierror.New(apierror.CodeNotFound, 0, "Outbox record not found.")
}

// clampLimit applies the shared default and ceiling for sweep queries.
func clampLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultSweepLimit
	case limit > maxSweepLimit:
		return maxSweepLimit
	default:
		return limit
	}
}

// newEventID defaults an empty id to a random uuid.
func newEventID(id string) string {
	if id == "" {
		return uuid.NewString()
	}
	return id
}

// InsertIfNew stores the event under its dedupe key. It reports stored=false
// when the key already exists, which is the duplicate delivery no-op. The
// unique index on dedupe_key is the guard; the Redis namespace
// genauto:webhook:dedupe: is the earlier, per delivery window filter.
func (s *Store) InsertIfNew(ctx context.Context, ev Event) (bool, error) {
	tag, err := s.pool.Exec(ctx, insertEventStmt, newEventID(ev.ID), ev.DedupeKey,
		ev.EventType, ev.PublishID, ev.PostID, ev.Payload)
	if err != nil {
		return false, fmt.Errorf("webhook: inserting event: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// MarkProcessed stamps the settled time on an applied event.
func (s *Store) MarkProcessed(ctx context.Context, dedupeKey string, at time.Time) error {
	tag, err := s.pool.Exec(ctx, markEventStmt, dedupeKey, at.UTC())
	if err != nil {
		return fmt.Errorf("webhook: marking event processed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errEventNotFound()
	}
	return nil
}

// sweep runs one bounded read and folds the rows through scan. It backs both
// retry sweeps so neither can drift into an unbounded query. The any parameter
// carries pgx variadic query arguments, the driver call boundary; every row is
// decoded straight into the typed structs above.
func sweep[T any](ctx context.Context, pool *pgxpool.Pool, query string, limit int, args []any, scan func(pgx.Row) (T, error), what string) ([]T, error) {
	limit = clampLimit(limit)
	rows, err := pool.Query(ctx, query, append(args, limit)...)
	if err != nil {
		return nil, fmt.Errorf("webhook: listing %s: %w", what, err)
	}
	defer rows.Close()
	out := make([]T, 0, limit)
	for rows.Next() {
		rec, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("webhook: scanning %s: %w", what, err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("webhook: iterating %s: %w", what, err)
	}
	return out, nil
}

// Unprocessed returns unprocessed events received before now minus olderThan,
// oldest first, so a retry sweep replays in arrival order.
func (s *Store) Unprocessed(ctx context.Context, olderThan time.Duration, limit int) ([]Event, error) {
	return sweep(ctx, s.pool, `SELECT `+eventColumns+` FROM webhook_events
		WHERE processed_at IS NULL AND received_at <= $1 ORDER BY received_at ASC LIMIT $2`,
		limit, []any{time.Now().UTC().Add(-olderThan)}, scanEvent, "events")
}

// EnqueueOutbox writes one pending transition.
func (s *Store) EnqueueOutbox(ctx context.Context, rec OutboxRecord) error {
	aggregateID, err := uuid.Parse(rec.AggregateID)
	if err != nil {
		return fmt.Errorf("webhook: parsing outbox aggregate id: %w", err)
	}
	if _, err := s.pool.Exec(ctx, enqueueStmt, newEventID(rec.ID), rec.AggregateType,
		aggregateID, rec.EventType, rec.Payload); err != nil {
		return fmt.Errorf("webhook: inserting outbox row: %w", err)
	}
	return nil
}

// OutboxPending returns unpublished rows oldest first.
func (s *Store) OutboxPending(ctx context.Context, limit int) ([]OutboxRecord, error) {
	return sweep(ctx, s.pool, `SELECT `+outboxColumns+` FROM outbox
		WHERE published_at IS NULL ORDER BY id ASC LIMIT $1`,
		limit, nil, scanOutbox, "outbox rows")
}

// MarkOutboxPublished stamps the published time on a delivered row.
func (s *Store) MarkOutboxPublished(ctx context.Context, id string, at time.Time) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return errOutboxNotFound().Wrap(fmt.Errorf("webhook: parsing outbox id: %w", err))
	}
	tag, err := s.pool.Exec(ctx, `UPDATE outbox SET published_at = $2 WHERE id = $1`, uid, at.UTC())
	if err != nil {
		return fmt.Errorf("webhook: marking outbox published: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errOutboxNotFound()
	}
	return nil
}

// CommitApplied writes the outbox row and stamps the event processed in one
// transaction, so a crash between the two replays neither half twice.
func (s *Store) CommitApplied(ctx context.Context, dedupeKey string, rec OutboxRecord, at time.Time) error {
	aggregateID, err := uuid.Parse(rec.AggregateID)
	if err != nil {
		return fmt.Errorf("webhook: parsing outbox aggregate id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("webhook: beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, enqueueStmt, newEventID(rec.ID), rec.AggregateType,
		aggregateID, rec.EventType, rec.Payload); err != nil {
		return fmt.Errorf("webhook: inserting outbox row: %w", err)
	}
	tag, err := tx.Exec(ctx, markEventStmt, dedupeKey, at.UTC())
	if err != nil {
		return fmt.Errorf("webhook: marking event processed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errEventNotFound()
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("webhook: committing applied event: %w", err)
	}
	return nil
}

// scanEvent reads one row in eventColumns order.
func scanEvent(row pgx.Row) (Event, error) {
	var (
		ev     Event
		id     uuid.UUID
		procAt *time.Time
	)
	err := row.Scan(&id, &ev.DedupeKey, &ev.EventType, &ev.PublishID, &ev.PostID,
		&ev.Payload, &ev.ReceivedAt, &procAt)
	if err != nil {
		return Event{}, err
	}
	ev.ID, ev.ProcessedAt = id.String(), procAt
	return ev, nil
}

// scanOutbox reads one row in outboxColumns order.
func scanOutbox(row pgx.Row) (OutboxRecord, error) {
	var (
		rec         OutboxRecord
		id, aggID   uuid.UUID
		publishedAt *time.Time
	)
	err := row.Scan(&id, &rec.AggregateType, &aggID, &rec.EventType, &rec.Payload, &publishedAt)
	if err != nil {
		return OutboxRecord{}, err
	}
	rec.ID, rec.AggregateID, rec.PublishedAt = id.String(), aggID.String(), publishedAt
	return rec, nil
}
