// Package webhook runs webhook deliveries on a bounded worker pool.
//
// @file      internal/webhook/worker.go
// @for       Bounded pool that applies queued webhook deliveries with bounded retries.
// @uses      context, encoding/json, log/slog, math/rand, runtime/debug, sync, time
// @reason    Keeps intake latency independent of dispatcher cost and gives backpressure instead of unbounded growth.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     worker
// @stability stable
// @since     2026-10-12
package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"runtime/debug"
	"sync"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// Pool defaults from 002 section 5.4 step 5, applied when a field is zero.
const (
	defaultWorkerSize  = 4
	defaultQueueDepth  = 64
	defaultRetryBase   = 250 * time.Millisecond
	defaultRetryMax    = 30 * time.Second
	maxJobAttempts     = 3
	errQueueFullDetail = "The webhook worker queue is full. Retry shortly."
)

// eventHandler is the dispatcher slice the pool drives. It is an interface so a
// panicking or failing dispatcher can be exercised without a database.
type eventHandler interface {
	Handle(ctx context.Context, env Envelope, raw []byte) (Outcome, error)
}

// WorkerConfig carries the pool sizing and retry bounds.
type WorkerConfig struct {
	Size       int
	QueueDepth int
	RetryBase  time.Duration
	RetryMax   time.Duration
}

// withDefaults fills the zero fields from the pool defaults.
func (c WorkerConfig) withDefaults() WorkerConfig {
	if c.Size <= 0 {
		c.Size = defaultWorkerSize
	}
	if c.QueueDepth <= 0 {
		c.QueueDepth = defaultQueueDepth
	}
	if c.RetryBase <= 0 {
		c.RetryBase = defaultRetryBase
	}
	if c.RetryMax <= 0 {
		c.RetryMax = defaultRetryMax
	}
	return c
}

// Worker is a fixed size pool of consumers over a bounded event queue.
type Worker struct {
	dispatch eventHandler
	queue    chan Event
	cfg      WorkerConfig
	logger   *slog.Logger
	wg       sync.WaitGroup
}

// NewWorker wires a pool over the dispatcher. A nil logger falls back to the
// process default. The store is not held here: the dispatcher owns persistence,
// and the reconciler owns the rows this pool leaves behind after the retry cap.
func NewWorker(dispatch *Dispatcher, cfg WorkerConfig, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	cfg = cfg.withDefaults()
	return &Worker{
		dispatch: dispatch,
		queue:    make(chan Event, cfg.QueueDepth),
		cfg:      cfg,
		logger:   logger,
	}
}

// QueueFullError is the typed signal an intake returns as 503 when Enqueue
// reports false, so TikTok retries instead of the delivery being dropped.
func QueueFullError() *apierror.Error {
	return apierror.New(apierror.CodeServiceUnavailable, 0, errQueueFullDetail)
}

// Enqueue offers one delivery to the pool. It reports false when the queue is
// full, which the caller answers with QueueFullError rather than dropping.
func (w *Worker) Enqueue(_ context.Context, ev Event) bool {
	select {
	case w.queue <- ev:
		return true
	default:
		return false
	}
}

// Run starts the pool and blocks until ctx is done and every in flight job has
// finished. Jobs run synchronously inside their worker, so the wait covers work
// already started and Run returning is the pool's termination condition.
func (w *Worker) Run(ctx context.Context) {
	w.wg.Add(w.cfg.Size)
	for range w.cfg.Size {
		go func() {
			defer w.wg.Done()
			w.consume(ctx)
		}()
	}
	w.wg.Wait()
}

// consume runs one worker's loop until the context is cancelled.
func (w *Worker) consume(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-w.queue:
			w.process(ctx, ev)
		}
	}
}

// process applies one delivery with bounded retries. After maxJobAttempts
// failures the row is left unprocessed for the reconciler sweep rather than
// retried forever, and the panic recovery sits outermost so a bad payload can
// never take the process down.
func (w *Worker) process(ctx context.Context, ev Event) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("webhook job panicked, recovered",
				"event", ev.EventType, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	for attempt := 1; attempt <= maxJobAttempts; attempt++ {
		err := w.apply(ctx, ev)
		if err == nil {
			return
		}
		w.logger.Warn("webhook job failed",
			"event", ev.EventType, "attempt", attempt, "error", err.Error())
		if attempt == maxJobAttempts {
			w.logger.Error("webhook job retries exhausted, leaving row for reconciler",
				"event", ev.EventType, "dedupe_key", ev.DedupeKey)
			return
		}
		if err := sleepBackoff(ctx, attempt, w.cfg.RetryBase, w.cfg.RetryMax); err != nil {
			return
		}
	}
}

// apply decodes the stored envelope and replays it through the dispatcher. The
// dispatcher dedupes, so a retry after a partial failure is a no-op rather than
// a double state change.
func (w *Worker) apply(ctx context.Context, ev Event) error {
	var env Envelope
	if err := json.Unmarshal(ev.Payload, &env); err != nil {
		return fmt.Errorf("webhook: decoding queued payload: %w", err)
	}
	if _, err := w.dispatch.Handle(ctx, env, ev.Payload); err != nil {
		return fmt.Errorf("webhook: handling queued delivery: %w", err)
	}
	return nil
}

// sleepBackoff waits out the retry delay and returns early on cancellation. The
// jitter shape mirrors the tiktokclient transport helper: exponential growth
// with full jitter, capped at the configured ceiling.
func sleepBackoff(ctx context.Context, attempt int, base, max time.Duration) error {
	delay := base << (attempt - 1)
	if delay > max || delay <= 0 {
		delay = max
	}
	timer := time.NewTimer(time.Duration(rand.Int63n(int64(delay) + 1)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
