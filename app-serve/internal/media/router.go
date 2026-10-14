// Package media serves upload, lookup, and delete endpoints over one Service.
//
// @file      internal/media/router.go
// @for       The media route table mounted under the API group.
// @uses      gin
// @reason    Callers mount the media endpoints without knowing the paths.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package media

import "github.com/gin-gonic/gin"

// RegisterRoutes mounts the three media endpoints on the caller's router or
// group. The caller mounts the group under /api/v1, so the paths here stay
// relative: POST /media/uploads, GET /media/:id, DELETE /media/:id.
func RegisterRoutes(r gin.IRouter, h *Handler) {
	r.POST("/media/uploads", h.Upload)
	r.GET("/media/:id", h.Show)
	r.DELETE("/media/:id", h.Delete)
}
