// Package compose relays outbox rows to a Redis stream consumer.
//
// @file      internal/compose/stream_sink.go
// @for       EventSink adapter publishing one outbox row to Redis.
// @uses      context, encoding/json, log/slog, github.com/redis/go-redis/v9, internal/webhook
// @reason    The outbox relay needs a consumer that stays idempotent on the outbox id, and streams carry that replay naturally.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     worker
// @stability stable
// @since     2026-10-16
package compose

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/redis/go-redis/v9"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/webhook"
)

// outboxStream names the stream the outbox relay appends to.
const outboxStream = "genauto:stream:events"

// streamSink publishes one outbox row per stream entry, keyed by outbox id so
// a republished row is visible to the consumer rather than written twice.
type streamSink struct {
	client *redis.Client
	logger *slog.Logger
}

// newStreamSink wraps a Redis client as the outbox consumer.
func newStreamSink(client *redis.Client, logger *slog.Logger) streamSink {
	if logger == nil {
		logger = slog.Default()
	}
	return streamSink{client: client, logger: logger}
}

// Publish appends one outbox row to the events stream.
func (s streamSink) Publish(ctx context.Context, rec webhook.OutboxRecord) error {
	payload, err := json.Marshal(map[string]any{
		"outbox_id":      rec.ID,
		"aggregate_type": rec.AggregateType,
		"aggregate_id":   rec.AggregateID,
		"event_type":     rec.EventType,
		"payload":        string(rec.Payload),
	})
	if err != nil {
		return err
	}
	if err := s.client.XAdd(ctx, &redis.XAddArgs{
		Stream: outboxStream,
		Values: map[string]any{"outbox_id": rec.ID, "body": string(payload)},
	}).Err(); err != nil {
		return err
	}
	return nil
}
