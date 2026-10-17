// Package auth issues and checks operator sessions and OAuth state.
//
// @file      internal/auth/router.go
// @for       The auth route table and its per-route middleware.
// @uses      time, github.com/gin-gonic/gin
// @reason    Callers mount the auth endpoints without knowing the guard layout.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package auth

import (
	"time"

	"github.com/gin-gonic/gin"
)

// RouterConfig carries the cookie settings and browser base the auth routes
// apply, plus the shared throttle mounted in front of the Google routes.
type RouterConfig struct {
	CookieName   string
	CookieTTL    time.Duration
	AppUIBaseURL string
	Throttle     gin.HandlerFunc
}

// RegisterRoutes mounts the six auth endpoints on the caller's router or
// group. The middleware is attached per route as variadic handlers because
// the passed group is shared with unguarded routes. The browser facing
// authorize and callback routes carry a session but redirect a missing one
// to the result page instead of a JSON 401; refresh, revoke, and logout
// require a session and a matching CSRF token; the session read requires a
// session only.
func RegisterRoutes(r gin.IRouter, svc *Service, sessions *SessionStore, cfg RouterConfig) {
	handler := NewHandler(svc, HandlerConfig{
		CookieName:   cfg.CookieName,
		CookieTTL:    cfg.CookieTTL,
		AppUIBaseURL: cfg.AppUIBaseURL,
	})
	requireSession := RequireSession(sessions, cfg.CookieName)
	requireSessionRedirect := RequireSessionRedirect(sessions, cfg.CookieName, cfg.AppUIBaseURL)

	r.GET("/auth/tiktok/authorize", requireSessionRedirect, handler.Authorize)
	r.GET("/auth/tiktok/callback", requireSessionRedirect, handler.Callback)
	r.POST("/auth/tiktok/refresh", requireSession, RequireCSRF(), handler.Refresh)
	r.POST("/auth/tiktok/revoke", requireSession, RequireCSRF(), handler.Revoke)
	r.POST("/auth/logout", requireSession, RequireCSRF(), handler.Logout)
	r.GET("/auth/session", requireSession, handler.Session)
}

// RegisterGoogleRoutes mounts the two Google browser routes. The optional
// throttle runs first, so an over-budget source never reaches the handler.
func RegisterGoogleRoutes(r gin.IRouter, google *GoogleService, cfg RouterConfig) {
	handler := NewGoogleHandler(google, HandlerConfig{
		CookieName:   cfg.CookieName,
		CookieTTL:    cfg.CookieTTL,
		AppUIBaseURL: cfg.AppUIBaseURL,
	})
	mount := func(path string, h gin.HandlerFunc) {
		if cfg.Throttle != nil {
			r.GET(path, cfg.Throttle, h)
			return
		}
		r.GET(path, h)
	}
	mount("/auth/google/authorize", handler.Authorize)
	mount("/auth/google/callback", handler.Callback)
}
