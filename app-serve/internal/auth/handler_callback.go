// Package auth issues and checks operator sessions and OAuth state.
//
// @file      internal/auth/handler_callback.go
// @for       The browser facing TikTok OAuth authorize and callback handlers.
// @uses      net/http, github.com/gin-gonic/gin
// @reason    The TikTok browser routes must always redirect and never leak upstream text.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Authorize starts the TikTok sign in with an optional scope override. A
// missing session means a wiring fault, so it redirects instead of writing JSON.
func (h *Handler) Authorize(c *gin.Context) {
	sess, ok := SessionFrom(c)
	if !ok {
		c.Redirect(http.StatusFound, h.svc.CallbackURL("error"))
		return
	}
	url, err := h.svc.AuthorizeURL(c.Request.Context(), sess.OperatorID, c.Query("scope"))
	if err != nil {
		c.Redirect(http.StatusFound, h.svc.CallbackURL("error"))
		return
	}
	c.Redirect(http.StatusFound, url)
}

// Callback connects the TikTok account of the signed in operator and returns
// the browser to the app. Every branch is a redirect: this route never writes
// a JSON body, and the upstream error text stays out of the Location header.
// The session cookie is set once by the Google sign in, not here.
func (h *Handler) Callback(c *gin.Context) {
	if c.Query("error") != "" || c.Query("code") == "" || c.Query("state") == "" {
		c.Redirect(http.StatusFound, h.svc.CallbackURL("error"))
		return
	}
	sess, ok := SessionFrom(c)
	if !ok {
		c.Redirect(http.StatusFound, h.svc.CallbackURL("error"))
		return
	}
	if err := h.svc.CompleteCallback(c.Request.Context(), sess.OperatorID, c.Query("code"), c.Query("state")); err != nil {
		c.Redirect(http.StatusFound, h.svc.CallbackURL("error"))
		return
	}
	c.Redirect(http.StatusFound, h.svc.CallbackURL("ok"))
}
