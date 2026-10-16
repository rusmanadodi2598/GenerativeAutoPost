// Package media replays a repeated upload instead of storing it twice.
//
// @file      internal/media/upload_guard.go
// @for       Idempotency-Key claim, replay, and release for POST /media/uploads.
// @uses      context, encoding/json, errors, fmt, gin, internal/apierror, internal/platform/idempotency
// @reason    A streamed body has no hash until it is read, so the claim keys on the client checksum.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-16
package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/idempotency"
)

// idempotencyHeader carries the caller supplied replay key. A request without
// it is not idempotent and is stored normally.
const idempotencyHeader = "Idempotency-Key"

// UploadGuard is the slice of the idempotency store the upload route needs.
// The media package declares it here so the route does not depend on how the
// claim is stored.
type UploadGuard interface {
	Begin(ctx context.Context, key, bodyHash string) ([]byte, error)
	Complete(ctx context.Context, key, bodyHash string, response []byte) error
	Release(ctx context.Context, key, bodyHash string) error
}

var _ UploadGuard = (*idempotency.Store)(nil)

// uploadClaim is the replay state one request carries through the handler. An
// empty key means the request is not idempotent; a non empty replay means the
// first attempt already finished and this request only reads its answer.
type uploadClaim struct {
	key      string
	checksum string
	replay   []byte
}

// claimed reports whether this request owns a key it must complete or release.
func (cl uploadClaim) claimed() bool { return cl.key != "" }

// claimUpload takes the replay key for this request. It returns the stored
// response bytes when the key was already completed, so the caller replays
// that instead of storing the file again.
//
// The claim is keyed on the declared checksum rather than a body hash: a
// multipart body has no hash until it is fully read, and reading it first
// would leave a concurrent duplicate with nothing to collide on.
func (h *Handler) claimUpload(c *gin.Context) (uploadClaim, error) {
	key := c.GetHeader(idempotencyHeader)
	if key == "" || h.guard == nil {
		return uploadClaim{}, nil
	}
	checksum, ok := uploadChecksum(c.GetHeader(checksumHeader))
	if !ok {
		return uploadClaim{}, apierror.New(apierror.CodeValidationFailed, 0,
			"Checksum-SHA256 must be 64 lowercase hex characters when Idempotency-Key is sent.")
	}
	if checksum == "" {
		return uploadClaim{}, apierror.New(apierror.CodeValidationFailed, 0,
			"Checksum-SHA256 is required when Idempotency-Key is sent.")
	}
	stored, err := h.guard.Begin(c.Request.Context(), key, checksum)
	if err != nil {
		return uploadClaim{}, keyReuseError(err)
	}
	return uploadClaim{key: key, checksum: checksum, replay: stored}, nil
}

// keyReuseError maps a store refusal onto the client safe conflict code, so
// the store wording never reaches the media surface.
func keyReuseError(err error) error {
	if errors.Is(err, idempotency.ErrKeyReused) {
		return apierror.New(apierror.CodeIdempotencyKeyReused, 0,
			"Idempotency key was already used with a different file.")
	}
	return err
}

// writeReplay answers a repeated request with the object the first attempt
// stored, so a retry returns the same media id instead of a second object.
func (h *Handler) writeReplay(c *gin.Context, stored []byte) {
	var view mediaView
	if err := json.Unmarshal(stored, &view); err != nil {
		writeError(c, apierror.New(apierror.CodeInternal, 0,
			"The stored upload response could not be read.").Wrap(err))
		return
	}
	h.writeSuccess(c, http.StatusCreated, view)
}

// finishUpload records the stored response so a repeat replays it. A failure
// here is logged through the gin error chain instead of failing the upload:
// the bytes are already stored, so the caller got a real object either way.
func (h *Handler) finishUpload(c *gin.Context, cl uploadClaim, obj MediaObject) {
	if !cl.claimed() || h.guard == nil {
		return
	}
	body, err := json.Marshal(mediaView{obj})
	if err != nil {
		_ = c.Error(fmt.Errorf("media: encoding replay payload: %w", err))
		return
	}
	if err := h.guard.Complete(c.Request.Context(), cl.key, cl.checksum, body); err != nil {
		_ = c.Error(fmt.Errorf("media: recording upload replay: %w", err))
	}
}

// abandonUpload drops a claim whose upload failed, so the client may correct
// the request and retry under the same key instead of being answered 409 by a
// claim that will never complete.
func (h *Handler) abandonUpload(c *gin.Context, cl uploadClaim) {
	if !cl.claimed() || h.guard == nil {
		return
	}
	if err := h.guard.Release(c.Request.Context(), cl.key, cl.checksum); err != nil {
		_ = c.Error(fmt.Errorf("media: releasing upload claim: %w", err))
	}
}
