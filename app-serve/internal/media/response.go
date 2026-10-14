// Package media shapes every media response into the shared envelope.
//
// @file      internal/media/response.go
// @for       Success envelope and error writers for the media handlers.
// @uses      errors, net/http, strings, time, gin, internal/apierror
// @reason    Keeps one response shape so clients parse every media reply alike.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package media

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

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

// mediaView wraps MediaObject so it can ride in the envelope.
type mediaView struct {
	MediaObject
}

func (mediaView) successData() {}

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

// tooLarge reports whether err came from the MaxBytesReader cap.
func tooLarge(err error) bool {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "request body too large")
}

// missingAsNotFound maps a store miss to a client safe 404 so the store
// package never leaks its internal error text to the wire.
func missingAsNotFound(err error) error {
	if errors.Is(err, errObjectMissing) {
		return apierror.New(apierror.CodeNotFound, 0, "Media object not found.")
	}
	var apiErr *apierror.Error
	if errors.As(err, &apiErr) {
		return err
	}
	return apierror.New(apierror.CodeNotFound, 0, "Media object not found.")
}

// writeValidation answers one invalid form field without field detail.
func writeValidation(c *gin.Context, detail string) {
	apierror.Write(c.Writer, c.Request, apierror.New(apierror.CodeValidationFailed, 0, detail))
}

// writeError forwards a typed service error, mapping a bare error to INTERNAL.
func writeError(c *gin.Context, err error) {
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		apiErr = apierror.New(apierror.CodeInternal, 0, "The request could not be completed.")
	}
	apierror.Write(c.Writer, c.Request, apiErr)
}
