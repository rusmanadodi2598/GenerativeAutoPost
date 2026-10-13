// Package auth issues and checks operator sessions and OAuth state.
//
// @file      internal/auth/middleware.go
// @for       Gin middleware that loads a session and checks the CSRF token.
// @uses      crypto/sha256, crypto/subtle, github.com/gin-gonic/gin, internal/apierror
// @reason    Protected routes need a cookie session plus a same origin check.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"

	"github.com/gin-gonic/gin"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// sessionContextKey is the gin context slot holding the loaded session.
const sessionContextKey = "auth.session"

// csrfHeader carries the double submit token on state changing requests.
const csrfHeader = "X-CSRF-Token"

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
