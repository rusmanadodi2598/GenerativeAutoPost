// Package webhook runs the reconciler sweeps over replay and drain.
//
// @file      internal/webhook/sweeps.go
// @for       ReconcileOnce ordering and the unprocessed replay step.
// @uses      context, fmt, time
// @reason    Keeps reconciler.go short by holding the sweep orchestration beside it.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     worker
// @stability stable
// @since     2026-10-14
package webhook

import (
	"context"
	"fmt"
)

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
