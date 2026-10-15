// Package insight registers the insight routes and shapes their responses.
//
// @file      internal/insight/router.go
// @for       The insight route table, its session guard, and the response envelope.
// @uses      context, errors, strconv, time, gin, go-playground/validator, internal/apierror
// @reason    Callers mount the insight endpoints without knowing the guard layout.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package insight

import (
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// validate runs the struct rules on the query body before the service is called.
var validate = validator.New()

// operatorContextKey is the gin slot holding the operator id resolved by
// requireSession. The unexported struct key keeps other packages from forging
// a value into the slot.
type operatorContextKey struct{}

// SessionAccount resolves the operator id for one request. A false result
// means no session is attached, so the guard answers 401.
type SessionAccount func(*gin.Context) (string, bool)

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

// maxCountQuery parses the optional page size. An empty value passes zero so
// the client applies its own default.
func maxCountQuery(raw string) (int, bool) {
	if raw == "" {
		return 0, true
	}
	count, err := strconv.Atoi(raw)
	if err != nil || count <= 0 || count > maxQueryIDs {
		return 0, false
	}
	return count, true
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

// bind decodes and validates one typed body. On failure the 422 problem is
// already written, so the caller returns at once. The reason for the untyped
// target is the decode boundary: every caller passes a concrete schema struct.
func bind(c *gin.Context, target any) bool {
	if err := c.ShouldBindJSON(target); err != nil {
		writeValidationError(c, err)
		return false
	}
	if err := validate.Struct(target); err != nil {
		writeValidationError(c, err)
		return false
	}
	return true
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
			apierror.New(apierror.CodeValidationFailed, 0, "Request failed validation.").
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

// RegisterRoutes mounts the six insight endpoints on the caller's router or
// group. The caller mounts the group under /api/v1, so the paths stay
// relative.
func RegisterRoutes(r gin.IRouter, h *Handler) {
	r.GET("/videos", h.requireSession, h.List)
	r.POST("/videos/query", h.requireSession, h.Query)
	r.GET("/videos/:video_id", h.requireSession, h.Show)
	r.GET("/videos/:video_id/stats", h.requireSession, h.Stats)
	r.POST("/videos/:video_id/stats/refresh", h.requireSession, h.RefreshStats)
	r.GET("/accounts/:account_id/profile", h.requireSession, h.Profile)
}
