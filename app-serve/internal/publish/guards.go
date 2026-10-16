// Package publish checks the init guard rails shared by every post type.
//
// @file      internal/publish/guards.go
// @for       Privacy, brand, URL, idempotency, and TikTok mapping helpers.
// @uses      context, encoding/json, internal/apierror, internal/tiktokclient
// @reason    Keeps the init flows on one screen by moving guards and mappers out.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/idempotency"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// claimVideo claims the key for a video init body. The wrapper exists so the
// hash is taken over a concrete schema struct rather than an untyped value.
func (s *Service) claimVideo(ctx context.Context, key string, req InitVideoRequest) (string, *InitResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", nil, fmt.Errorf("publish: encoding init body: %w", err)
	}
	return s.begin(ctx, key, body)
}

// claimPhoto claims the key for a photo init body. The wrapper exists so the
// hash is taken over a concrete schema struct rather than an untyped value.
func (s *Service) claimPhoto(ctx context.Context, key string, req InitPhotoRequest) (string, *InitResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", nil, fmt.Errorf("publish: encoding init body: %w", err)
	}
	return s.begin(ctx, key, body)
}

// claimDraft claims the key for a draft init body. The wrapper exists so the
// hash is taken over a concrete schema struct rather than an untyped value.
func (s *Service) claimDraft(ctx context.Context, key string, req InitDraftRequest) (string, *InitResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", nil, fmt.Errorf("publish: encoding init body: %w", err)
	}
	return s.begin(ctx, key, body)
}

// begin claims an already-encoded body. A same key and same body repeat
// replays the stored response; a changed body is a 409.
func (s *Service) begin(ctx context.Context, key string, body []byte) (string, *InitResult, error) {
	hash := idempotency.BodyHash(body)
	raw, err := s.idempotency.Begin(ctx, key, hash)
	if errors.Is(err, idempotency.ErrKeyReused) {
		return "", nil, apierror.New(apierror.CodeIdempotencyKeyReused, 409, "Idempotency key was already used with a different request body.")
	}
	if err != nil {
		return "", nil, err
	}
	if raw == nil {
		return hash, nil, nil
	}
	var replay InitResult
	if err := json.Unmarshal(raw, &replay); err != nil {
		return "", nil, fmt.Errorf("publish: decoding replayed init: %w", err)
	}
	return hash, &replay, nil
}

// finish persists the idempotency outcome after the attempt row is stored.
func (s *Service) finish(ctx context.Context, key, hash string, res InitResult) (InitResult, error) {
	raw, err := json.Marshal(res)
	if err != nil {
		return InitResult{}, fmt.Errorf("publish: encoding init result: %w", err)
	}
	if err := s.idempotency.Complete(ctx, key, hash, raw); err != nil {
		return InitResult{}, err
	}
	return res, nil
}

// checkPrivacy requires a privacy level drawn from the account options.
// Ownership is already resolved by the caller, so this only reads the options
// and compares.
func (s *Service) checkPrivacy(ctx context.Context, operatorID, accountID, level string) error {
	options, err := s.creators.Options(ctx, operatorID, accountID)
	if err != nil {
		return err
	}
	for _, opt := range options {
		if level != "" && opt == level {
			return nil
		}
	}
	return apierror.New(apierror.CodePublishPrivacyMismatch, 0, "Privacy level is not one of the account options.")
}

// checkMediaURL rejects a pull URL whose host is not the verified media host.
func (s *Service) checkMediaURL(source, raw string) error {
	if source != sourcePullFromURL {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return apierror.New(apierror.CodeValidationFailed, 0, "Media URL is not a valid http(s) URL.")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "" && (host == s.mediaHost || strings.HasSuffix(host, "."+s.mediaHost)) {
		return nil
	}
	return apierror.New(apierror.CodeTikTokURLNotVerified, 0, "Media URL host is not a verified media host.")
}

// checkBrand rejects a branded flag paired with SELF_ONLY visibility.
func checkBrand(info PostInfoRequest) error {
	if (info.BrandContentToggle || info.BrandOrganicToggle) && info.PrivacyLevel == privacySelfOnly {
		return apierror.New(apierror.CodeValidationFailed, 0, "Branded content cannot use self only visibility.")
	}
	return nil
}

// toPostInfo maps the schema block to the TikTok wire shape with a server set flag.
func toPostInfo(info PostInfoRequest, generated bool) tiktokclient.PostInfo {
	return tiktokclient.PostInfo{
		Title: info.Title, PrivacyLevel: info.PrivacyLevel,
		DisableDuet: info.DisableDuet, DisableComment: info.DisableComment,
		DisableStitch: info.DisableStitch, VideoCoverTimestampMS: info.VideoCoverTimestampMs,
		BrandContentToggle: info.BrandContentToggle, BrandOrganicToggle: info.BrandOrganicToggle,
		IsAIGC: generated,
	}
}

// toPhotoInfo maps the photo schema block with a server set flag.
func toPhotoInfo(info PhotoInfoRequest, generated bool) tiktokclient.PhotoPostInfo {
	return tiktokclient.PhotoPostInfo{
		PostInfo:    toPostInfo(info.PostInfoRequest, generated),
		Description: info.Description, AutoAddMusic: info.AutoAddMusic,
	}
}

// toSourceInfo maps the source and optional pull URL to the TikTok shape.
func toSourceInfo(source, mediaURL string) tiktokclient.SourceInfo {
	out := tiktokclient.SourceInfo{Source: source}
	if source == sourcePullFromURL {
		out.VideoURL = mediaURL
	}
	return out
}

// invalid maps a validator failure to a client safe validation error.
func invalid(err error) error {
	return apierror.New(apierror.CodeValidationFailed, 0, "Init request failed validation.").Wrap(err)
}

// deref unwraps a replayed result or returns the zero value on a fresh claim.
func deref(replay *InitResult) InitResult {
	if replay == nil {
		return InitResult{}
	}
	return *replay
}
