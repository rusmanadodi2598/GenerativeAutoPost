// Package webhook throttles the TikTok intake by source IP.
//
// @file      internal/webhook/throttle.go
// @for       Per-IP fixed window middleware in front of the intake.
// @uses      context, log/slog, strconv, net/http, gin, internal/apierror
// @reason    SPEC 003 section 9 names a fixed window 429 per IP, so a flood never reaches the dispatcher.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-16
package webhook

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/ratelimit"
)

// retryAfterSeconds is the hint a throttled sender backs off by. It matches
// the window, so a sender that honors it lands in the next slot.
const retryAfterSeconds = 60

// IPThrottle approves one hit for a key. It stays an interface so the intake
// test does not need Redis.
type IPThrottle interface {
	Allow(ctx context.Context, key string) (bool, error)
}

// webhookThrottlePrefix namespaces the intake window apart from every other
// throttle scope in the same Redis.
const webhookThrottlePrefix = "genauto:ratelimit:webhook:"

// ipWindow adapts the shared fixed window to the intake key shape, so the
// middleware only ever passes an IP.
type ipWindow struct {
	inner *ratelimit.Window
}

// NewIPWindow wraps the shared window as the intake throttle.
func NewIPWindow(inner *ratelimit.Window) IPThrottle {
	return ipWindow{inner: inner}
}

// Allow counts one hit for the source IP.
func (w ipWindow) Allow(ctx context.Context, ip string) (bool, error) {
	return w.inner.Allow(ctx, webhookThrottlePrefix+ip)
}

// Throttle refuses over-budget sources with a 429 before the handler runs. A
// throttle error fails open: the signature guard still protects the route, and
// a dropped delivery would only come back through the retry window.
func Throttle(throttle IPThrottle, logger *slog.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(c *gin.Context) {
		if throttle == nil {
			c.Next()
			return
		}
		ok, err := throttle.Allow(c.Request.Context(), c.ClientIP())
		if err != nil {
			logger.Warn("webhook throttle unavailable, allowing delivery", "error", err)
			c.Next()
			return
		}
		if !ok {
			logger.Warn("webhook delivery throttled", "ip", c.ClientIP())
			c.Header("Retry-After", strconv.Itoa(retryAfterSeconds))
			apierror.Write(c.Writer, c.Request,
				apierror.New(apierror.CodeRateLimited, 0, "Too many requests. Try again in a minute."))
			c.Abort()
			return
		}
		c.Next()
	}
}
