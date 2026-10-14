// Package media serves upload, lookup, and delete endpoints over one Service.
//
// @file      internal/media/router.go
// @for       The media route table plus the session guard guarding every route.
// @uses      gin, internal/apierror
// @reason    Callers mount the media endpoints without knowing the paths.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package media

import (
	"github.com/gin-gonic/gin"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// accountContextKey is the gin context slot holding the account id resolved by
// requireSession. The unexported struct key keeps callers in other packages
// from guessing the slot name and forging a value.
type accountContextKey struct{}

// requireSession answers 401 when no session is attached to the request.
// On success it stashes the resolved account id for the handler downstream,
// so a handler never has to resolve the session a second time.
func (h *Handler) requireSession(c *gin.Context) {
	accountID, ok := h.session(c)
	if !ok || accountID == "" {
		apierror.Write(c.Writer, c.Request,
			apierror.New(apierror.CodeUnauthorized, 0, "Sign in to continue."))
		c.Abort()
		return
	}
	c.Set(accountContextKey{}, accountID)
	c.Next()
}

// accountFrom reads the account id stashed by requireSession.
func accountFrom(c *gin.Context) (string, bool) {
	value, ok := c.Get(accountContextKey{})
	if !ok {
		return "", false
	}
	accountID, ok := value.(string)
	return accountID, ok && accountID != ""
}

// RegisterRoutes mounts the three media endpoints on the caller's router or
// group. The caller mounts the group under /api/v1, so the paths here stay
// relative: POST /media/uploads, GET /media/:id, DELETE /media/:id.
func RegisterRoutes(r gin.IRouter, h *Handler) {
	r.POST("/media/uploads", h.requireSession, h.Upload)
	r.GET("/media/:id", h.requireSession, h.Show)
	r.DELETE("/media/:id", h.requireSession, h.Delete)
}
