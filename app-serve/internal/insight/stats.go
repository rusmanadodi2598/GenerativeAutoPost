// Package insight appends and reads the video stats and creator snapshots.
//
// @file      internal/insight/stats.go
// @for       Stats fetch, snapshot append, and the creator profile read.
// @uses      context, time, internal/apierror, internal/tiktokclient
// @reason    Snapshot writes are inserts, so the history grows instead of being rewritten.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package insight

import (
	"context"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// VideoClient is the upstream read slice insight needs.
type VideoClient interface {
	ListVideos(ctx context.Context, access, cursor string, maxCount int) (tiktokclient.VideoPage, error)
	QueryVideos(ctx context.Context, access string, ids, fields []string) ([]tiktokclient.Video, error)
	GetUserInfo(ctx context.Context, access string, fields []string) (tiktokclient.UserInfo, error)
	QueryCreatorInfo(ctx context.Context, access string) (tiktokclient.CreatorInfo, error)
}

// AccountResolver returns the access token for an account the operator owns.
type AccountResolver interface {
	Owned(ctx context.Context, operatorID, accountID string) (access string, err error)
}

// SnapshotStore is the repository slice the service reads and appends.
type SnapshotStore interface {
	InsertStats(ctx context.Context, snap StatSnapshot) (StatSnapshot, error)
	LatestStats(ctx context.Context, videoID string) (StatSnapshot, error)
	ListStats(ctx context.Context, videoID string, since time.Time, limit int) ([]StatSnapshot, bool, error)
	InsertCreatorSnapshot(ctx context.Context, rec CreatorSnapshot) (CreatorSnapshot, error)
	LatestCreatorSnapshot(ctx context.Context, accountID string) (CreatorSnapshot, error)
}

// fetchStats reads one video upstream and appends the snapshot.
func (s *Service) fetchStats(ctx context.Context, access, videoID string) (StatSnapshot, error) {
	videos, err := s.cfg.Videos.QueryVideos(ctx, access, []string{videoID}, videoQueryFields)
	if err != nil {
		return StatSnapshot{}, err
	}
	if len(videos) == 0 || videos[0].ID != videoID {
		return StatSnapshot{}, errVideoNotFound(videoID)
	}
	return s.cfg.Store.InsertStats(ctx, snapshotFromVideo(videos[0], s.cfg.Now()))
}

// VideoStats serves the newest snapshot, or fetches one when there is none or
// the stored point is older than the cache window. A stale point that cannot
// be refreshed is still served: its fetched_at is in the body, so the console
// can show the data as old rather than losing it to an upstream outage.
func (s *Service) VideoStats(ctx context.Context, operatorID, accountID, videoID string) (StatSnapshot, error) {
	access, err := s.access(ctx, operatorID, accountID)
	if err != nil {
		return StatSnapshot{}, err
	}
	stored, storeErr := s.cfg.Store.LatestStats(ctx, videoID)
	if storeErr == nil && s.cfg.Now().Sub(stored.FetchedAt) < s.cfg.StatsTTL {
		return stored, nil
	}
	fetched, err := s.fetchStats(ctx, access, videoID)
	if err != nil && storeErr == nil {
		return stored, nil
	}
	return fetched, err
}

// StatsSeries reads the stored history for one window. Only the newest point is
// freshness checked, so reading a chart appends at most one point and never
// refetches history: the console can read a series as often as it likes
// without spending TikTok status quota.
func (s *Service) StatsSeries(ctx context.Context, operatorID, accountID, videoID string, window StatRange) (StatSeries, error) {
	if _, err := s.access(ctx, operatorID, accountID); err != nil {
		return StatSeries{}, err
	}
	days, ok := statRangeDays[window]
	if !ok {
		return StatSeries{}, apierror.New(apierror.CodeValidationFailed, 0,
			"Stats range must be one of 7d, 30d, 90d.")
	}
	latest, err := s.VideoStats(ctx, operatorID, accountID, videoID)
	if err != nil {
		return StatSeries{}, err
	}
	since := s.cfg.Now().AddDate(0, 0, -days)
	points, truncated, err := s.cfg.Store.ListStats(ctx, videoID, since, maxSeriesPoints)
	if err != nil {
		return StatSeries{}, err
	}
	if len(points) == 0 {
		// The newest point can predate the window; surfacing it keeps the
		// console showing the last known value rather than an empty chart.
		points = append(points, latest)
	}
	return StatSeries{VideoID: videoID, Range: window, Points: points, Truncated: truncated}, nil
}

// RefreshStats always fetches and appends, so the series keeps a point per
// manual request rather than only per cache miss.
func (s *Service) RefreshStats(ctx context.Context, operatorID, accountID, videoID string) (StatSnapshot, error) {
	access, err := s.access(ctx, operatorID, accountID)
	if err != nil {
		return StatSnapshot{}, err
	}
	return s.fetchStats(ctx, access, videoID)
}

// Profile reads the account counts and the postable creator record, and caches
// the latter so the publish forms keep their privacy options available.
func (s *Service) Profile(ctx context.Context, operatorID, accountID string) (ProfileView, error) {
	access, err := s.access(ctx, operatorID, accountID)
	if err != nil {
		return ProfileView{}, err
	}
	info, err := s.cfg.Videos.GetUserInfo(ctx, access, userInfoFields)
	if err != nil {
		return ProfileView{}, err
	}
	creator, err := s.CreatorInfo(ctx, operatorID, accountID)
	if err != nil {
		return ProfileView{}, err
	}
	return ProfileView{
		OpenID:                  info.OpenID,
		DisplayName:             info.DisplayName,
		AvatarURL:               info.AvatarURL,
		BioDescription:          info.BioDescription,
		ProfileDeepLink:         info.ProfileDeepLink,
		FollowerCount:           ptr(info.FollowerCount),
		FollowingCount:          ptr(info.FollowingCount),
		LikesCount:              ptr(info.LikesCount),
		VideoCount:              ptr(info.VideoCount),
		CreatorUsername:         creator.CreatorUsername,
		PrivacyLevelOptions:     creator.PrivacyLevelOptions,
		CommentDisabled:         creator.CommentDisabled,
		DuetDisabled:            creator.DuetDisabled,
		StitchDisabled:          creator.StitchDisabled,
		MaxVideoPostDurationSec: creator.MaxVideoPostDurationSec,
		FetchedAt:               creator.FetchedAt,
	}, nil
}
