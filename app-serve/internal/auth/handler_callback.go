// Package auth issues and checks operator sessions and OAuth state.
//
// @file      internal/auth/handler_callback.go
// @for       The browser facing TikTok OAuth callback handler.
// @uses      net/http, github.com/gin-gonic/gin
// @reason    The callback must always redirect and never leak upstream text.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Callback completes the sign in, sets the session cookie, and returns the
// browser to the app. Every branch is a redirect: this route never writes a
// JSON body, and the upstream error text stays out of the Location header.
func (h *Handler) Callback(c *gin.Context) {
	if c.Query("error") != "" || c.Query("code") == "" || c.Query("state") == "" {
		c.Redirect(http.StatusFound, h.svc.CallbackURL("error"))
		return
	}
	sess, err := h.svc.CompleteCallback(c.Request.Context(), c.Query("code"), c.Query("state"))
	if err != nil {
		c.Redirect(http.StatusFound, h.svc.CallbackURL("error"))
		return
	}
	h.setSessionCookie(c, sess.ID)
	c.Redirect(http.StatusFound, h.svc.CallbackURL("ok"))
}
