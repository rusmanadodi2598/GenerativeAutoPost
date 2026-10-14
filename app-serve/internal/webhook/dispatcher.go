// Package webhook decodes TikTok webhook envelopes and event payloads.
//
// @file      internal/webhook/dispatcher.go
// @for       Event handling after signature verification.
// @uses      context, encoding/json, log/slog, strconv, time, github.com/google/uuid
// @reason    Applies deduped webhook transitions and records the outbox row.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// StatusUpdater is the publish state slice the dispatcher drives.
type StatusUpdater interface {
	MarkPublishComplete(ctx context.Context, publishID string, postID string) error
	MarkPublishFailed(ctx context.Context, publishID string, reason string) error
	MarkInboxDelivered(ctx context.Context, publishID string) error
	MarkUnpublished(ctx context.Context, publishID string, postID string) error
}

// DispatcherConfig carries dispatcher wiring beyond the store and statuses.
type DispatcherConfig struct {
	OutboxAggregate string
}

// Outcome reports how one delivery was treated.
type Outcome int

const (
	OutcomeIgnored Outcome = iota
	OutcomeDuplicate
	OutcomeStored
	OutcomeApplied
)

// storeWriter is the persistence seam the dispatcher drives. It is an interface
// so Handle is testable without a database. The Redis namespace
// genauto:webhook:dedupe: is the earlier, per delivery window duplicate filter;
// the unique dedupe_key in the store is the durable guard.
type storeWriter interface {
	InsertIfNew(ctx context.Context, ev Event) (bool, error)
	CommitApplied(ctx context.Context, dedupeKey string, rec OutboxRecord, at time.Time) error
}

// Dispatcher applies verified webhook envelopes to publish state.
type Dispatcher struct {
	store   storeWriter
	status  StatusUpdater
	onAuth  func(ctx context.Context, userOpenID string) error
	aggType string
	logger  *slog.Logger
}

// NewDispatcher wires a dispatcher over the store, the status updater, and a
// logger that falls back to the process default when nil.
func NewDispatcher(store *Store, statuses StatusUpdater, cfg DispatcherConfig, logger *slog.Logger) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{store: store, status: statuses, aggType: cfg.OutboxAggregate, logger: logger}
}

// OnAuthRemoved registers the account side handler for removed
// authorizations. It must delete token material and stop scheduled publishes.
func (d *Dispatcher) OnAuthRemoved(fn func(ctx context.Context, userOpenID string) error) {
	d.onAuth = fn
}

// DedupeKey builds the stable dedupe key for one delivery. Post publish events
// key on post_id when present and create_time otherwise. The
// authorization.removed event carries no publish id, so it keys on user_openid.
func (d *Dispatcher) DedupeKey(env Envelope, publishID, postID string) string {
	if env.Event == EventAuthRemoved {
		return env.Event + ":" + env.UserOpenID + ":" + strconv.FormatInt(env.CreateTime, 10)
	}
	suffix := postID
	if suffix == "" {
		suffix = strconv.FormatInt(env.CreateTime, 10)
	}
	return env.Event + ":" + publishID + ":" + suffix
}

// transition is the single updater call and outbox row for one delivery.
type transition struct {
	eventType string
	publishID string
	postID    string
	apply     func(ctx context.Context) error
}

// Handle dedupes the envelope, applies the state transition, then records the
// outbox row in the same transaction as the processed stamp. Duplicate
// deliveries return OutcomeDuplicate with no state change. Ignored events are
// logged and acknowledged rather than returned as errors, so TikTok stops
// retrying events this deployment never acts on.
func (d *Dispatcher) Handle(ctx context.Context, env Envelope, raw []byte) (Outcome, error) {
	trans, ignored, err := d.transitionFor(env)
	if err != nil {
		return OutcomeStored, err
	}
	if ignored {
		return OutcomeIgnored, nil
	}
	dedupeKey := d.DedupeKey(env, trans.publishID, trans.postID)
	payload := raw
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	eventID := uuid.NewString()

	stored, err := d.store.InsertIfNew(ctx, Event{
		ID: eventID, DedupeKey: dedupeKey, EventType: trans.eventType,
		PublishID: trans.publishID, PostID: trans.postID, Payload: payload,
	})
	if err != nil {
		return OutcomeStored, fmt.Errorf("webhook: storing delivery: %w", err)
	}
	if !stored {
		d.logger.Info("webhook delivery duplicate, skipping state change",
			"event", trans.eventType, "dedupe_key", dedupeKey)
		return OutcomeDuplicate, nil
	}
	if err := trans.apply(ctx); err != nil {
		return OutcomeStored, fmt.Errorf("webhook: applying transition: %w", err)
	}
	if err := d.commit(ctx, eventID, dedupeKey, trans); err != nil {
		return OutcomeStored, err
	}
	return OutcomeApplied, nil
}

