// Package insight serves the privacy options the publish guard validates against.
//
// @file      internal/insight/creator.go
// @for       Cache-first creator privacy level lookup.
// @uses      context, time
// @reason    Publish rejects a privacy level outside the account's options, so this answers from the same snapshot the console reads.
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

// CreatorOptions returns the privacy levels the account may post with. It is
// the same cached snapshot CreatorInfo serves, so the two never disagree.
func (s *Service) CreatorOptions(ctx context.Context, operatorID, accountID string) ([]string, error) {
	info, err := s.CreatorInfo(ctx, operatorID, accountID)
	if err != nil {
		return nil, err
	}
	return info.PrivacyLevelOptions, nil
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
