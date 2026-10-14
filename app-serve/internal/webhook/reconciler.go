// Package webhook reconciles drift left by lost deliveries and failed sweeps.
//
// @file      internal/webhook/reconciler.go
// @for       Polling fallback that replays unprocessed rows and resolves stale open attempts.
// @uses      context, fmt, log/slog, time
// @reason    Replays deliveries TikTok never got an answer for, so no publish stalls silently. StatusUpdate is a local struct so this file never imports tiktokclient directly.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     worker
// @stability stable
// @since     2026-10-12
package webhook

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Reconciler defaults bound both sweeps, applied when a field is zero.
const (
	defaultReconcileInterval = 30 * time.Second
	defaultOpenAfter         = 10 * time.Minute
	defaultUnprocessedAfter  = 5 * time.Minute
	defaultReconcileBatch    = 50
	defaultReconcileTries    = 5
)

// exhaustReason is the fail reason stamped when the failure cap is reached. It
// is never a status: the publish status check constraint keeps six values, so
// exhaustion has to land in fail_reason.
const exhaustReason = "RECONCILE_EXHAUSTED"

// StatusUpdate is the reconciler's status verdict for one open attempt. It
// stays local so the reconciler never imports tiktokclient; the StatusFetcher
// maps the wire answer before returning.
type StatusUpdate struct {
	Status        string
	FailReason    string
	PostIDs       []string
	UploadedBytes int64
}

// StatusFetcher reads the upstream status for one publish id.
type StatusFetcher interface {
	FetchStatus(ctx context.Context, access string, publishID string) (StatusUpdate, error)
}

// RateGate caps status reads per account so reconciliation never spikes usage.
// The shape is narrower than tiktokclient.Limiter, whose Allow also takes an
// endpoint family and returns a remaining count; the wiring supplies a small
// adapter that drops both, which is why this file does not import that package.
type RateGate interface {
	Allow(ctx context.Context, accountID string) error
}

// ProgressRecorder stores confirmed upload bytes on an attempt that has not
// settled yet. It sits beside StatusUpdater rather than inside it because a
// byte count is not a state transition and must not travel the dispatcher path.
type ProgressRecorder interface {
	RecordProgress(ctx context.Context, publishID string, uploadedBytes int64) error
}

// OpenAttempt is one open attempt the sweep must resolve, carrying the failure
// count that bounds how often the sweep may poll it.
type OpenAttempt struct {
	PublishID     string
	AccountID     string
	AccessToken   string
	Failures      int
	Status        string
	FailReason    string
	PostID        string
	UploadedBytes int64
}

// OpenAttemptReader lists open attempts older than its argument, oldest first.
// It is injected because the publish repository has no stale open attempt query
// yet; Task 11 or a follow-up adds the real reader and this stays the seam.
type OpenAttemptReader interface {
	OpenAttempts(ctx context.Context, olderThan time.Time, limit int) ([]OpenAttempt, error)
}

// sweepStore is the persistence slice the reconciler drives. It is an
// interface so ReconcileOnce is testable without a database.
type sweepStore interface {
	Unprocessed(ctx context.Context, olderThan time.Duration, limit int) ([]Event, error)
	OutboxPending(ctx context.Context, limit int) ([]OutboxRecord, error)
	MarkOutboxPublished(ctx context.Context, id string, at time.Time) error
}

// ReconcilerConfig carries the sweep intervals and bounds.
type ReconcilerConfig struct {
	Interval         time.Duration
	OpenAfter        time.Duration
	UnprocessedAfter time.Duration
	BatchSize        int
	MaxAttempts      int
}

// withDefaults fills the zero fields from the reconciler defaults.
func (c ReconcilerConfig) withDefaults() ReconcilerConfig {
	if c.Interval <= 0 {
		c.Interval = defaultReconcileInterval
	}
	if c.OpenAfter <= 0 {
		c.OpenAfter = defaultOpenAfter
	}
	if c.UnprocessedAfter <= 0 {
		c.UnprocessedAfter = defaultUnprocessedAfter
	}
	if c.BatchSize <= 0 {
		c.BatchSize = defaultReconcileBatch
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = defaultReconcileTries
	}
	return c
}

// Reconciler replays unprocessed deliveries and resolves stale open attempts.
// Attempt statuses are applied through the shared StatusUpdater the dispatcher
// uses, so Task 8's publish state machine stays authoritative and no second
// writer path emerges here.
type Reconciler struct {
	store    sweepStore
	dispatch eventHandler
	statuses StatusUpdater
	attempts OpenAttemptReader
	fetch    StatusFetcher
	gate     RateGate
	progress ProgressRecorder
	cfg      ReconcilerConfig
	logger   *slog.Logger
}

// NewReconciler wires the reconciler over its stores and fetchers. A nil
// logger falls back to the process default.
func NewReconciler(store *Store, dispatch *Dispatcher, statuses StatusUpdater,
	attempts OpenAttemptReader, fetch StatusFetcher, gate RateGate,
	progress ProgressRecorder, cfg ReconcilerConfig, logger *slog.Logger) *Reconciler {
	if logger == nil {
		logger = slog.Default()
	}
	cfg = cfg.withDefaults()
	return &Reconciler{
		store: store, dispatch: dispatch, statuses: statuses,
		attempts: attempts, fetch: fetch, gate: gate, progress: progress,
		cfg: cfg, logger: logger,
	}
}

// Run loops ReconcileOnce on the configured interval until ctx is done.
func (r *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.ReconcileOnce(ctx); err != nil {
				r.logger.Warn("webhook reconciliation failed", "error", err.Error())
			}
		}
	}
}

// ReconcileOnce runs both sweeps in order within ctx. The unprocessed replay
// runs first, so a delivery that just landed is preferred over a polled status
// for the same publish id.
func (r *Reconciler) ReconcileOnce(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.replayUnprocessed(ctx); err != nil {
		return fmt.Errorf("webhook: replaying unprocessed events: %w", err)
	}
	if err := r.resolveOpen(ctx); err != nil {
		return fmt.Errorf("webhook: resolving open attempts: %w", err)
	}
	return nil
}

// replayUnprocessed re-runs the dispatcher over rows past the threshold. Rows
// younger than the threshold are left to the worker pool, so a live queue is
// never driven from two places at once.
func (r *Reconciler) replayUnprocessed(ctx context.Context) error {
	events, err := r.store.Unprocessed(ctx, r.cfg.UnprocessedAfter, r.cfg.BatchSize)
	if err != nil {
		return err
	}
	for _, ev := range events {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := r.dispatch.Handle(ctx, fromEvent(ev), ev.Payload); err != nil {
			r.logger.Warn("webhook replay failed, leaving row unprocessed",
				"dedupe_key", ev.DedupeKey, "error", err.Error())
		}
	}
	return nil
}

// fromEvent rebuilds the envelope hint the dispatcher logs with. Handle decodes
// the payload itself, so the hint carries only the event type.
func fromEvent(ev Event) Envelope {
	return Envelope{Event: ev.EventType}
}
