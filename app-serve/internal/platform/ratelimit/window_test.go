// Package ratelimit enforces inbound request budgets shared across replicas.
//
// @file      internal/platform/ratelimit/window_test.go
// @for       Tests for the fixed window counter.
// @uses      testing, context, time, miniredis, redis/go-redis/v9
// @reason    A local counter would let every replica hand out its own budget, so the window must live in Redis.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-16
package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestWindow builds a window over an in-memory Redis.
func newTestWindow(t *testing.T, limit int64) (*Window, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewWindow(client, limit, time.Minute, nil), server
}

// TestWindowAllowsUpToTheLimitThenDenies proves the budget is spent exactly once
// per key, so an over-limit caller gets the 429 the contract promises.
func TestWindowAllowsUpToTheLimitThenDenies(t *testing.T) {
	w, _ := newTestWindow(t, 2)
	ctx := context.Background()

	for hit := 1; hit <= 2; hit++ {
		ok, err := w.Allow(ctx, "203.0.113.7")
		if err != nil {
			t.Fatalf("hit %d: Allow returned %v", hit, err)
		}
		if !ok {
			t.Fatalf("hit %d inside the budget was denied", hit)
		}
	}
	ok, err := w.Allow(ctx, "203.0.113.7")
	if err != nil {
		t.Fatalf("over budget Allow returned %v", err)
	}
	if ok {
		t.Fatal("the third hit is over the budget and must be denied")
	}
}

// TestWindowKeepsBudgetsPerKey proves one noisy source cannot spend the budget
// of another, since TikTok fans out from several egress IPs.
func TestWindowKeepsBudgetsPerKey(t *testing.T) {
	w, _ := newTestWindow(t, 1)
	ctx := context.Background()

	if ok, _ := w.Allow(ctx, "203.0.113.7"); !ok {
		t.Fatal("the first hit must be allowed")
	}
	if ok, _ := w.Allow(ctx, "203.0.113.8"); !ok {
		t.Fatal("a different key must have its own budget")
	}
	if ok, _ := w.Allow(ctx, "203.0.113.7"); ok {
		t.Fatal("the spent key must stay denied")
	}
}

// TestWindowFailsOpenWhenRedisIsUnreachable proves a Redis outage degrades the
// throttle instead of dropping every delivery: the HMAC check still guards the
// route, and TikTok only retries for 72 hours.
func TestWindowFailsOpenWhenRedisIsUnreachable(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	w := NewWindow(client, 1, time.Minute, nil)

	ok, err := w.Allow(context.Background(), "203.0.113.7")
	if err != nil {
		t.Fatalf("Allow must fail open without an error, got %v", err)
	}
	if !ok {
		t.Fatal("an unreachable Redis must not deny deliveries")
	}
}
