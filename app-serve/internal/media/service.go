// Package media validates uploads before they reach any object store.
//
// @file      internal/media/service.go
// @for       Upload validation, checksum computation, and URL allow checks.
// @uses      context, crypto/sha256, encoding/hex, log/slog, net/url, io, github.com/google/uuid
// @reason    Rejects bad input before storage so a rejected upload costs no disk.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// ServiceConfig holds the limits and allow lists one service enforces.
type ServiceConfig struct {
	Driver        string
	MaxBytes      int64
	MaxDurationMs int64
	AllowedTypes  []string
	PublicBaseURL string
	Root          string
}

// Service validates uploads and hands accepted bytes to a Store.
type Service struct {
	store  Store
	cfg    ServiceConfig
	logger *slog.Logger
}

// MaxBytes is the configured upload cap, used by the handler to bound the request body.
func (s *Service) MaxBytes() int64 {
	return s.cfg.MaxBytes
}

// NewService wires validation and storage together.
func NewService(store Store, cfg ServiceConfig, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: store, cfg: cfg, logger: logger}
}

// Upload validates the declared metadata, checksums the bytes while reading,
// then stores them. The declared size is checked against MaxBytes and the
// stream is read one byte past that cap so an under-declared size fails too.
// A declared checksum is verified before the store is touched, so a mismatched
// header never leaves a stored object behind.
func (s *Service) Upload(ctx context.Context, req UploadRequest) (MediaObject, error) {
	if req.AccountID == "" {
		return MediaObject{}, apierror.New(apierror.CodeValidationFailed, 0, "account id is required")
	}
	if req.Size <= 0 {
		return MediaObject{}, apierror.New(apierror.CodeValidationFailed, 0, "media size must be greater than zero")
	}
	if s.cfg.MaxBytes > 0 && req.Size > s.cfg.MaxBytes {
		return MediaObject{}, apierror.New(apierror.CodeValidationFailed, 0,
			fmt.Sprintf("media size exceeds the %d byte limit", s.cfg.MaxBytes))
	}
	if !s.allowedType(req.ContentType) {
		return MediaObject{}, apierror.New(apierror.CodeValidationFailed, 0,
			fmt.Sprintf("content type %q is not allowed", req.ContentType))
	}
	if req.DurationMs < 0 {
		return MediaObject{}, apierror.New(apierror.CodeValidationFailed, 0, "duration must not be negative")
	}
	if s.cfg.MaxDurationMs > 0 && req.DurationMs > s.cfg.MaxDurationMs {
		return MediaObject{}, apierror.New(apierror.CodeValidationFailed, 0,
			fmt.Sprintf("media duration exceeds the %d ms limit", s.cfg.MaxDurationMs))
	}

	id, err := uuid.NewV7()
	if err != nil {
		return MediaObject{}, fmt.Errorf("generating media id: %w", err)
	}

	hash := sha256.New()
	reader := io.TeeReader(req.Body, hash)
	if s.cfg.MaxBytes > 0 {
		reader = io.LimitReader(reader, s.cfg.MaxBytes+1)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		return MediaObject{}, fmt.Errorf("reading media payload: %w", err)
	}
	if s.cfg.MaxBytes > 0 && int64(len(payload)) > s.cfg.MaxBytes {
		return MediaObject{}, apierror.New(apierror.CodeValidationFailed, 0,
			fmt.Sprintf("media size exceeds the %d byte limit", s.cfg.MaxBytes))
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if req.ExpectedChecksum != "" && !strings.EqualFold(digest, req.ExpectedChecksum) {
		return MediaObject{}, apierror.New(apierror.CodeValidationFailed, 0,
			"Checksum-SHA256 does not match the uploaded file.")
	}

	obj := MediaObject{
		ID:             id.String(),
		OwnerID:        req.AccountID,
		Kind:           req.Kind,
		ContentType:    req.ContentType,
		ByteSize:       int64(len(payload)),
		ChecksumSHA256: digest,
		DurationMs:     req.DurationMs,
		Storage:        MediaStorage(s.cfg.Driver),
		CreatedAt:      time.Now().UTC(),
	}

	if err := s.store.Put(ctx, obj, bytes.NewReader(payload)); err != nil {
		s.logger.ErrorContext(ctx, "media put failed", "id", obj.ID, "error", err)
		return MediaObject{}, err
	}
	publicURL, err := s.store.SignPublicURL(ctx, obj.ID)
	if err != nil {
		s.logger.ErrorContext(ctx, "media sign public url failed", "id", obj.ID, "error", err)
	} else {
		obj.PublicURL = publicURL
	}
	return obj, nil
}

// Get resolves one object for accountID. A stored object owned by another
// account is reported as missing, so the handler cannot confirm it exists.
func (s *Service) Get(ctx context.Context, accountID, id string) (MediaObject, io.ReadCloser, error) {
	obj, body, err := s.store.Get(ctx, id)
	if err != nil {
		return MediaObject{}, nil, err
	}
	if !ownsObject(accountID, obj) {
		_ = body.Close()
		return MediaObject{}, nil, apierror.New(apierror.CodeNotFound, 0, "Media object not found.")
	}
	return obj, body, nil
}

// Delete removes one object owned by accountID. A missing object is a no-op
// and a foreign object is a 404, so a retry after a lost response is safe.
func (s *Service) Delete(ctx context.Context, accountID, id string) error {
	obj, body, err := s.store.Get(ctx, id)
	if err != nil {
		if isMissing(err) {
			return nil
		}
		return err
	}
	if body != nil {
		_ = body.Close()
	}
	if !ownsObject(accountID, obj) {
		return apierror.New(apierror.CodeNotFound, 0, "Media object not found.")
	}
	return s.store.Delete(ctx, id)
}

// ownsObject reports whether accountID owns obj. An object with no recorded
// owner is never handed out, so a misconfigured store denies rather than leaks.
func ownsObject(accountID string, obj MediaObject) bool {
	return accountID != "" && obj.OwnerID != "" && obj.OwnerID == accountID
}

// isMissing reports whether err means the id was never stored. Backends wrap
// the miss in a NOT_FOUND error without an Unwrap chain, so the code is
// checked alongside the sentinel.
func isMissing(err error) bool {
	if errors.Is(err, errObjectMissing) {
		return true
	}
	var apiErr *apierror.Error
	return errors.As(err, &apiErr) && apiErr.Code == apierror.CodeNotFound
}

// PullFromURL accepts only a host under the verified PublicBaseURL. The caller
// supplies the URL, so an unverified host must never be fetched: this keeps the
// service off the SSRF paths in docs/RULLES/SSRF.md.
func (s *Service) PullFromURL(ctx context.Context, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return apierror.New(apierror.CodeValidationFailed, 0, "media url is not a valid url")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return apierror.New(apierror.CodeValidationFailed, 0,
			fmt.Sprintf("scheme %q is not allowed", parsed.Scheme))
	}
	if !s.verifiedHost(parsed.Hostname()) {
		return apierror.New(apierror.CodeValidationFailed, 0,
			fmt.Sprintf("host %q is not a verified media host", parsed.Hostname()))
	}
	return nil
}

// allowedType reports whether contentType appears in the configured allow list.
func (s *Service) allowedType(contentType string) bool {
	if contentType == "" || len(s.cfg.AllowedTypes) == 0 {
		return false
	}
	for _, allowed := range s.cfg.AllowedTypes {
		if strings.EqualFold(allowed, contentType) {
			return true
		}
	}
	return false
}

// verifiedHost compares the host against the base host on label boundaries, so
// a lookalike such as base.attacker.test never matches as a suffix.
func (s *Service) verifiedHost(host string) bool {
	base, err := url.Parse(s.cfg.PublicBaseURL)
	if err != nil || base.Hostname() == "" {
		return false
	}
	baseHost := base.Hostname()
	if host == "" {
		return false
	}
	if strings.EqualFold(host, baseHost) {
		return true
	}
	return strings.HasSuffix(strings.ToLower(host), "."+strings.ToLower(baseHost))
}
