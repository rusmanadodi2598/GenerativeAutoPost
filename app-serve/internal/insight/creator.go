// Package insight serves the privacy options the publish guard validates against.
//
// @file      internal/insight/creator.go
// @for       Cache-first creator privacy level lookup.
// @uses      context, time, internal/tiktokclient
// @reason    Publish rejects a privacy level outside the account's options, so this answers from the cache first and refreshes inside one window.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-15
package insight

import (
	"context"
	"time"
)

// defaultCreatorTTL is the cache window for privacy options.
const defaultCreatorTTL = 5 * time.Minute

// CreatorOptions returns the privacy levels the account may post with. A
// snapshot inside the window answers without an upstream call; a stale one is
// refetched, and a refetch failure falls back to the stored options so a TikTok
// outage does not block every init.
func (s *Service) CreatorOptions(ctx context.Context, operatorID, accountID string) ([]string, error) {
	stored, storeErr := s.cfg.Store.LatestCreatorSnapshot(ctx, accountID)
	if storeErr == nil && s.creatorFresh(stored.FetchedAt) {
		return stored.PrivacyOptions, nil
	}
	access, err := s.access(ctx, operatorID, accountID)
	if err != nil {
		return nil, err
	}
	creator, err := s.cfg.Videos.QueryCreatorInfo(ctx, access)
	if err != nil {
		if storeErr == nil {
			return stored.PrivacyOptions, nil
		}
		return nil, err
	}
	options := creator.PrivacyLevelOptions
	if options == nil {
		options = []string{}
	}
	_, _ = s.cfg.Store.InsertCreatorSnapshot(ctx, CreatorSnapshot{
		AccountID:               accountID,
		PrivacyOptions:          options,
		CommentDisabled:         creator.CommentDisabled,
		DuetDisabled:            creator.DuetDisabled,
		StitchDisabled:          creator.StitchDisabled,
		MaxVideoPostDurationSec: creator.MaxVideoPostDurationSec,
		FetchedAt:               s.cfg.Now(),
	})
	return options, nil
}

// creatorFresh reports whether a snapshot is inside the cache window.
func (s *Service) creatorFresh(fetchedAt time.Time) bool {
	return s.cfg.Now().Sub(fetchedAt) < s.creatorTTL()
}

// creatorTTL returns the configured privacy option window or the default.
func (s *Service) creatorTTL() time.Duration {
	if s.cfg.CreatorTTL > 0 {
		return s.cfg.CreatorTTL
	}
	return defaultCreatorTTL
}
