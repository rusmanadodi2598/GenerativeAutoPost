// Package httpapi builds the Gin engine for the data plane.
//
// @file      internal/platform/httpapi/httpapi.go
// @for       Gin engine with request id, slog access log, and recovery.
// @uses      github.com/gin-gonic/gin, log/slog
// @reason    One place owns HTTP middleware on the data plane engine.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Server wraps the Gin engine with its logger.
type Server struct {
	engine *gin.Engine
	logger *slog.Logger
}

// New builds the engine in release mode with middleware. The health route is
// not registered here: the composition root mounts it with the module tables,
// so one place owns the route layout.
func New(logger *slog.Logger) *Server {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	s := &Server{engine: engine, logger: logger}
	engine.Use(s.requestID(), s.accessLog(), s.recovery())
	return s
}

// Engine exposes the middleware-wired engine so the composition root can mount
// the module route tables on it.
func (s *Server) Engine() *gin.Engine { return s.engine }

// Handler exposes the engine as an http.Handler for serving and tests.
func (s *Server) Handler() http.Handler {
	return s.engine
}

// requestID assigns a random hex id per request for log correlation.
func (s *Server) requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		var buf [16]byte
		if _, err := rand.Read(buf[:]); err != nil {
			c.Set("request_id", "unknown")
		} else {
			c.Set("request_id", hex.EncodeToString(buf[:]))
		}
		c.Header("X-Request-ID", c.GetString("request_id"))
		c.Next()
	}
}

// accessLog writes one structured line per request with latency and status.
func (s *Server) accessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		s.logger.Info("http request",
			"request_id", c.GetString("request_id"),
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
		)
	}
}

// recovery converts a handler panic into a 500 instead of crashing.
func (s *Server) recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("http panic recovered",
					"request_id", c.GetString("request_id"),
					"panic", rec,
					"path", c.Request.URL.Path,
				)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"status": "error",
				})
			}
		}()
		c.Next()
	}
}
