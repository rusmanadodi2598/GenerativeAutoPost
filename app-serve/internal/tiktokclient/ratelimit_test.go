// Package tiktokclient_test pins the Redis rate limiter policy.
//
// @file      internal/tiktokclient/ratelimit_test.go
// @for       Table tests for window counting, failure policy, and nil safety.
// @uses      testing, log/slog, miniredis/v2, redis/go-redis/v9, internal/apierror
// @reason    Proves budgets hold, windows expire, and Redis outages fail per family.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package tiktokclient

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// newTestLimiter starts miniredis and a limiter over it.
func newTestLimiter(t *testing.T) (*Limiter, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewLimiter(client, slog.New(slog.NewTextHandler(io.Discard, nil))), mr
}

func TestLimiterAllowsUnderTheLimit(t *testing.T) {
	tests := []struct {
		name      string
		family    Family
		limit     int64
		remaining int64
	}{
		{name: "video init first token leaves five", family: FamilyVideoInit, limit: 6, remaining: 5},
		{name: "list videos first token leaves twenty nine", family: FamilyListVideos, limit: 30, remaining: 29},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, _ := newTestLimiter(t)
			remaining, err := l.Allow(context.Background(), "acct-1", tc.family)
			if err != nil {
				t.Fatalf("Allow: %v", err)
			}
			if remaining != tc.remaining {
				t.Fatalf("remaining = %d, want %d", remaining, tc.remaining)
			}
		})
	}
}

func TestLimiterRefusesAtTheLimit(t *testing.T) {
	l, _ := newTestLimiter(t)
	ctx := context.Background()
	var remaining int64
	for i := int64(0); i < 6; i++ {
		var err error
		remaining, err = l.Allow(ctx, "acct-1", FamilyVideoInit)
		if err != nil {
			t.Fatalf("Allow %d: %v", i, err)
		}
	}
	if remaining != 0 {
		t.Fatalf("remaining at the limit = %d, want 0", remaining)
	}
	remaining, err := l.Allow(ctx, "acct-1", FamilyVideoInit)
	wantError(t, err, apierror.CodeTikTokRateLimited, "")
	if remaining != 0 {
		t.Fatalf("remaining after refusal = %d, want 0", remaining)
	}
}

func TestLimiterWindowExpires(t *testing.T) {
	l, mr := newTestLimiter(t)
	ctx := context.Background()
	for i := int64(0); i < 6; i++ {
		if _, err := l.Allow(ctx, "acct-1", FamilyVideoInit); err != nil {
			t.Fatalf("Allow %d: %v", i, err)
		}
	}
	if _, err := l.Allow(ctx, "acct-1", FamilyVideoInit); err == nil {
		t.Fatal("expected the sixth token plus one to be refused")
	}
	mr.FastForward(121 * time.Second)
	remaining, err := l.Allow(ctx, "acct-1", FamilyVideoInit)
	if err != nil {
		t.Fatalf("Allow after window: %v", err)
	}
	if remaining != 5 {
		t.Fatalf("remaining after reset = %d, want 5", remaining)
	}
}

// TestLimiterRedisUnreachable pins the fail policy per family side.
func TestLimiterRedisUnreachable(t *testing.T) {
	tests := []struct {
		name      string
		family    Family
		wantErr   apierror.Code
		remaining int64
	}{
		{name: "publish family refuses", family: FamilyVideoInit, wantErr: apierror.CodeServiceUnavailable},
		{name: "read family allows", family: FamilyListVideos, remaining: 30},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			mr.Close()
			t.Cleanup(func() { _ = client.Close() })
			l := NewLimiter(client, slog.New(slog.NewTextHandler(io.Discard, nil)))
			remaining, err := l.Allow(context.Background(), "acct-1", tc.family)
			if tc.wantErr != "" {
				wantError(t, err, tc.wantErr, "")
				return
			}
			if err != nil {
				t.Fatalf("Allow: %v", err)
			}
			if remaining != tc.remaining {
				t.Fatalf("remaining = %d, want %d", remaining, tc.remaining)
			}
		})
	}
}

// TestLimiterNilClientFailsClosedWithoutPanic proves an unconfigured limiter
// applies the same policy instead of dereferencing nil.
func TestLimiterNilClientFailsClosedWithoutPanic(t *testing.T) {
	var nilLimiter *Limiter
	if _, err := nilLimiter.Allow(context.Background(), "acct-1", FamilyStatus); err == nil {
		t.Fatal("expected refusal from a nil limiter")
	} else {
		wantError(t, err, apierror.CodeServiceUnavailable, "")
	}
	_, err := NewLimiter(nil, nil).Allow(context.Background(), "acct-1", FamilyListVideos)
	if err != nil {
		t.Fatalf("read family under a nil client = %v, want nil", err)
	}
}

// TestLimiterNilLoggerDefaultsInsteadOfFailingClosed proves a missing logger
// falls back to the default logger rather than being read as a Redis outage.
func TestLimiterNilLoggerDefaultsInsteadOfFailingClosed(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	remaining, err := NewLimiter(client, nil).Allow(context.Background(), "acct-1", FamilyVideoInit)
	if err != nil {
		t.Fatalf("publish family under a nil logger = %v, want allowed", err)
	}
	if remaining != 5 {
		t.Fatalf("remaining = %d, want 5", remaining)
	}
	_, err = NewLimiter(nil, nil).Allow(context.Background(), "acct-1", FamilyVideoInit)
	wantError(t, err, apierror.CodeServiceUnavailable, "")
	_, err = NewLimiter(nil, nil).Allow(context.Background(), "acct-1", FamilyListVideos)
	if err != nil {
		t.Fatalf("read family under a nil client = %v, want nil", err)
	}
}
