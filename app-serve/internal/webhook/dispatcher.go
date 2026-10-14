// Package webhook decodes TikTok webhook envelopes and event payloads.
//
// @file      internal/webhook/dispatcher.go
// @for       Event handling after signature verification.
// @uses      context, encoding/json, fmt, log/slog, strconv, time, github.com/google/uuid
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

// StatusBinder rebinds the publish updater to an open transaction, so the state
// change commits with the outbox row and the processed stamp. The publish
// repository supplies it by adapting its repository to the store's Tx; without
// one the dispatcher refuses to apply rather than commit state outside the
// transaction.
type StatusBinder interface {
	InTx(ctx context.Context, tx Tx) StatusUpdater
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
	OutcomeStoreFailed
	OutcomeApplyFailed
	OutcomeStored
	OutcomeApplied
)

// storeWriter is the persistence seam the dispatcher drives. It is an interface
// so Handle is testable without a database. The Redis namespace
// genauto:webhook:dedupe: is the earlier, per delivery window duplicate filter;
// the unique dedupe_key in the store is the durable guard.
type storeWriter interface {
	InsertIfNew(ctx context.Context, ev Event) (bool, error)
	CommitApplied(ctx context.Context, dedupeKey string, rec OutboxRecord, at time.Time,
		apply func(ctx context.Context, tx Tx) error) error
	DeleteEvent(ctx context.Context, dedupeKey string) error
}

// Dispatcher applies verified webhook envelopes to publish state.
type Dispatcher struct {
	store   storeWriter
	status  StatusUpdater
	binder  StatusBinder
	onAuth  func(ctx context.Context, userOpenID string) error
	aggType string
	logger  *slog.Logger
}

// NewDispatcher wires a dispatcher over the store, the status updater, the
// transaction binder, and a logger that falls back to the process default when
// nil.
func NewDispatcher(store *Store, statuses StatusUpdater, binder StatusBinder,
	cfg DispatcherConfig, logger *slog.Logger) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{store: store, status: statuses, binder: binder,
		aggType: cfg.OutboxAggregate, logger: logger}
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

// transition is the single updater call and outbox row for one delivery. The
// apply step takes the updater bound to the open transaction rather than the
// process wide one.
type transition struct {
	eventType string
	publishID string
	postID    string
	apply     func(ctx context.Context, up StatusUpdater) error
}

// Handle dedupes the envelope, then applies the state transition, the outbox
// row, and the processed stamp in one transaction. Duplicate deliveries return
// OutcomeDuplicate with no state change. Ignored events are logged and
// acknowledged rather than returned as errors, so TikTok stops retrying events
// this deployment never acts on.
func (d *Dispatcher) Handle(ctx context.Context, env Envelope, raw []byte) (Outcome, error) {
	trans, ignored, err := d.transitionFor(env)
	if err != nil {
		return OutcomeStoreFailed, err
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
		return OutcomeStoreFailed, fmt.Errorf("webhook: storing delivery: %w", err)
	}
	if !stored {
		d.logger.Info("webhook delivery duplicate, skipping state change",
			"event", trans.eventType, "dedupe_key", dedupeKey)
		return OutcomeDuplicate, nil
	}
	apply, err := d.applyFn(trans)
	if err != nil {
		d.discard(ctx, dedupeKey, trans.eventType, err)
		return OutcomeApplyFailed, err
	}
	if err := d.commit(ctx, eventID, dedupeKey, trans, apply); err != nil {
		d.discard(ctx, dedupeKey, trans.eventType, err)
		return OutcomeApplyFailed, err
	}
	return OutcomeApplied, nil
}

// applyFn builds the transaction scoped apply step. A missing binder is a
// wiring error: applying outside the transaction would let the state change
// survive without its outbox row.
func (d *Dispatcher) applyFn(trans transition) (func(ctx context.Context, tx Tx) error, error) {
	if d.binder == nil {
		return nil, fmt.Errorf("webhook: no transaction binder configured for event %q", trans.eventType)
	}
	return func(ctx context.Context, tx Tx) error {
		if err := trans.apply(ctx, d.binder.InTx(ctx, tx)); err != nil {
			return err
		}
		return nil
	}, nil
}

// discard removes the stored row after a failed apply. Durability property: the
// dedupe key must not outlive an unapplied delivery, or the retry answers as a
// duplicate and the state change is lost for good.
func (d *Dispatcher) discard(ctx context.Context, dedupeKey, eventType string, cause error) {
	if err := d.store.DeleteEvent(ctx, dedupeKey); err != nil {
		d.logger.Error("webhook apply failed and the stored row could not be removed",
			"event", eventType, "dedupe_key", dedupeKey,
			"apply_error", cause.Error(), "error", err.Error())
		return
	}
	d.logger.Warn("webhook apply failed, stored row removed so the retry re-applies",
		"event", eventType, "dedupe_key", dedupeKey, "error", cause.Error())
}

// commit writes the outbox row for the applied transition and stamps the event
// processed, in the same transaction as the state change. The outbox aggregate
// id is the event row id, which keeps the row traceable back to the exact
// delivery that caused it.
func (d *Dispatcher) commit(ctx context.Context, eventID, dedupeKey string,
	trans transition, apply func(ctx context.Context, tx Tx) error) error {
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
	if err := d.store.CommitApplied(ctx, dedupeKey, rec, time.Now().UTC(), apply); err != nil {
		return fmt.Errorf("webhook: committing applied event: %w", err)
	}
	return nil
}
