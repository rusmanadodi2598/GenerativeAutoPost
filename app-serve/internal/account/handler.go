// Package account holds the TikTok account aggregate and its persistence shapes.
//
// @file      internal/account/handler.go
// @for       Gin handlers for the account list, show, and delete routes.
// @uses      net/http, errors, time, gin, internal/apierror
// @reason    Keeps request decoding, session resolution, and envelopes out of the service.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-16
package account

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// SessionAccount resolves the operator id for one request. A false result
// means no session is attached, so the guard answers 401.
type SessionAccount func(*gin.Context) (string, bool)

// operatorContextKey is the gin slot holding the operator id resolved by
// requireSession. The unexported struct key keeps other packages from forging a
// value into the slot.
type operatorContextKey struct{}

// successEnvelope is the shared success shape: typed data plus request meta.
// The reason for the untyped member is the JSON boundary: every handler passes
// a concrete view type, so nothing unvalidated reaches the wire.
type successEnvelope struct {
	Data any          `json:"data"`
	Meta responseMeta `json:"meta"`
}

// responseMeta carries the request id and the server clock.
type responseMeta struct {
	RequestID  string `json:"request_id"`
	ServerTime string `json:"server_time"`
}

// Handler serves the account HTTP surface.
type Handler struct {
	svc     *Service
	session SessionAccount
	csrf    gin.HandlerFunc
}

// NewHandler wires the account handlers to the service, the session resolver,
// and the CSRF guard the destructive route runs behind.
func NewHandler(svc *Service, session SessionAccount, csrf gin.HandlerFunc) *Handler {
	return &Handler{svc: svc, session: session, csrf: csrf}
}

// requireSession answers 401 when no operator session is attached. On success
// it stashes the operator id so no handler resolves the session twice.
func (h *Handler) requireSession(c *gin.Context) {
	operatorID, ok := h.session(c)
	if !ok || operatorID == "" {
		apierror.Write(c.Writer, c.Request,
			apierror.New(apierror.CodeUnauthorized, 0, "Sign in to continue."))
		c.Abort()
		return
	}
	c.Set(operatorContextKey{}, operatorID)
	c.Next()
}

// operatorFrom reads the operator id stashed by requireSession. A missing
// value answers 401, so a wiring mistake denies instead of panicking.
func operatorFrom(c *gin.Context) (string, bool) {
	value, ok := c.Get(operatorContextKey{})
	if !ok {
		apierror.Write(c.Writer, c.Request,
			apierror.New(apierror.CodeUnauthorized, 0, "Sign in to continue."))
		return "", false
	}
	operatorID, ok := value.(string)
	if !ok || operatorID == "" {
		apierror.Write(c.Writer, c.Request,
			apierror.New(apierror.CodeUnauthorized, 0, "Sign in to continue."))
		return "", false
	}
	return operatorID, true
}

// List answers the operator's accounts, token material excluded.
func (h *Handler) List(c *gin.Context) {
	operatorID, ok := operatorFrom(c)
	if !ok {
		return
	}
	views, err := h.svc.List(c.Request.Context(), operatorID)
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeSuccess(c, http.StatusOK, views)
}

// Show answers one account after the ownership check in the service.
func (h *Handler) Show(c *gin.Context) {
	operatorID, ok := operatorFrom(c)
	if !ok {
		return
	}
	acct, err := h.svc.GetOwned(c.Request.Context(), operatorID, c.Param("account_id"))
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeSuccess(c, http.StatusOK, acct.View())
}

// Delete clears the account credentials and marks it revoked. The row stays so
// publish attempts and stats keep their audit trail.
func (h *Handler) Delete(c *gin.Context) {
	operatorID, ok := operatorFrom(c)
	if !ok {
		return
	}
	if err := h.svc.Forget(c.Request.Context(), operatorID, c.Param("account_id")); err != nil {
		writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// writeSuccess writes the success envelope with fresh request metadata. The
// reason for the untyped data is the encode boundary: every caller passes a
// view built by this package.
func (h *Handler) writeSuccess(c *gin.Context, status int, data any) {
	c.JSON(status, successEnvelope{
		Data: data,
		Meta: responseMeta{
			RequestID:  c.GetString("request_id"),
			ServerTime: time.Now().UTC().Format(time.RFC3339),
		},
	})
}

// writeError forwards a typed service error, mapping a bare error to INTERNAL.
func writeError(c *gin.Context, err error) {
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		apiErr = apierror.New(apierror.CodeInternal, 0, "The request could not be completed.")
	}
	apierror.Write(c.Writer, c.Request, apiErr)
}

// RegisterRoutes mounts the three account endpoints on the caller's group. The
// delete route carries the CSRF guard the composition root supplies, since a
// destructive call must fail the same origin check.
func RegisterRoutes(r gin.IRouter, h *Handler) {
	r.GET("/accounts", h.requireSession, h.List)
	r.GET("/accounts/:account_id", h.requireSession, h.Show)
	r.DELETE("/accounts/:account_id", h.requireSession, h.csrf, h.Delete)
}
