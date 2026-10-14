// Package media serves upload, lookup, and delete endpoints over one Service.
//
// @file      internal/media/handler.go
// @for       HTTP handlers for the media upload surface.
// @uses      net/http, time, gin, internal/apierror
// @reason    Exposes validated uploads to the console without leaking storage.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package media

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// SessionAccount resolves the owning account id for one request. A false
// result means no session is attached, so the handler answers 401.
type SessionAccount func(*gin.Context) (string, bool)

// Handler serves the media HTTP surface over one Service and Store.
type Handler struct {
	svc     *Service
	store   Store
	session SessionAccount
}

// NewHandler wires the media handlers to validation, storage, and sessions.
func NewHandler(svc *Service, store Store, session SessionAccount) *Handler {
	return &Handler{svc: svc, store: store, session: session}
}

// Upload accepts one multipart file part and stores it after validation.
func (h *Handler) Upload(c *gin.Context) {
	accountID, ok := accountFrom(c)
	if !ok {
		apierror.Write(c.Writer, c.Request,
			apierror.New(apierror.CodeUnauthorized, 0, "Sign in to continue."))
		return
	}
	maxBytes := h.svc.MaxBytes()
	if maxBytes <= 0 {
		writeError(c, apierror.New(apierror.CodeInternal, 0, "The request could not be completed."))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)

	kind, ok := uploadKind(c.PostForm("kind"))
	if !ok {
		writeValidation(c, "Media kind must be video or photo.")
		return
	}
	durationMs, ok := uploadDuration(c.PostForm("duration_ms"))
	if !ok {
		writeValidation(c, "Media duration must be a non-negative integer.")
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		if tooLarge(err) {
			writeValidation(c, "Media payload exceeds the request size limit.")
			return
		}
		writeValidation(c, "The file part is required.")
		return
	}
	defer func() { _ = file.Close() }()
	if header.Size <= 0 {
		writeValidation(c, "The file part must not be empty.")
		return
	}

	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = sniffContentType(file)
	}

	obj, err := h.svc.Upload(c.Request.Context(), accountID, kind, contentType, header.Size, durationMs, file)
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeSuccess(c, http.StatusCreated, mediaView{obj})
}

// Show returns one object: a 302 to its signed URL when the store signs
// public URLs, otherwise the metadata envelope. A missing id is a 404.
func (h *Handler) Show(c *gin.Context) {
	accountID, ok := accountFrom(c)
	if !ok {
		apierror.Write(c.Writer, c.Request,
			apierror.New(apierror.CodeUnauthorized, 0, "Sign in to continue."))
		return
	}
	obj, rc, err := h.svc.Get(c.Request.Context(), accountID, c.Param("id"))
	if err != nil {
		writeError(c, missingAsNotFound(err))
		return
	}
	_ = rc.Close()

	signed, err := h.store.SignPublicURL(c.Request.Context(), obj.ID)
	if err != nil {
		writeError(c, err)
		return
	}
	if signed != "" {
		c.Redirect(http.StatusFound, signed)
		return
	}
	h.writeSuccess(c, http.StatusOK, mediaView{obj})
}

// Delete removes one object and answers 204. A missing id still answers
// 204 because the end state, no stored bytes, already holds.
func (h *Handler) Delete(c *gin.Context) {
	accountID, ok := accountFrom(c)
	if !ok {
		apierror.Write(c.Writer, c.Request,
			apierror.New(apierror.CodeUnauthorized, 0, "Sign in to continue."))
		return
	}
	if err := h.svc.Delete(c.Request.Context(), accountID, c.Param("id")); err != nil {
		writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// uploadKind accepts only the two supported kinds. An empty value means
// video so plain photo-agnostic clients keep working.
func uploadKind(raw string) (MediaKind, bool) {
	switch MediaKind(strings.ToLower(strings.TrimSpace(raw))) {
	case "", MediaKindVideo:
		return MediaKindVideo, true
	case MediaKindPhoto:
		return MediaKindPhoto, true
	default:
		return "", false
	}
}

// uploadDuration parses the optional duration form value. Empty means zero.
func uploadDuration(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms < 0 {
		return 0, false
	}
	return ms, true
}

// sniffContentType reads the head of the stream to guess its type, then
// seeks back so the service still checksums the full payload.
func sniffContentType(file interface {
	Read([]byte) (int, error)
	Seek(int64, int) (int64, error)
}) string {
	head := make([]byte, 512)
	n, _ := file.Read(head)
	_, _ = file.Seek(0, 0)
	if n == 0 {
		return "application/octet-stream"
	}
	return http.DetectContentType(head[:n])
}
