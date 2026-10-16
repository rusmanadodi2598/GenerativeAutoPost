// Package insight serves the creator record the console publish form reads.
//
// @file      internal/insight/creator_info.go
// @for       Cache-first read of the full creator profile and privacy record.
// @uses      context, internal/tiktokclient
// @reason    SPEC 003 7.2 serves creator info from a short cache, so the profile and the options must come from one snapshot.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-16
package insight

import (
	"context"
	"time"
)

// CreatorInfoView is the creator record as the console renders it. It is the
// snapshot fields, so a cached answer and a fresh answer have the same shape.
type CreatorInfoView struct {
	CreatorUsername         string    `json:"creator_username"`
	CreatorNickname         string    `json:"creator_nickname"`
	CreatorAvatarURL        string    `json:"creator_avatar_url"`
	PrivacyLevelOptions     []string  `json:"privacy_level_options"`
	CommentDisabled         bool      `json:"comment_disabled"`
	DuetDisabled            bool      `json:"duet_disabled"`
	StitchDisabled          bool      `json:"stitch_disabled"`
	MaxVideoPostDurationSec int       `json:"max_video_post_duration_sec"`
	FetchedAt               time.Time `json:"fetched_at"`
}

// CreatorInfo returns the creator profile and its privacy options. Ownership is
// checked before the cache is read, so a foreign account id is not confirmed by
// the fact that a snapshot exists for it.
func (s *Service) CreatorInfo(ctx context.Context, operatorID, accountID string) (CreatorInfoView, error) {
	stored, storeErr := s.cfg.Store.LatestCreatorSnapshot(ctx, accountID)
	if storeErr == nil && s.creatorFresh(stored.FetchedAt) {
		return viewCreator(stored), nil
	}
	access, err := s.access(ctx, operatorID, accountID)
	if err != nil {
		return CreatorInfoView{}, err
	}
	creator, err := s.cfg.Videos.QueryCreatorInfo(ctx, access)
	if err != nil {
		if storeErr == nil {
			return viewCreator(stored), nil
		}
		return CreatorInfoView{}, err
	}
	options := creator.PrivacyLevelOptions
	if options == nil {
		options = []string{}
	}
	// The snapshot is a cache for the publish form, not the answer to this
	// request, so a failed write must not fail a read TikTok already answered.
	_, _ = s.cfg.Store.InsertCreatorSnapshot(ctx, CreatorSnapshot{
		AccountID:               accountID,
		PrivacyOptions:          options,
		CommentDisabled:         creator.CommentDisabled,
		DuetDisabled:            creator.DuetDisabled,
		StitchDisabled:          creator.StitchDisabled,
		MaxVideoPostDurationSec: creator.MaxVideoPostDurationSec,
		CreatorUsername:         creator.CreatorUsername,
		CreatorNickname:         creator.CreatorNickname,
		CreatorAvatarURL:        creator.CreatorAvatarURL,
		FetchedAt:               s.cfg.Now(),
	})
	return CreatorInfoView{
		CreatorUsername:         creator.CreatorUsername,
		CreatorNickname:         creator.CreatorNickname,
		CreatorAvatarURL:        creator.CreatorAvatarURL,
		PrivacyLevelOptions:     options,
		CommentDisabled:         creator.CommentDisabled,
		DuetDisabled:            creator.DuetDisabled,
		StitchDisabled:          creator.StitchDisabled,
		MaxVideoPostDurationSec: creator.MaxVideoPostDurationSec,
		FetchedAt:               s.cfg.Now(),
	}, nil
}

// viewCreator maps one cached snapshot to the wire shape, keeping the original
// fetched_at so the console can judge how stale the options are.
func viewCreator(snap CreatorSnapshot) CreatorInfoView {
	options := snap.PrivacyOptions
	if options == nil {
		options = []string{}
	}
	return CreatorInfoView{
		CreatorUsername:         snap.CreatorUsername,
		CreatorNickname:         snap.CreatorNickname,
		CreatorAvatarURL:        snap.CreatorAvatarURL,
		PrivacyLevelOptions:     options,
		CommentDisabled:         snap.CommentDisabled,
		DuetDisabled:            snap.DuetDisabled,
		StitchDisabled:          snap.StitchDisabled,
		MaxVideoPostDurationSec: snap.MaxVideoPostDurationSec,
		FetchedAt:               snap.FetchedAt,
	}
}
