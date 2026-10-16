// Package insight serves video reads and the stats snapshot history.
//
// @file      internal/insight/service.go
// @for       Video list, query, stats refresh, and creator profile reads.
// @uses      context, time, fmt, strconv, internal/apierror, internal/tiktokclient
// @reason    Keeps the upstream reads and the snapshot writes behind one rule set.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package insight

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// maxQueryIDs caps one video query, per 002 section 7.
const maxQueryIDs = 20

// Config carries the service dependencies and the cache window.
type Config struct {
	Videos     VideoClient
	Accounts   AccountResolver
	Store      SnapshotStore
	Now        func() time.Time
	CreatorTTL time.Duration
}

// Service reads videos upstream and keeps the snapshot history.
type Service struct {
	cfg Config
}

// NewService wires the insight service, defaulting the clock and the cache window.
func NewService(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{cfg: cfg}
}

// access resolves the operator's own account to an access token, so ownership
// is proven before any upstream call happens.
func (s *Service) access(ctx context.Context, operatorID, accountID string) (string, error) {
	if _, err := uuid.Parse(accountID); err != nil {
		return "", apierror.New(apierror.CodeValidationFailed, 0, "The account id is invalid.").
			Wrap(fmt.Errorf("insight: parsing account id: %w", err))
	}
	return s.cfg.Accounts.Owned(ctx, operatorID, accountID)
}

// errVideoNotFound is the answer when upstream does not return the id asked for.
func errVideoNotFound(videoID string) *apierror.Error {
	return apierror.New(apierror.CodeNotFound, 0,
		fmt.Sprintf("Video %s was not found on this account.", videoID))
}

// ListVideos reads one page of the account's videos.
func (s *Service) ListVideos(ctx context.Context, operatorID, accountID, cursor string, maxCount int) (VideoPage, error) {
	access, err := s.access(ctx, operatorID, accountID)
	if err != nil {
		return VideoPage{}, err
	}
	page, err := s.cfg.Videos.ListVideos(ctx, access, cursor, maxCount)
	if err != nil {
		return VideoPage{}, err
	}
	now := s.cfg.Now()
	views := make([]VideoView, 0, len(page.Videos))
	for _, video := range page.Videos {
		views = append(views, viewVideo(video, now))
	}
	out := VideoPage{Videos: views, HasMore: page.HasMore}
	if page.Cursor > 0 {
		out.Cursor = strconv.FormatInt(page.Cursor, 10)
	}
	return out, nil
}

// QueryVideos reads specific videos. More than maxQueryIDs ids is rejected
// before the upstream call, so a wide request costs nothing when it fails.
func (s *Service) QueryVideos(ctx context.Context, operatorID, accountID string, ids []string) ([]VideoView, error) {
	if len(ids) == 0 {
		return nil, apierror.New(apierror.CodeValidationFailed, 0, "At least one video id is required.")
	}
	if len(ids) > maxQueryIDs {
		return nil, apierror.New(apierror.CodeValidationFailed, 0,
			"At most 20 video ids may be queried at once.")
	}
	access, err := s.access(ctx, operatorID, accountID)
	if err != nil {
		return nil, err
	}
	videos, err := s.cfg.Videos.QueryVideos(ctx, access, ids, videoQueryFields)
	if err != nil {
		return nil, err
	}
	now := s.cfg.Now()
	views := make([]VideoView, 0, len(videos))
	for _, video := range videos {
		views = append(views, viewVideo(video, now))
	}
	return views, nil
}

// Video reads one video through a query, which is how a stale cover URL gets
// refreshed before the console displays it.
func (s *Service) Video(ctx context.Context, operatorID, accountID, videoID string) (VideoView, error) {
	if videoID == "" {
		return VideoView{}, apierror.New(apierror.CodeValidationFailed, 0, "A video id is required.")
	}
	access, err := s.access(ctx, operatorID, accountID)
	if err != nil {
		return VideoView{}, err
	}
	videos, err := s.cfg.Videos.QueryVideos(ctx, access, []string{videoID}, videoQueryFields)
	if err != nil {
		return VideoView{}, err
	}
	if len(videos) == 0 || videos[0].ID != videoID {
		return VideoView{}, errVideoNotFound(videoID)
	}
	return viewVideo(videos[0], s.cfg.Now()), nil
}
