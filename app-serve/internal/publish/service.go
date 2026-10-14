// Package publish validates publish inits before they reach TikTok.
//
// @file      internal/publish/service.go
// @for       Video init validation orchestration and shared service types.
// @uses      context, encoding/json, internal/apierror, internal/tiktokclient
// @reason    Centralizes the 003 section 7.4 rules so handlers stay thin.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package publish

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// Length caps are counted in UTF-16 code units, the unit TikTok enforces.
const (
	maxVideoTitleLen  = 2200
	maxPhotoTitleLen  = 90
	maxPhotoDescLen   = 4000
	defaultChunkSize  = 10 << 20
	privacySelfOnly   = "SELF_ONLY"
	sourcePullFromURL = "PULL_FROM_URL"
	sourceFileUpload  = "FILE_UPLOAD"
	modeDirectPost    = "DIRECT_POST"
)

// Poster is the TikTok client slice this service needs. The narrow shape lets
// *tiktokclient.Client satisfy it with no wrapper.
type Poster interface {
	QueryCreatorInfo(ctx context.Context, access string) (tiktokclient.CreatorInfo, error)
	InitVideoPost(ctx context.Context, access string, req tiktokclient.VideoInitRequest) (tiktokclient.VideoInitResponse, error)
	InitPhotoPost(ctx context.Context, access string, req tiktokclient.PhotoInitRequest) (tiktokclient.PhotoInitResponse, error)
	InitDraftUpload(ctx context.Context, access string, req tiktokclient.DraftInitRequest) (tiktokclient.DraftInitResponse, error)
	FetchStatus(ctx context.Context, access string, publishID string) (tiktokclient.PublishStatus, error)
}

// AccountResolver returns the access token for an account the operator owns.
type AccountResolver interface {
	Owned(ctx context.Context, operatorID string, accountID string) (access string, err error)
}

// CreatorOptions returns the current privacy_level_options for the account.
type CreatorOptions interface {
	Options(ctx context.Context, accountID string) ([]string, error)
}

// AttemptWriter persists one attempt row. *Repository satisfies it directly.
type AttemptWriter interface {
	Insert(ctx context.Context, rec Record) (Record, error)
}

// IdempotencyClaimer claims one outcome per key and body hash.
// *IdempotencyStore satisfies it directly.
type IdempotencyClaimer interface {
	Begin(ctx context.Context, key, bodyHash string) ([]byte, error)
	Complete(ctx context.Context, key, bodyHash string, response []byte) error
}

var (
	_ Poster             = (*tiktokclient.Client)(nil)
	_ AttemptWriter      = (*Repository)(nil)
	_ IdempotencyClaimer = (*IdempotencyStore)(nil)
)

// UploadPlan tells the caller how to deliver the bytes. It never carries the
// upstream upload URL, which stays server side.
type UploadPlan struct {
	Source      string `json:"source"`
	ChunkSize   int64  `json:"chunk_size,omitempty"`
	TotalChunks int    `json:"total_chunks,omitempty"`
}

// InitResult is the stored 202 shape for one init: the attempt row identity,
// the TikTok publish id, the attempt status, and the upload plan.
type InitResult struct {
	ID         string     `json:"id"`
	PublishID  string     `json:"publish_id"`
	Status     Status     `json:"status"`
	UploadPlan UploadPlan `json:"upload_plan"`
}

// Service enforces the init validation rules in 003 section 7.4 order. It
// never writes tokens, so no credential writes happen here.
type Service struct {
	poster      Poster
	accounts    AccountResolver
	creators    CreatorOptions
	attempts    AttemptWriter
	idempotency IdempotencyClaimer
	mediaHost   string
	validate    *validator.Validate
}

// Config wires one Service. MediaHost is the verified PULL_FROM_URL host.
type Config struct {
	Poster      Poster
	Accounts    AccountResolver
	Creators    CreatorOptions
	Attempts    AttemptWriter
	Idempotency IdempotencyClaimer
	MediaHost   string
}

// NewService wires validation and its collaborators together.
func NewService(cfg Config) *Service {
	return &Service{
		poster: cfg.Poster, accounts: cfg.Accounts, creators: cfg.Creators,
		attempts: cfg.Attempts, idempotency: cfg.Idempotency,
		mediaHost: strings.ToLower(cfg.MediaHost), validate: validator.New(),
	}
}

// InitVideo validates and starts a direct video post or a media upload
// session. Generated server sets is_aigc; the client cannot turn it off.
func (s *Service) InitVideo(ctx context.Context, operatorID string, req InitVideoRequest, generated bool, key string) (InitResult, error) {
	var empty InitResult
	if err := s.validate.Struct(req); err != nil {
		return empty, invalid(err)
	}
	hash, replay, err := s.claim(ctx, key, req)
	if err != nil || replay != nil {
		return deref(replay), err
	}
	if req.PostInfo.PrivacyLevel == "" {
		return empty, apierror.New(apierror.CodeValidationFailed, 0, "Privacy level is required for video posts.")
	}
	if err := s.checkPrivacy(ctx, req.AccountID, req.PostInfo.PrivacyLevel); err != nil {
		return empty, err
	}
	if n := utf16Len(req.PostInfo.Title); n > maxVideoTitleLen {
		return empty, apierror.New(apierror.CodeValidationFailed, 0, "Title exceeds the 2200 character limit.")
	}
	if err := checkBrand(req.PostInfo); err != nil {
		return empty, err
	}
	if err := s.checkMediaURL(req.Source, req.MediaURL); err != nil {
		return empty, err
	}
	access, err := s.accounts.Owned(ctx, operatorID, req.AccountID)
	if err != nil {
		return empty, err
	}
	upstream, err := s.poster.InitVideoPost(ctx, access, tiktokclient.VideoInitRequest{
		PostInfo:   toPostInfo(req.PostInfo, generated),
		SourceInfo: toSourceInfo(req.Source, req.MediaURL),
	})
	if err != nil {
		return empty, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return empty, fmt.Errorf("publish: generating attempt id: %w", err)
	}
	stored, err := s.attempts.Insert(ctx, Record{
		ID: id.String(), AccountID: req.AccountID, PublishID: upstream.PublishID,
		PostMode: modeDirectPost, MediaType: "VIDEO", Source: req.Source,
		Status: StatusCreated, PrivacyLevel: req.PostInfo.PrivacyLevel, Title: req.PostInfo.Title,
	})
	if err != nil {
		return empty, err
	}
	return s.finish(ctx, key, hash, InitResult{
		ID: stored.ID, PublishID: upstream.PublishID, Status: StatusCreated,
		UploadPlan: UploadPlan{Source: req.Source, ChunkSize: defaultChunkSize, TotalChunks: 1},
	})
}
