// Package auth issues and checks operator sessions and OAuth state.
//
// @file      internal/auth/handler.go
// @for       The auth HTTP handlers, success envelope, and session cookie.
// @uses      net/http, time, errors, gin, go-playground/validator/v10, internal/apierror
// @reason    The console drives sign in, refresh, revoke, and logout over HTTP.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// HandlerConfig carries the cookie settings the handlers apply.
type HandlerConfig struct {
	CookieName   string
	CookieTTL    time.Duration
	AppUIBaseURL string
}

// Handler serves the auth HTTP surface over one Service.
type Handler struct {
	svc *Service
	cfg HandlerConfig
}

// NewHandler wires the auth handlers to their service and cookie config.
func NewHandler(svc *Service, cfg HandlerConfig) *Handler {
	return &Handler{svc: svc, cfg: cfg}
}

// successEnvelope is the shared success shape: typed data plus request meta.
type successEnvelope struct {
	Data successData  `json:"data"`
	Meta responseMeta `json:"meta"`
}

// successData marks a payload that travels inside the success envelope.
type successData interface {
	successData()
}

// responseMeta carries the request id and the server clock.
type responseMeta struct {
	RequestID  string `json:"request_id"`
	ServerTime string `json:"server_time"`
}

// accountData is the refresh payload.
type accountData struct {
	AccountID string `json:"account_id"`
}

func (accountData) successData() {}

// sessionView wraps SessionResponse so it can ride in the envelope.
type sessionView struct {
	SessionResponse
}

func (sessionView) successData() {}

// refreshRequest is the typed body shared by refresh and revoke.
type refreshRequest struct {
	AccountID string `json:"account_id" binding:"required"`
}

// Refresh rotates the TikTok tokens of one account.
func (h *Handler) Refresh(c *gin.Context) {
	sess, ok := SessionFrom(c)
	if !ok {
		writeError(c, errNoSession())
		return
	}
	req, ok := bindAccount(c)
	if !ok {
		return
	}
	if err := h.svc.Refresh(c.Request.Context(), sess.OperatorID, req.AccountID); err != nil {
		writeError(c, err)
		return
	}
	h.writeSuccess(c, http.StatusOK, accountData(req))
}

// Revoke invalidates the TikTok credentials of one account.
func (h *Handler) Revoke(c *gin.Context) {
	sess, ok := SessionFrom(c)
	if !ok {
		writeError(c, errNoSession())
		return
	}
	req, ok := bindAccount(c)
	if !ok {
		return
	}
	if err := h.svc.Revoke(c.Request.Context(), sess.OperatorID, req.AccountID); err != nil {
		writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Logout deletes the server session and expires the browser cookie.
func (h *Handler) Logout(c *gin.Context) {
	sess, ok := SessionFrom(c)
	if !ok {
		writeError(c, errNoSession())
		return
	}
	if err := h.svc.Logout(c.Request.Context(), sess.ID); err != nil {
		writeError(c, err)
		return
	}
	writeSessionCookie(c, h.cfg, "")
	c.Status(http.StatusNoContent)
}

// Session returns the current operator session without token material.
func (h *Handler) Session(c *gin.Context) {
	sess, ok := SessionFrom(c)
	if !ok {
		writeError(c, errNoSession())
		return
	}
	h.writeSuccess(c, http.StatusOK, sessionView{SessionResponse{
		OperatorID: sess.OperatorID,
		Scopes:     sess.Scopes,
		ExpiresAt:  h.expiresAt(sess.CreatedAt),
	}})
}

// errNoSession is the client safe answer when a guarded route runs without
// an attached session, so a wiring mistake still denies instead of panicking.
func errNoSession() *apierror.Error {
	return apierror.New(apierror.CodeUnauthorized, 0, "Sign in to continue.")
}

// writeSuccess writes the success envelope with fresh request metadata.
func (h *Handler) writeSuccess(c *gin.Context, status int, data successData) {
	c.JSON(status, successEnvelope{
		Data: data,
		Meta: responseMeta{
			RequestID:  c.GetString("request_id"),
			ServerTime: time.Now().UTC().Format(time.RFC3339),
		},
	})
}

// expiresAt derives the session deadline from creation plus the cookie TTL.
// An unset TTL leaves the creation time, so the view never reads negative.
func (h *Handler) expiresAt(created time.Time) time.Time {
	if h.cfg.CookieTTL <= 0 {
		return created
	}
	return created.Add(h.cfg.CookieTTL)
}

// writeSessionCookie writes the session cookie, or expires it when sessionID
// is empty. Callback passes a session id; Logout passes the empty string.
func writeSessionCookie(c *gin.Context, cfg HandlerConfig, sessionID string) {
	maxAge := int(cfg.CookieTTL / time.Second)
	if sessionID == "" {
		maxAge = -1
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cfg.CookieName,
		Value:    sessionID,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// bindAccount decodes and validates the account body. On failure the 422
// problem is already written, so the caller returns at once.
func bindAccount(c *gin.Context) (refreshRequest, bool) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeValidationError(c, err)
		return refreshRequest{}, false
	}
	return req, true
}

// writeValidationError answers a malformed body or a missing required field.
func writeValidationError(c *gin.Context, err error) {
	apiErr := apierror.New(apierror.CodeValidationFailed, 0, "The request body is invalid.")
	if fields := fieldErrors(err); len(fields) > 0 {
		apiErr = apiErr.WithFields(fields)
	}
	apierror.Write(c.Writer, c.Request, apiErr)
}

// fieldErrors maps validator failures to wire field errors.
func fieldErrors(err error) []apierror.FieldError {
	var invalid validator.ValidationErrors
	if !errors.As(err, &invalid) {
		return nil
	}
	fields := make([]apierror.FieldError, 0, len(invalid))
	for _, fe := range invalid {
		fields = append(fields, apierror.FieldError{
			Field: fe.Field(),
			Tag:   fe.Tag(),
			Param: fe.Param(),
		})
	}
	return fields
}

// writeError forwards a typed service error, mapping a bare error to INTERNAL.
func writeError(c *gin.Context, err error) {
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		apiErr = apierror.New(apierror.CodeInternal, 0, "The request could not be completed.")
	}
	apierror.Write(c.Writer, c.Request, apiErr)
}
