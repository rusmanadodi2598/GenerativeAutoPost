// Package publish serves the TikTok publish HTTP surface over one Service.
//
// @file      internal/publish/handler.go
// @for       Gin handlers for the init, chunk, lookup, list, and reconcile routes.
// @uses      errors, io, net/http, gin, go-playground/validator/v10, internal/apierror
// @reason    Keeps request decoding, session resolution, and envelopes out of the service.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package publish

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// SessionAccount resolves the operator id for one request. A false result
// means no session is attached, so the guard answers 401.
type SessionAccount func(*gin.Context) (string, bool)

// idempotencyHeader carries the caller supplied replay key. An absent header
// means the request is not idempotent, which the service already handles.
const idempotencyHeader = "Idempotency-Key"

// chunkIndexHeader carries the optional zero based chunk index.
const chunkIndexHeader = "X-Chunk-Index"

// HandlerConfig carries the request body cap applied to chunk uploads.
type HandlerConfig struct {
	MaxChunkBytes int64
}

// Handler serves the publish HTTP surface.
type Handler struct {
	svc     *Service
	reader  AttemptReader
	recon   Reconciler
	session SessionAccount
	maxBody int64
}

// NewHandler wires the publish handlers to the service, repository, and session.
func NewHandler(svc *Service, reader AttemptReader, recon Reconciler, session SessionAccount, cfg HandlerConfig) *Handler {
	return &Handler{svc: svc, reader: reader, recon: recon, session: session, maxBody: cfg.MaxChunkBytes}
}

// InitVideo accepts a video init body and answers 202 with the attempt.
func (h *Handler) InitVideo(c *gin.Context) {
	operatorID, ok := operatorFrom(c)
	if !ok {
		return
	}
	var req InitVideoRequest
	if !bind(c, &req) {
		return
	}
	res, err := h.svc.InitVideo(c.Request.Context(), operatorID, req, c.Query("generated") == "true", c.GetHeader(idempotencyHeader))
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeSuccess(c, http.StatusAccepted, res)
}

// InitPhoto accepts a photo init body and answers 202 with the attempt.
func (h *Handler) InitPhoto(c *gin.Context) {
	operatorID, ok := operatorFrom(c)
	if !ok {
		return
	}
	var req InitPhotoRequest
	if !bind(c, &req) {
		return
	}
	res, err := h.svc.InitPhoto(c.Request.Context(), operatorID, req, c.Query("generated") == "true", c.GetHeader(idempotencyHeader))
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeSuccess(c, http.StatusAccepted, res)
}

// InitDraft accepts a draft init body and answers 202 with the attempt.
func (h *Handler) InitDraft(c *gin.Context) {
	operatorID, ok := operatorFrom(c)
	if !ok {
		return
	}
	var req InitDraftRequest
	if !bind(c, &req) {
		return
	}
	res, err := h.svc.InitDraft(c.Request.Context(), operatorID, req, c.GetHeader(idempotencyHeader))
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeSuccess(c, http.StatusAccepted, res)
}

// PutChunk accepts one raw chunk body and answers 200 with the progress.
func (h *Handler) PutChunk(c *gin.Context) {
	operatorID, ok := operatorFrom(c)
	if !ok {
		return
	}
	index, ok := chunkIndex(c.GetHeader(chunkIndexHeader))
	if !ok {
		writeValidation(c, "Chunk index must be a non-negative integer.")
		return
	}
	if h.maxBody > 0 {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxBody)
	}
	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeValidation(c, "Chunk payload exceeds the request size limit.")
		return
	}
	res, err := h.svc.PutChunk(c.Request.Context(), operatorID, c.Param("id"), index, c.GetHeader("Content-Type"), data)
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeSuccess(c, http.StatusOK, res)
}

// Show returns one attempt the operator owns. A foreign or missing attempt is
// a 404 so the endpoint never confirms another account's attempt id.
func (h *Handler) Show(c *gin.Context) {
	operatorID, ok := operatorFrom(c)
	if !ok {
		return
	}
	rec, err := h.reader.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeError(c, err)
		return
	}
	if _, err := h.svc.accounts.Owned(c.Request.Context(), operatorID, rec.AccountID); err != nil {
		writeError(c, apierror.New(apierror.CodeNotFound, 0, "Publish attempt not found."))
		return
	}
	h.writeSuccess(c, http.StatusOK, viewAttempt(rec))
}

// List returns the operator's attempts, newest first, with a clamped limit.
func (h *Handler) List(c *gin.Context) {
	operatorID, ok := operatorFrom(c)
	if !ok {
		return
	}
	limit, ok := listLimit(c.Query("limit"))
	if !ok {
		writeValidation(c, "Limit must be an integer between 1 and 100.")
		return
	}
	cursor, ok := listCursor(c.Query("cursor"))
	if !ok {
		writeValidation(c, "Cursor must be a millisecond timestamp.")
		return
	}
	accountID := c.Query("account_id")
	if accountID != "" {
		if _, err := h.svc.accounts.Owned(c.Request.Context(), operatorID, accountID); err != nil {
			writeError(c, err)
			return
		}
	}
	records, err := h.reader.List(c.Request.Context(), accountID, c.Query("status"), cursor, limit)
	if err != nil {
		writeError(c, err)
		return
	}
	views := make([]attemptView, 0, len(records))
	for _, rec := range records {
		views = append(views, viewAttempt(rec))
	}
	h.writeSuccess(c, http.StatusOK, views)
}

// Reconcile re-reads the upstream status of one owned attempt and answers 202.
func (h *Handler) Reconcile(c *gin.Context) {
	operatorID, ok := operatorFrom(c)
	if !ok {
		return
	}
	if h.recon == nil {
		writeError(c, apierror.New(apierror.CodeInternal, 0, "Reconciliation is not configured."))
		return
	}
	rec, err := h.recon.Reconcile(c.Request.Context(), operatorID, c.Param("id"))
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeSuccess(c, http.StatusAccepted, viewAttempt(rec))
}

// bind decodes and validates one typed body. On failure the 422 problem is
// already written, so the caller returns at once.
func bind(c *gin.Context, target any) bool {
	if err := c.ShouldBindJSON(target); err != nil {
		writeValidationError(c, err)
		return false
	}
	var invalid validator.ValidationErrors
	if err := validate.Struct(target); err != nil {
		if !errors.As(err, &invalid) {
			writeError(c, err)
			return false
		}
		apiErr := apierror.New(apierror.CodeValidationFailed, 0, "Init request failed validation.").Wrap(err)
		writeError(c, apiErr)
		return false
	}
	return true
}
