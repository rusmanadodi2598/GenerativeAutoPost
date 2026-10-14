// Package publish registers the publish routes and shapes their responses.
//
// @file      internal/publish/router.go
// @for       The publish route table, its session guard, and the response envelope.
// @uses      context, errors, strconv, time, gin, go-playground/validator, internal/apierror
// @reason    Callers mount the publish endpoints without knowing the guard layout.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package publish

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// validate runs the same struct rules the service runs, so a handler rejects
// a malformed body before any upstream call happens.
var validate = validator.New()

// operatorContextKey is the gin slot holding the operator id resolved by
// requireSession. The unexported struct key keeps other packages from forging
// a value into the slot.
type operatorContextKey struct{}

// successEnvelope is the shared success shape: typed data plus request meta.
type successEnvelope struct {
	Data any          `json:"data"`
	Meta responseMeta `json:"meta"`
}

// responseMeta carries the request id and the server clock.
type responseMeta struct {
	RequestID  string `json:"request_id"`
	ServerTime string `json:"server_time"`
}

// attemptView is the stored row as the console sees it. It lists fields one
// by one, so no code path can leak the upstream upload URL.
type attemptView struct {
	ID            string    `json:"id"`
	AccountID     string    `json:"account_id"`
	PublishID     string    `json:"publish_id"`
	PostMode      string    `json:"post_mode"`
	MediaType     string    `json:"media_type"`
	Source        string    `json:"source"`
	Status        Status    `json:"status"`
	PrivacyLevel  string    `json:"privacy_level,omitempty"`
	Title         string    `json:"title,omitempty"`
	Description   string    `json:"description,omitempty"`
	FailReason    string    `json:"fail_reason,omitempty"`
	PostID        string    `json:"post_id,omitempty"`
	UploadedBytes int64     `json:"uploaded_bytes"`
	CreatedAt     time.Time `json:"created_at"`
}

// viewAttempt maps one stored row to the wire shape. The fields are copied
// one by one so a new column cannot reach the wire without a decision.
func viewAttempt(rec Record) attemptView {
	var view attemptView
	view.ID, view.AccountID, view.PublishID = rec.ID, rec.AccountID, rec.PublishID
	view.PostMode, view.MediaType, view.Source = rec.PostMode, rec.MediaType, rec.Source
	view.Status, view.PrivacyLevel, view.Title = rec.Status, rec.PrivacyLevel, rec.Title
	view.Description, view.FailReason, view.PostID = rec.Description, rec.FailReason, rec.PostID
	view.UploadedBytes, view.CreatedAt = rec.UploadedBytes, rec.CreatedAt
	return view
}

// AttemptReader is the repository slice the lookup and list routes need.
// *Repository satisfies it directly.
type AttemptReader interface {
	Get(ctx context.Context, id string) (Record, error)
	List(ctx context.Context, accountID, status string, cursor time.Time, limit int) ([]Record, error)
}

// Reconciler re-reads the upstream status of one attempt and stores it.
type Reconciler interface {
	Reconcile(ctx context.Context, operatorID, attemptID string) (Record, error)
}

// chunkIndex parses the optional index header. An empty header means the
// service decides which index comes next.
func chunkIndex(raw string) (int, bool) {
	if raw == "" {
		return 0, true
	}
	index, err := strconv.Atoi(raw)
	return index, err == nil && index >= 0
}

// listLimit clamps the query limit to the 1..100 window.
func listLimit(raw string) (int, bool) {
	if raw == "" {
		return defaultListLimit, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return 0, false
	}
	if limit > maxListLimit {
		return maxListLimit, true
	}
	return limit, true
}

// listCursor parses the millisecond timestamp cursor. An empty cursor starts
// at the beginning of the result set.
func listCursor(raw string) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, true
	}
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(ms).UTC(), true
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

// writeSuccess writes the success envelope with fresh request metadata.
func (h *Handler) writeSuccess(c *gin.Context, status int, data any) {
	c.JSON(status, successEnvelope{
		Data: data,
		Meta: responseMeta{
			RequestID:  c.GetString("request_id"),
			ServerTime: time.Now().UTC().Format(time.RFC3339),
		},
	})
}

// writeValidation answers one invalid request field.
func writeValidation(c *gin.Context, detail string) {
	apierror.Write(c.Writer, c.Request, apierror.New(apierror.CodeValidationFailed, 0, detail))
}

// writeValidationError answers a malformed or rule breaking body, attaching
// the validator field errors when the failure came from struct rules.
func writeValidationError(c *gin.Context, err error) {
	var invalid validator.ValidationErrors
	if errors.As(err, &invalid) {
		fields := make([]apierror.FieldError, 0, len(invalid))
		for _, fe := range invalid {
			fields = append(fields, apierror.FieldError{
				Field: fe.Field(), Tag: fe.Tag(), Param: fe.Param(),
			})
		}
		apierror.Write(c.Writer, c.Request,
			apierror.New(apierror.CodeValidationFailed, 0, "Init request failed validation.").
				WithFields(fields).Wrap(err))
		return
	}
	apierror.Write(c.Writer, c.Request,
		apierror.New(apierror.CodeValidationFailed, 0, "The request body is invalid.").Wrap(err))
}

// writeError forwards a typed service error, mapping a bare error to INTERNAL.
func writeError(c *gin.Context, err error) {
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		apiErr = apierror.New(apierror.CodeInternal, 0, "The request could not be completed.")
	}
	apierror.Write(c.Writer, c.Request, apiErr)
}

// RegisterRoutes mounts the seven publish endpoints on the caller's router or
// group. The caller mounts the group under /api/v1, so the paths stay
// relative. requireSession is attached per route because the passed group is
// shared with unguarded routes.
func RegisterRoutes(r gin.IRouter, h *Handler) {
	r.POST("/publish/videos", h.requireSession, h.InitVideo)
	r.POST("/publish/photos", h.requireSession, h.InitPhoto)
	r.POST("/publish/drafts", h.requireSession, h.InitDraft)
	r.PUT("/publish/:id/chunks", h.requireSession, h.PutChunk)
	r.GET("/publish/:id", h.requireSession, h.Show)
	r.GET("/publish", h.requireSession, h.List)
	r.POST("/publish/:id/reconcile", h.requireSession, h.Reconcile)
}
