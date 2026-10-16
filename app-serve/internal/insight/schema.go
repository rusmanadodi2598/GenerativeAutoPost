// Package insight holds the video stats and creator snapshot shapes.
//
// @file      internal/insight/schema.go
// @for       View types for video stats and creator info snapshots.
// @uses      time
// @reason    Snapshot rows are append-only history, so the read side is a plain view.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     schema
// @stability stable
// @since     2026-10-12
package insight

import (
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// StatSnapshot is one point of the video stats time series.
type StatSnapshot struct {
	ID           string    `json:"id"`
	VideoID      string    `json:"video_id"`
	ViewCount    *int64    `json:"view_count,omitempty"`
	LikeCount    *int64    `json:"like_count,omitempty"`
	CommentCount *int64    `json:"comment_count,omitempty"`
	ShareCount   *int64    `json:"share_count,omitempty"`
	FetchedAt    time.Time `json:"fetched_at"`
}

// StatRange is one of the closed window choices the stats series accepts.
// Point density follows the worker tick, so 7d is the only window retention can fill.
type StatRange string

// statRangeMessage is the single client copy for an unsupported range.
const statRangeMessage = "Stats range must be 7d."

// Supported stats windows. A longer range is a contract change, not a value.
const (
	StatRange7d StatRange = "7d"
)

// statRangeDays widths one range to the number of days the series covers.
var statRangeDays = map[StatRange]int{
	StatRange7d: 7,
}

// maxSeriesPoints caps one series response so a long history never becomes an
// unbounded read.
const maxSeriesPoints = 500

// parseRange maps the raw query value to a window, defaulting to 7 days.
// An unknown value is rejected rather than silently defaulted, so a client
// typo cannot hide behind data it did not ask for.
func parseRange(raw string) (StatRange, error) {
	if raw == "" {
		return StatRange7d, nil
	}
	window := StatRange(raw)
	if _, ok := statRangeDays[window]; !ok {
		return "", apierror.New(apierror.CodeValidationFailed, 0, statRangeMessage)
	}
	return window, nil
}

// StatSeries is the windowed stats answer: the covered window, the points in
// ascending fetched_at order, and whether the point cap cut older history off.
type StatSeries struct {
	VideoID   string         `json:"video_id"`
	Range     StatRange      `json:"range"`
	Points    []StatSnapshot `json:"points"`
	Truncated bool           `json:"truncated"`
}

// CreatorSnapshot is one cached view of a creator privacy and profile record.
type CreatorSnapshot struct {
	ID                      string    `json:"id"`
	AccountID               string    `json:"account_id"`
	CreatorUsername         string    `json:"creator_username"`
	CreatorNickname         string    `json:"creator_nickname"`
	CreatorAvatarURL        string    `json:"creator_avatar_url"`
	PrivacyOptions          []string  `json:"privacy_level_options"`
	CommentDisabled         bool      `json:"comment_disabled"`
	DuetDisabled            bool      `json:"duet_disabled"`
	StitchDisabled          bool      `json:"stitch_disabled"`
	MaxVideoPostDurationSec int       `json:"max_video_post_duration_sec"`
	FetchedAt               time.Time `json:"fetched_at"`
}
