// Package auth_test races concurrent outage paths against the stores.
//
// @file      internal/auth/race_test.go
// @for       Concurrent Redis outage tests that guard per-call error values.
// @uses      testing, sync, miniredis/v2, internal/apierror
// @reason    Shared package level errors would race on Wrap under load.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package auth

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// TestSessionStoreGetConcurrentRedisDown races 16 readers against one closed
// Redis so the per-call error builder is exercised under -race.
func TestSessionStoreGetConcurrentRedisDown(t *testing.T) {
	store, mr := newSessionStore(t, time.Hour)
	mr.Close()

	const workers = 16
	start := make(chan struct{})
	results := make(chan error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, err := store.Get(context.Background(), "sess-abc")
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	for err := range results {
		wantCode(t, err, apierror.CodeServiceUnavailable)
	}
}

// TestStateStoreConsumeConcurrentRedisDown does the same for the OAuth state
// store, covering its own per-call error builder.
func TestStateStoreConsumeConcurrentRedisDown(t *testing.T) {
	store, mr := newStateStore(t)
	mr.Close()

	const workers = 16
	start := make(chan struct{})
	results := make(chan error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, err := store.Consume(context.Background(), "state-abc")
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	for err := range results {
		wantCode(t, err, apierror.CodeServiceUnavailable)
	}
}
