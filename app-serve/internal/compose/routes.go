// Package compose wires every module into one HTTP surface.
//
// @file      internal/compose/routes.go
// @for       Mounting the module route tables under the api group.
// @uses      gin
// @reason    One function owns the /api/v1 prefix so main stays wiring only.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     router
// @stability stable
// @since     2026-10-15
package compose

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// MountTable registers one module inside the caller's group.
type MountTable func(gin.IRouter)

// Routes carries one mount function per module.
type Routes struct {
	Auth     MountTable
	Accounts MountTable
	Media    MountTable
	Publish  MountTable
	Insight  MountTable
	Webhook  MountTable
}

// Mount mounts the health probe, the webhook at its root path, and the
// console modules under /api/v1. The webhook is last and outside the versioned
// group on purpose: it is TikTok's contract, not ours.
func Mount(engine *gin.Engine, routes Routes) {
	engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	api := engine.Group("/api/v1")
	if routes.Auth != nil {
		routes.Auth(api)
	}
	if routes.Accounts != nil {
		routes.Accounts(api)
	}
	if routes.Media != nil {
		routes.Media(api)
	}
	if routes.Publish != nil {
		routes.Publish(api)
	}
	if routes.Insight != nil {
		routes.Insight(api)
	}
	if routes.Webhook != nil {
		routes.Webhook(engine)
	}
}
