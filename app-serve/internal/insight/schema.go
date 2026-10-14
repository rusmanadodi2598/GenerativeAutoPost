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

import "time"

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

// CreatorSnapshot is one cached view of a creator privacy and profile record.
type CreatorSnapshot struct {
	ID                      string    `json:"id"`
	AccountID               string    `json:"account_id"`
	PrivacyOptions          []string  `json:"privacy_level_options"`
	CommentDisabled         bool      `json:"comment_disabled"`
	DuetDisabled            bool      `json:"duet_disabled"`
	StitchDisabled          bool      `json:"stitch_disabled"`
	MaxVideoPostDurationSec int       `json:"max_video_post_duration_sec"`
	FetchedAt               time.Time `json:"fetched_at"`
}
