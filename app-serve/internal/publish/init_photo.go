// Package publish validates photo, draft, and video inits before they reach TikTok.
//
// @file      internal/publish/init_photo.go
// @for       Photo, draft, and video init validation orchestration.
// @uses      context, internal/apierror, internal/tiktokclient
// @reason    Splits the three init flows so each file holds one concern.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package publish

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// InitPhoto validates and starts a direct photo post or a photo upload
// session. Privacy is required for DIRECT_POST only.
func (s *Service) InitPhoto(ctx context.Context, operatorID string, req InitPhotoRequest, generated bool, key string) (InitResult, error) {
	var empty InitResult
	if err := s.validate.Struct(req); err != nil {
		return empty, invalid(err)
	}
	hash, replay, err := s.claimPhoto(ctx, key, req)
	if err != nil || replay != nil {
		return deref(replay), err
	}
	if req.PostMode == modeDirectPost {
		if req.PostInfo.PrivacyLevel == "" {
			return empty, apierror.New(apierror.CodeValidationFailed, 0, "Privacy level is required for direct posts.")
		}
	}
	// Ownership resolves before the privacy verdict, so a foreign account
	// answers NOT_FOUND instead of revealing which levels it supports.
	access, err := s.accounts.Owned(ctx, operatorID, req.AccountID)
	if err != nil {
		return empty, err
	}
	if req.PostMode == modeDirectPost {
		if err := s.checkPrivacy(ctx, operatorID, req.AccountID, req.PostInfo.PrivacyLevel); err != nil {
			return empty, err
		}
	}
	if n := utf16Len(req.PostInfo.Title); n > maxPhotoTitleLen {
		return empty, apierror.New(apierror.CodeValidationFailed, 0, "Title exceeds the 90 character limit.")
	}
	if n := utf16Len(req.PostInfo.Description); n > maxPhotoDescLen {
		return empty, apierror.New(apierror.CodeValidationFailed, 0, "Description exceeds the 4000 character limit.")
	}
	if req.PostInfo.AutoAddMusic && req.PostMode != modeDirectPost {
		return empty, apierror.New(apierror.CodeValidationFailed, 0, "Auto add music is only allowed for direct posts.")
	}
	if err := checkBrand(req.PostInfo.PostInfoRequest); err != nil {
		return empty, err
	}
	upstream, err := s.poster.InitPhotoPost(ctx, access, tiktokclient.PhotoInitRequest{
		PostInfo:    toPhotoInfo(req.PostInfo, generated),
		PostMode:    req.PostMode,
		MediaType:   "PHOTO",
		PhotoImages: req.PhotoURLs,
		SourceInfo:  tiktokclient.SourceInfo{Source: sourceFileUpload},
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
		PostMode: req.PostMode, MediaType: "PHOTO", Source: sourceFileUpload,
		Status: StatusCreated, PrivacyLevel: req.PostInfo.PrivacyLevel,
		Title: req.PostInfo.Title, Description: req.PostInfo.Description,
	})
	if err != nil {
		return empty, err
	}
	return s.finish(ctx, key, hash, InitResult{
		ID: stored.ID, PublishID: upstream.PublishID, Status: StatusCreated,
		UploadPlan: UploadPlan{Source: sourceFileUpload, ChunkSize: defaultChunkSize, TotalChunks: 1},
	})
}

// InitDraft validates and starts an inbox draft upload. The draft body carries
// no privacy info, so it routes straight to the draft endpoint.
func (s *Service) InitDraft(ctx context.Context, operatorID string, req InitDraftRequest, key string) (InitResult, error) {
	var empty InitResult
	if err := s.validate.Struct(req); err != nil {
		return empty, invalid(err)
	}
	hash, replay, err := s.claimDraft(ctx, key, req)
	if err != nil || replay != nil {
		return deref(replay), err
	}
	// Ownership resolves first: an unverified URL is a property of the request,
	// while the ownership verdict is the one that must not vary by operator.
	access, err := s.accounts.Owned(ctx, operatorID, req.AccountID)
	if err != nil {
		return empty, err
	}
	if err := s.checkMediaURL(req.Source, req.MediaURL); err != nil {
		return empty, err
	}
	upstream, err := s.poster.InitDraftUpload(ctx, access, tiktokclient.DraftInitRequest{
		SourceInfo: toSourceInfo(req.Source, req.MediaURL),
	})
	if err != nil {
		return empty, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return empty, fmt.Errorf("publish: generating attempt id: %w", err)
	}
	draft := NewDraft(id.String(), req.AccountID, upstream.PublishID)
	stored, err := s.attempts.Insert(ctx, Record{
		ID: draft.ID(), AccountID: req.AccountID, PublishID: upstream.PublishID,
		PostMode: "DRAFT", MediaType: "VIDEO", Source: req.Source, Status: draft.Status(),
	})
	if err != nil {
		return empty, err
	}
	return s.finish(ctx, key, hash, InitResult{
		ID: stored.ID, PublishID: upstream.PublishID, Status: draft.Status(),
		UploadPlan: UploadPlan{Source: req.Source, ChunkSize: defaultChunkSize, TotalChunks: 1},
	})
}

// InitVideo validates and starts a direct video post or a media upload
// session. Generated server sets is_aigc; the client cannot turn it off.
func (s *Service) InitVideo(ctx context.Context, operatorID string, req InitVideoRequest, generated bool, key string) (InitResult, error) {
	var empty InitResult
	if err := s.validate.Struct(req); err != nil {
		return empty, invalid(err)
	}
	hash, replay, err := s.claimVideo(ctx, key, req)
	if err != nil || replay != nil {
		return deref(replay), err
	}
	if req.PostInfo.PrivacyLevel == "" {
		return empty, apierror.New(apierror.CodeValidationFailed, 0, "Privacy level is required for video posts.")
	}
	// Ownership resolves before the privacy verdict, so a foreign account
	// answers NOT_FOUND instead of revealing which levels it supports.
	access, err := s.accounts.Owned(ctx, operatorID, req.AccountID)
	if err != nil {
		return empty, err
	}
	if err := s.checkPrivacy(ctx, operatorID, req.AccountID, req.PostInfo.PrivacyLevel); err != nil {
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