// commit writes the outbox row for the applied transition and stamps the event
// processed. The outbox aggregate id is the event row id, which keeps the row
// traceable back to the exact delivery that caused it.
func (d *Dispatcher) commit(ctx context.Context, eventID, dedupeKey string, trans transition) error {
	payload, err := json.Marshal(map[string]string{
		"event": trans.eventType, "publish_id": trans.publishID, "post_id": trans.postID,
	})
	if err != nil {
		return fmt.Errorf("webhook: encoding outbox payload: %w", err)
	}
	aggType := d.aggType
	if aggType == "" {
		aggType = "webhook_event"
	}
	rec := OutboxRecord{
		AggregateType: aggType, AggregateID: eventID,
		EventType: trans.eventType, Payload: payload,
	}
	if err := d.store.CommitApplied(ctx, dedupeKey, rec, time.Now().UTC()); err != nil {
		return fmt.Errorf("webhook: committing applied event: %w", err)
	}
	return nil
}

// transitionFor decodes the envelope and selects the updater call. The second
// return reports an acknowledged no-op, which covers both unknown event names
// and the Video Kit events the Content Posting flow never uses.
func (d *Dispatcher) transitionFor(env Envelope) (transition, bool, error) {
	switch env.Event {
	case EventVideoUploadFailed, EventVideoPublishDone:
		d.logger.Info("webhook event ignored, unused by content posting", "event", env.Event)
		return transition{}, true, nil
	case EventAuthRemoved:
		return d.authTransition(env)
	case EventComplete, EventPubliclyAvailable, EventFailed, EventInboxDelivered,
		EventNoLongerPublicly:
		return d.publishTransition(env)
	default:
		d.logger.Info("webhook event ignored, unknown type", "event", env.Event)
		return transition{}, true, nil
	}
}

// publishTransition maps a post.publish event to its updater call.
func (d *Dispatcher) publishTransition(env Envelope) (transition, bool, error) {
	var content PostPublishContent
	if err := decodedContent(env, &content); err != nil {
		return transition{}, false, err
	}
	t := transition{eventType: env.Event, publishID: content.PublishID, postID: content.PostID}
	switch env.Event {
	case EventComplete, EventPubliclyAvailable:
		t.apply = func(ctx context.Context) error {
			return d.status.MarkPublishComplete(ctx, content.PublishID, content.PostID)
		}
	case EventFailed:
		t.apply = func(ctx context.Context) error {
			return d.status.MarkPublishFailed(ctx, content.PublishID, content.Reason)
		}
	case EventInboxDelivered:
		t.apply = func(ctx context.Context) error {
			return d.status.MarkInboxDelivered(ctx, content.PublishID)
		}
	default:
		t.apply = func(ctx context.Context) error {
			return d.status.MarkUnpublished(ctx, content.PublishID, content.PostID)
		}
	}
	return t, false, nil
}

// authTransition builds the account side transition for a removed authorization.
func (d *Dispatcher) authTransition(env Envelope) (transition, bool, error) {
	var content AuthorizationRemovedContent
	if err := decodedContent(env, &content); err != nil {
		return transition{}, false, err
	}
	t := transition{eventType: env.Event}
	t.apply = func(ctx context.Context) error {
		if d.onAuth == nil {
			return nil
		}
		return d.onAuth(ctx, env.UserOpenID)
	}
	return t, false, nil
}
