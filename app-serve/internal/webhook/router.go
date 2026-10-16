// Package webhook mounts the TikTok intake outside the console API.
//
// @file      internal/webhook/router.go
// @for       Route registration for the webhook intake.
// @uses      gin
// @reason    Keeps the caller unaware of the plan mandated path.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     router
// @stability stable
// @since     2026-10-12
package webhook

import "github.com/gin-gonic/gin"

// RegisterRoutes mounts the intake at the plan mandated path on the caller's
// root router, outside /api/v1 and its middleware: no session, no CSRF, because
// the caller is TikTok and not a browser. The per-IP throttle is the only
// guard the caller adds here, and it is authenticated after the limit so a
// flood cannot reach the HMAC check.
func RegisterRoutes(r gin.IRouter, h *Handler, throttle gin.HandlerFunc) {
	r.POST("/webhooks/tiktok", throttle, h.Receive)
}
