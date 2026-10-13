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

// RouterConfig carries the cookie settings the auth routes apply.
type RouterConfig struct {
	CookieName string
	CookieTTL  time.Duration
}

// RegisterRoutes mounts the six auth endpoints on the caller's router or
// group. The middleware is attached per route as variadic handlers because
// the passed group is shared with unguarded routes. The browser facing
// authorize and callback routes skip session middleware entirely; refresh,
// revoke, and logout require a session and a matching CSRF token; the session
// read requires a session only.
func RegisterRoutes(r gin.IRouter, svc *Service, sessions *SessionStore, cfg RouterConfig) {
	handler := NewHandler(svc, HandlerConfig(cfg))
	requireSession := RequireSession(sessions, cfg.CookieName)

	r.GET("/auth/tiktok/authorize", handler.Authorize)
	r.GET("/auth/tiktok/callback", handler.Callback)
	r.POST("/auth/tiktok/refresh", requireSession, RequireCSRF(), handler.Refresh)
	r.POST("/auth/tiktok/revoke", requireSession, RequireCSRF(), handler.Revoke)
	r.POST("/auth/logout", requireSession, RequireCSRF(), handler.Logout)
	r.GET("/auth/session", requireSession, handler.Session)
}
