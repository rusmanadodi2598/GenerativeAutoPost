// Package auth issues and checks operator sessions and OAuth state.
//
// @file      internal/auth/middleware.go
// @for       Gin middleware that loads a session and checks the CSRF token.
// @uses      context, crypto/sha256, crypto/subtle, errors, net/http, strconv, log/slog, gin, internal/apierror
// @reason    Protected routes need a cookie session plus a same origin check.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// sessionContextKey is the gin context slot holding the loaded session.
const sessionContextKey = "auth.session"

// csrfHeader carries the double submit token on state changing requests.
const csrfHeader = "X-CSRF-Token"

// authThrottlePrefix namespaces the auth rate limit apart from every other
// throttle scope in the same Redis.
const authThrottlePrefix = "genauto:ratelimit:auth:"

// retryAfterSeconds is the hint a throttled browser backs off by.
const retryAfterSeconds = 60

// browserResultURL builds the console result page URL for a status. An empty
// base URL yields the relative path, so a same origin deployment works.
func browserResultURL(baseURL, status string) string {
	path := "/auth/result?status=" + status
	if baseURL == "" {
		return path
	}
	return baseURL + path
}

// RequireSession loads the cookie session or aborts with a typed error.
func RequireSession(store *SessionStore, cookieName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := c.Cookie(cookieName)
		if err != nil || raw == "" {
			apierror.Write(c.Writer, c.Request, apierror.New(apierror.CodeUnauthorized, 0, "Sign in to continue."))
			c.Abort()
			return
		}
		sess, err := store.Get(c.Request.Context(), raw)
		if err != nil {
			writeSessionError(c, err)
			c.Abort()
			return
		}
		c.Set(sessionContextKey, sess)
		c.Next()
	}
}

// RequireSessionRedirect is RequireSession for browser navigated routes: a
// missing or unreadable session redirects to the result page instead of
// writing a JSON 401. A valid session is still attached for the handler.
func RequireSessionRedirect(store *SessionStore, cookieName string, baseURL string) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := c.Cookie(cookieName)
		if err != nil || raw == "" {
			c.Redirect(http.StatusFound, browserResultURL(baseURL, "error"))
			c.Abort()
			return
		}
		sess, err := store.Get(c.Request.Context(), raw)
		if err != nil {
			c.Redirect(http.StatusFound, browserResultURL(baseURL, "error"))
			c.Abort()
			return
		}
		c.Set(sessionContextKey, sess)
		c.Next()
	}
}

// IPLimiter is the per-IP window the auth browser routes share.
type IPLimiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

// Throttle refuses over-budget sources with a 429 before the handler runs. A
// limiter error fails open: a throttle outage must not lock every operator
// out, and the allowlist check still guards sign in.
func Throttle(limiter IPLimiter, logger *slog.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(c *gin.Context) {
		if limiter == nil {
			c.Next()
			return
		}
		ok, err := limiter.Allow(c.Request.Context(), authThrottlePrefix+c.ClientIP())
		if err != nil {
			logger.WarnContext(c.Request.Context(), "auth throttle unavailable, allowing request", "error", err)
			c.Next()
			return
		}
		if !ok {
			logger.WarnContext(c.Request.Context(), "auth route throttled", "ip", c.ClientIP())
			c.Header("Retry-After", strconv.Itoa(retryAfterSeconds))
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}
		c.Next()
	}
}

// RequireCSRF compares the header token against the session token in constant
// time, aborting when the session is absent or the tokens differ.
func RequireCSRF() gin.HandlerFunc {
	return func(c *gin.Context) {
		sess, ok := SessionFrom(c)
		if !ok {
			writeCSRFError(c)
			c.Abort()
			return
		}
		presented := c.GetHeader(csrfHeader)
		if presented == "" || !constantTimeEqual(presented, sess.CSRFToken) {
			writeCSRFError(c)
			c.Abort()
			return
		}
		c.Next()
	}
}

// SessionFrom reads the session attached by RequireSession.
func SessionFrom(c *gin.Context) (*Session, bool) {
	value, ok := c.Get(sessionContextKey)
	if !ok {
		return nil, false
	}
	sess, ok := value.(*Session)
	return sess, ok
}

// writeSessionError forwards the store's typed error unchanged. The fallback
// only runs when the store violates its contract and returns a bare error.
func writeSessionError(c *gin.Context, err error) {
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		apiErr = errSessionUnavailable(err)
	}
	apierror.Write(c.Writer, c.Request, apiErr)
}

// writeCSRFError answers a failed same origin check.
func writeCSRFError(c *gin.Context) {
	apierror.Write(c.Writer, c.Request, apierror.New(apierror.CodeForbidden, 0, "The request could not be verified."))
}

// constantTimeEqual compares two tokens by their SHA-256 digests, so length
// differences do not leak through the comparison itself.
func constantTimeEqual(a, b string) bool {
	left := sha256.Sum256([]byte(a))
	right := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(left[:], right[:]) == 1
}
