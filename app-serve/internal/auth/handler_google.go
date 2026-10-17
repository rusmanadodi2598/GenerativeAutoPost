// Package auth issues and checks operator sessions and OAuth state.
//
// @file      internal/auth/handler_google.go
// @for       The browser facing Google sign in authorize and callback handlers.
// @uses      net/http, github.com/gin-gonic/gin
// @reason    Google is the only sign in path; every branch is a redirect.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-17
package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// GoogleHandler serves the Google sign in browser routes.
type GoogleHandler struct {
	svc *GoogleService
	cfg HandlerConfig
}

// NewGoogleHandler wires the Google handlers to their service and cookie config.
func NewGoogleHandler(svc *GoogleService, cfg HandlerConfig) *GoogleHandler {
	return &GoogleHandler{svc: svc, cfg: cfg}
}

// Authorize redirects the browser to the Google consent page. A failure to
// build the URL returns to the result page rather than writing a body.
func (h *GoogleHandler) Authorize(c *gin.Context) {
	url, err := h.svc.AuthorizeURL(c.Request.Context())
	if err != nil {
		c.Redirect(http.StatusFound, browserResultURL(h.cfg.AppUIBaseURL, "error"))
		return
	}
	c.Redirect(http.StatusFound, url)
}

// Callback completes the sign in, sets the session cookie, and returns the
// browser to the console. Every branch is a redirect: this route never writes
// a JSON body, and the upstream error text stays out of the Location header.
func (h *GoogleHandler) Callback(c *gin.Context) {
	if c.Query("error") != "" || c.Query("code") == "" || c.Query("state") == "" {
		c.Redirect(http.StatusFound, browserResultURL(h.cfg.AppUIBaseURL, "error"))
		return
	}
	sess, err := h.svc.Complete(c.Request.Context(), c.Query("code"), c.Query("state"))
	if err != nil {
		c.Redirect(http.StatusFound, browserResultURL(h.cfg.AppUIBaseURL, "error"))
		return
	}
	writeSessionCookie(c, h.cfg, sess.ID)
	c.Redirect(http.StatusFound, consoleRootURL(h.cfg.AppUIBaseURL))
}

// consoleRootURL builds the console landing URL. An empty base URL yields the
// relative root, so a same origin deployment works.
func consoleRootURL(baseURL string) string {
	if baseURL == "" {
		return "/"
	}
	return baseURL + "/"
}
