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
	"context"
	"crypto/sha256"
	"encoding/hex"
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
func (s *Service) Upload(ctx context.Context, accountID string, kind MediaKind, contentType string, size int64, durationMs int64, r io.Reader) (MediaObject, error) {
	if accountID == "" {
		return MediaObject{}, apierror.New(apierror.CodeValidationFailed, 0, "account id is required")
	}
	if size <= 0 {
		return MediaObject{}, apierror.New(apierror.CodeValidationFailed, 0, "media size must be greater than zero")
	}
	if s.cfg.MaxBytes > 0 && size > s.cfg.MaxBytes {
		return MediaObject{}, apierror.New(apierror.CodeValidationFailed, 0,
			fmt.Sprintf("media size exceeds the %d byte limit", s.cfg.MaxBytes))
	}
	if !s.allowedType(contentType) {
		return MediaObject{}, apierror.New(apierror.CodeValidationFailed, 0,
			fmt.Sprintf("content type %q is not allowed", contentType))
	}
	if durationMs < 0 {
		return MediaObject{}, apierror.New(apierror.CodeValidationFailed, 0, "duration must not be negative")
	}
	if s.cfg.MaxDurationMs > 0 && durationMs > s.cfg.MaxDurationMs {
		return MediaObject{}, apierror.New(apierror.CodeValidationFailed, 0,
			fmt.Sprintf("media duration exceeds the %d ms limit", s.cfg.MaxDurationMs))
	}

	id, err := uuid.NewV7()
	if err != nil {
		return MediaObject{}, fmt.Errorf("generating media id: %w", err)
	}

	hash := sha256.New()
	reader := io.TeeReader(r, hash)
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

	obj := MediaObject{
		ID:             id.String(),
		Kind:           kind,
		ContentType:    contentType,
		ByteSize:       int64(len(payload)),
		ChecksumSHA256: hex.EncodeToString(hash.Sum(nil)),
		DurationMs:     durationMs,
		Storage:        MediaStorage(s.cfg.Driver),
		CreatedAt:      time.Now().UTC(),
	}

	if err := s.store.Put(ctx, obj, strings.NewReader(string(payload))); err != nil {
		s.logger.ErrorContext(ctx, "media put failed", "id", obj.ID, "error", err)
		return MediaObject{}, err
	}
	if publicURL, err := s.store.SignPublicURL(ctx, obj.ID); err == nil {
		obj.PublicURL = publicURL
	}
	return obj, nil
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
