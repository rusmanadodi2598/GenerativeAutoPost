// Package webhook proves the worker pool, reconciler, and outbox relay behave.
//
// @file      internal/webhook/worker_test.go
// @for       Unit tests for the pool lifecycle, backoff, and queue backpressure.
// @uses      testing, context, errors, log/slog, sync, sync/atomic, time
// @reason    Shutdown ordering, panic recovery, and the retry cap are the risks.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     worker
// @stability stable
// @since     2026-10-12
package webhook

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubHandler replays canned outcomes and can panic on demand.
type stubHandler struct {
	calls atomic.Int64
	fail  atomic.Int32
	panic bool
}

func (s *stubHandler) Handle(_ context.Context, _ Envelope, _ []byte) (Outcome, error) {
	s.calls.Add(1)
	if s.panic {
		panic("stub dispatch panic")
	}
	if s.fail.Load() > 0 {
		return OutcomeStored, errors.New("stub failure")
	}
	return OutcomeApplied, nil
}

// blockingHandler holds one job open until released, so a test can cancel the
// pool around it and prove Run waits for in flight work.
type blockingHandler struct {
	started  chan struct{}
	release  chan struct{}
	finished chan struct{}
}

// Handle blocks the worker until release is closed, then signals completion.
func (b *blockingHandler) Handle(_ context.Context, _ Envelope, _ []byte) (Outcome, error) {
	close(b.started)
	<-b.release
	b.finished <- struct{}{}
	return OutcomeApplied, nil
}

// safeBuffer serializes log writes for race clean assertions.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write satisfies io.Writer under a mutex.
func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

// String snapshots the log content under a mutex.
func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// newTestWorker builds a pool with tiny timings so backoff stays fast.
func newTestWorker(t *testing.T, h eventHandler) (*Worker, *safeBuffer) {
	t.Helper()
	buf := &safeBuffer{}
	w := NewWorker(nil, WorkerConfig{
		Size: 2, QueueDepth: 4, RetryBase: time.Millisecond, RetryMax: 2 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(buf, nil)))
	w.dispatch = h
	return w, buf
}

// queuedEvent builds a payload the pool can decode.
func queuedEvent(name string) Event {
	return Event{
		DedupeKey: "k:" + name, EventType: name,
		Payload: []byte(`{"event":"` + name + `","create_time":1}`),
	}
}

// runPool starts the pool and returns a func that cancels and waits for exit.
func runPool(t *testing.T, w *Worker) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("worker pool did not stop after cancellation")
		}
	}
}

// waitFor polls until cond is true or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !cond() {
		time.Sleep(time.Millisecond)
	}
	if !cond() {
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestWorker_EnqueueGivenFullQueue_ThenOverflowReportsFalse(t *testing.T) {
	w, _ := newTestWorker(t, &stubHandler{})
	for range w.cfg.QueueDepth {
		if !w.Enqueue(context.Background(), queuedEvent("fill")) {
			t.Fatal("queue should still have room before the depth limit")
		}
	}
	if w.Enqueue(context.Background(), queuedEvent("overflow")) {
		t.Fatal("enqueue should report false once the queue is full")
	}
	if code := QueueFullError().Code; code != "SERVICE_UNAVAILABLE" {
		t.Fatalf("queue full should map to 503, got %s", code)
	}
}

func TestWorker_GivenPanickingJob_ThenPoolRecoversAndKeepsRunning(t *testing.T) {
	w, buf := newTestWorker(t, &stubHandler{panic: true})
	stop := runPool(t, w)
	defer stop()

	if !w.Enqueue(context.Background(), queuedEvent("boom")) {
		t.Fatal("enqueue should be accepted on an empty queue")
	}
	waitFor(t, "panic value in the log", func() bool {
		return strings.Contains(buf.String(), "stub dispatch panic")
	})
	if !w.Enqueue(context.Background(), queuedEvent("after")) {
		t.Fatal("pool should still accept work after a recovered panic")
	}
}

func TestWorker_GivenCancellationMidJob_ThenRunWaitsForInflightJob(t *testing.T) {
	h := &blockingHandler{started: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{}, 1)}
	w := NewWorker(nil, WorkerConfig{Size: 1, QueueDepth: 2, RetryBase: time.Millisecond}, nil)
	w.dispatch = h
	stop := runPool(t, w)

	if !w.Enqueue(context.Background(), queuedEvent("slow")) {
		t.Fatal("enqueue should be accepted")
	}
	waitFor(t, "job start", func() bool {
		select {
		case <-h.started:
			return true
		default:
			return false
		}
	})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		stop()
	}()
	select {
	case <-h.finished:
		t.Fatal("job should still be blocked while the pool drains")
	case <-time.After(50 * time.Millisecond):
	}
	close(h.release)
	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pool should exit once the in flight job completes")
	}
}

func TestWorker_GivenAlwaysFailingJob_ThenRetriesStopAtTheCap(t *testing.T) {
	h := &stubHandler{}
	h.fail.Store(1)
	w, _ := newTestWorker(t, h)
	stop := runPool(t, w)
	defer stop()

	if !w.Enqueue(context.Background(), queuedEvent("fail")) {
		t.Fatal("enqueue should be accepted")
	}
	waitFor(t, "retry cap", func() bool { return h.calls.Load() == maxJobAttempts })
	deadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if got := h.calls.Load(); got != maxJobAttempts {
		t.Fatalf("expected exactly %d attempts, got %d", maxJobAttempts, got)
	}
}
