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
	"crypto/sha256"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// SessionAccount resolves the owning account id for one request. A false
// result means no session is attached, so the handler answers 401.
type SessionAccount func(*gin.Context) (string, bool)

// Handler serves the media HTTP surface over one Service, one Store, and
// one idempotency guard. A nil guard means uploads are not idempotent.
type Handler struct {
	svc     *Service
	store   Store
	guard   UploadGuard
	session SessionAccount
}

// NewHandler wires the media handlers to validation, storage, replay, and
// sessions.
func NewHandler(svc *Service, store Store, guard UploadGuard, session SessionAccount) *Handler {
	return &Handler{svc: svc, store: store, guard: guard, session: session}
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

	checksum := c.GetHeader(checksumHeader)
	if h.guard == nil {
		// No declared replay key: validate the header on its own so a
		// malformed checksum still fails before the file is read.
		var ok bool
		checksum, ok = uploadChecksum(checksum)
		if !ok {
			writeValidation(c, "Checksum-SHA256 must be 64 lowercase hex characters.")
			return
		}
	}

	claim, err := h.claimUpload(c)
	if err != nil {
		writeError(c, err)
		return
	}
	if claim.replay != nil {
		h.writeReplay(c, claim.replay)
		return
	}
	if claim.claimed() {
		checksum = claim.checksum
	}

	obj, err := h.svc.Upload(c.Request.Context(), UploadRequest{
		AccountID: accountID, Kind: kind, ContentType: contentType, Size: header.Size,
		DurationMs: durationMs, ExpectedChecksum: checksum, Body: file,
	})
	if err != nil {
		h.abandonUpload(c, claim)
		writeError(c, missingAsNotFound(err))
		return
	}
	h.finishUpload(c, claim, obj)
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

// checksumHeader carries the client declared SHA-256 of the file bytes in hex.
// The upload handler validates its shape so the claim runs on a normalized
// value, never on a raw header string.
const checksumHeader = "Checksum-SHA256"

// uploadChecksum normalizes the Checksum-SHA256 header to a lowercase hex
// digest. An absent header is legal and means the client sends no declared
// checksum.
func uploadChecksum(raw string) (string, bool) {
	if raw == "" {
		return "", true
	}
	digest := strings.ToLower(strings.TrimSpace(raw))
	if len(digest) != sha256.Size*2 {
		return "", false
	}
	for _, c := range digest {
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' {
			continue
		}
		return "", false
	}
	return digest, true
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
