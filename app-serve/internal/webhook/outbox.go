// Package webhook relays published outbox rows to their consumer.
//
// @file      internal/webhook/outbox.go
// @for       PostgreSQL access for the outbox table.
// @uses      context, time, github.com/google/uuid, github.com/jackc/pgx/v5
// @reason    Keeps the publish noticing rows behind parameterized SQL without crowding store.go past the line limit.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-13
package webhook

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

const (
	outboxColumns = `id, aggregate_type, aggregate_id, event_type, payload, published_at`
	enqueueStmt   = `INSERT INTO outbox (id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5)`
)

// OutboxRecord is one pending transition awaiting a publisher.
type OutboxRecord struct {
	ID            string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       []byte
	PublishedAt   *time.Time
}

func errOutboxNotFound() *apierror.Error {
	return apierror.New(apierror.CodeNotFound, 0, "Outbox record not found.")
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
