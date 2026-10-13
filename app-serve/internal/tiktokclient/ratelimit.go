// Package tiktokclient enforces TikTok's per-account request budgets in Redis.
//
// @file      internal/tiktokclient/ratelimit.go
// @for       Token bucket per account per endpoint family in Redis so the limit holds across replicas.
// @uses      github.com/redis/go-redis/v9, log/slog, internal/platform/redisx, internal/apierror
// @reason    Per account budgets must hold across replicas; when Redis is unreachable the publish-side families fail closed and the read-side families fail open with a log.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package tiktokclient

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/redisx"
)

// Family identifies a rate-limited endpoint group.
type Family string

// Rate-limited endpoint families.
const (
	FamilyVideoInit   Family = "video_init"
	FamilyInboxInit   Family = "inbox_init"
	FamilyCreatorInfo Family = "creator_info"
	FamilyStatus      Family = "status"
	FamilyListVideos  Family = "list_videos"
)

// familyLimits holds the per-minute budget for each family (002 sections 3, 4.1, 4.3, 4.5, 7).
var familyLimits = map[Family]int64{
	FamilyVideoInit:   6,
	FamilyInboxInit:   6,
	FamilyCreatorInfo: 20,
	FamilyStatus:      30,
	FamilyListVideos:  30,
}

// windowSeconds is the fixed-window lifetime; two minutes so a window survives its minute.
const windowSeconds = 120

// incrWindow increments the window counter and arms its expiry on the first hit.
// One Lua script keeps the increment and expiry in a single atomic round trip.
var incrWindow = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
	redis.call('EXPIRE', KEYS[1], ` + strconv.Itoa(windowSeconds) + `)
end
return count
`)

// Limiter enforces TikTok's per-account request budgets.
type Limiter struct {
	client *redis.Client
	logger *slog.Logger
}

// NewLimiter builds a limiter over the shared Redis client. A nil logger
// defaults to slog.Default so a wiring mistake is not read as a Redis outage.
func NewLimiter(client *redis.Client, logger *slog.Logger) *Limiter {
	if logger == nil {
		logger = slog.Default()
	}
	return &Limiter{client: client, logger: logger}
}

// Allow consumes one token for account+family. It returns the remaining
// tokens and a typed error when the budget is spent or the policy refuses.
func (l *Limiter) Allow(ctx context.Context, accountID string, family Family) (int64, error) {
	limit, ok := familyLimits[family]
	if !ok {
		return 0, apierror.New(apierror.CodeTikTokInvalidParam, 0, "Unknown rate limit family.")
	}
	if l == nil || l.client == nil {
		return l.unavailable(family, limit, nil)
	}
	count, err := incrWindow.Run(ctx, l.client, []string{windowKey(family, accountID)}).Int64()
	if err != nil {
		return l.unavailable(family, limit, err)
	}
	if count > limit {
		return 0, apierror.New(apierror.CodeTikTokRateLimited, 0, "Too many requests. Try again shortly.")
	}
	remaining := limit - count
	if remaining < 0 {
		remaining = 0
	}
	return remaining, nil
}

// windowKey names the fixed window for one account and family in the current minute.
func windowKey(family Family, accountID string) string {
	return redisx.KeyRateLimit + string(family) + ":" + accountID + ":" +
		strconv.FormatInt(time.Now().Unix()/60, 10)
}

// unavailable applies the fail policy when Redis cannot answer. Publish-side
// families refuse; the read-side family allows so a listing stays available.
func (l *Limiter) unavailable(family Family, limit int64, cause error) (int64, error) {
	var logger *slog.Logger
	if l != nil {
		logger = l.logger
	}
	if family == FamilyListVideos {
		if logger != nil {
			logger.Warn("rate limiter unavailable, allowing read", "family", string(family), "error", cause)
		}
		return limit, nil
	}
	if logger != nil {
		logger.Error("rate limiter unavailable, refusing request", "family", string(family), "error", cause)
	}
	return 0, apierror.New(apierror.CodeServiceUnavailable, 0, "Rate limit check is unavailable. Try again shortly.")
}
