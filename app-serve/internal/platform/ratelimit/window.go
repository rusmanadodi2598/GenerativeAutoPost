// Package ratelimit enforces inbound request budgets shared across replicas.
//
// @file      internal/platform/ratelimit/window.go
// @for       Fixed window counter over Redis for inbound request throttles.
// @uses      context, strconv, time, github.com/redis/go-redis/v9, log/slog
// @reason    An in-process counter would hand every replica its own budget, so the window has to live in Redis.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-16
package ratelimit

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Window counts hits per key inside one fixed window.
type Window struct {
	client *redis.Client
	limit  int64
	window time.Duration
	logger *slog.Logger
}

// NewWindow builds a fixed window throttle. A non-positive limit or window
// leaves the throttle open, so a missing setting cannot silently drop traffic.
func NewWindow(client *redis.Client, limit int64, window time.Duration, logger *slog.Logger) *Window {
	if logger == nil {
		logger = slog.Default()
	}
	return &Window{client: client, limit: limit, window: window, logger: logger}
}

// incrWindow increments the window counter and arms its expiry on the first
// hit. One Lua script keeps the increment and the expiry in one atomic round
// trip, so two replicas cannot both arm the window.
var incrWindow = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
	redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return count
`)

// Allow consumes one hit for key and reports whether it is inside the budget.
// Redis is the only shared state, so the count holds across replicas. When
// Redis cannot answer the call fails open: the caller's own authentication is
// the real guard, and a failed throttle must not become an outage.
func (w *Window) Allow(ctx context.Context, key string) (bool, error) {
	if w == nil || w.client == nil || w.limit <= 0 || w.window <= 0 {
		return true, nil
	}
	count, err := incrWindow.Run(ctx, w.client, []string{windowKey(key, w.window)}, w.window.Milliseconds()).Int64()
	if err != nil {
		w.logger.Warn("rate window unavailable, allowing request", "key", key, "error", err)
		return true, nil
	}
	return count <= w.limit, nil
}

// windowKey appends the current window slot so each slot counts on its own.
func windowKey(key string, window time.Duration) string {
	return key + ":" + strconv.FormatInt(time.Now().UnixNano()/window.Nanoseconds(), 10)
}
