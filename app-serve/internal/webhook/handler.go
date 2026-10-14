// Package webhook serves the TikTok webhook intake endpoint.
//
// @file      internal/webhook/handler.go
// @for       Signature verification and envelope dispatch over HTTP.
// @uses      encoding/json, errors, io, log/slog, net/http, time, gin, internal/apierror
// @reason    Gives TikTok one authenticated entry point for publish events.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package webhook

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// defaultTolerance matches the 002 section 5.2 replay window.
const defaultTolerance = 5 * time.Minute

// HandlerConfig carries the HMAC secret and the replay tolerance.
type HandlerConfig struct {
	Secret    string
	Tolerance time.Duration
}

// accepted is the only success body. TikTok ignores it and retries on any
// other status, so the payload carries no state.
type accepted struct {
	Status string `json:"status"`
}

// Handler verifies one webhook delivery and hands it to the dispatcher. It
// holds no store: persistence belongs to the dispatcher, and the queue the
// worker owns stays out of the intake path so backpressure cannot be faked
// here.
type Handler struct {
	dispatch *Dispatcher
	cfg      HandlerConfig
	logger   *slog.Logger
}

// NewHandler wires the intake to the dispatcher and logger. A nil logger falls
// back to the process default and a zero tolerance falls back to the contract
// default.
func NewHandler(dispatch *Dispatcher, cfg HandlerConfig, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Tolerance <= 0 {
		cfg.Tolerance = defaultTolerance
	}
	return &Handler{dispatch: dispatch, cfg: cfg, logger: logger}
}

// Receive authenticates the delivery then dispatches it. Verification runs
// before parsing, so an unauthenticated caller gets the same 401 whether the
// signature or the envelope is bad. Every reject logs the source IP and never
// the body, which may carry account and token metadata.
func (h *Handler) Receive(c *gin.Context) {
	raw, ok := h.readBody(c)
	if !ok {
		return
	}
	header := c.GetHeader("TikTok-Signature")
	if err := Verify(h.cfg.Secret, header, raw, time.Now().UTC(), h.cfg.Tolerance); err != nil {
		h.reject(c, "signature verification failed", "signature_present", header != "")
		return
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		h.reject(c, "envelope decoding failed", "stage", "decode")
		return
	}
	outcome, err := h.dispatch.Handle(c.Request.Context(), env, raw)
	if err != nil {
		h.answerHandleError(c, env, outcome, err)
		return
	}
	h.logger.Info("webhook delivery handled", "event", env.Event, "outcome", int(outcome))
	c.JSON(http.StatusOK, accepted{Status: "accepted"})
}

// answerHandleError preserves the typed error from the dispatcher so a
// permanently malformed payload answers 502 and TikTok stops hammering it as
// a 500, and only a bare error becomes a generic 500.
func (h *Handler) answerHandleError(c *gin.Context, env Envelope, outcome Outcome, err error) {
	h.logger.Error("webhook handling failed", "event", env.Event,
		"outcome", int(outcome), "error", err.Error())
	var apiErr *apierror.Error
	if errors.As(err, &apiErr) {
		apierror.Write(c.Writer, c.Request, apiErr)
		return
	}
	apierror.Write(c.Writer, c.Request,
		apierror.New(apierror.CodeInternal, 0, "The webhook could not be processed."))
}

// readBody reads at most MaxBodyBytes plus one byte. Reading one byte past the
// limit is what separates a body at the limit from one over it, so an oversized
// delivery is rejected instead of truncated and then failing to verify.
func (h *Handler) readBody(c *gin.Context) ([]byte, bool) {
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, MaxBodyBytes+1))
	if err != nil {
		h.reject(c, "body read failed", "stage", "read")
		return nil, false
	}
	if len(raw) > MaxBodyBytes {
		h.reject(c, "body exceeds the size limit", "stage", "limit")
		return nil, false
	}
	return raw, true
}

// reject logs the failure and answers the single generic 401 problem body so a
// caller learns nothing about which check failed.
func (h *Handler) reject(c *gin.Context, reason string, extra ...any) {
	args := append([]any{"reason", reason, "ip", c.ClientIP()}, extra...)
	h.logger.Warn("webhook rejected", args...)
	apierror.Write(c.Writer, c.Request,
		apierror.New(apierror.CodeUnauthorized, 0, "The webhook signature is invalid."))
}
