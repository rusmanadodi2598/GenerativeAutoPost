// Package insight shapes the TikTok reads into the console wire views.
//
// @file      internal/insight/views.go
// @for       Wire views and the field lists each upstream read requests.
// @uses      time, github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient
// @reason    Keeps the mapping rules in one file so a count can never be zeroed on the way out.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     schema
// @stability stable
// @since     2026-10-12
package insight

import (
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// userInfoFields are the open API fields the console profile shows.
var userInfoFields = []string{
	"open_id", "avatar_url", "display_name", "bio_description",
	"profile_deep_link", "follower_count", "following_count", "likes_count", "video_count",
}

// videoQueryFields are the fields a video query and every stats read ask for.
var videoQueryFields = []string{"id", "like_count", "comment_count", "share_count", "view_count"}

// ptr returns a copy of the value behind a count, or nil when it is absent.
func ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

// VideoView is one video as the console sees it.
type VideoView struct {
	ID            string    `json:"id"`
	CreateTime    int64     `json:"create_time,omitempty"`
	Title         string    `json:"title,omitempty"`
	Description   string    `json:"description,omitempty"`
	CoverImageURL string    `json:"cover_image_url,omitempty"`
	ShareURL      string    `json:"share_url,omitempty"`
	Duration      int64     `json:"duration,omitempty"`
	Height        int64     `json:"height,omitempty"`
	Width         int64     `json:"width,omitempty"`
	LikeCount     *int64    `json:"like_count,omitempty"`
	CommentCount  *int64    `json:"comment_count,omitempty"`
	ShareCount    *int64    `json:"share_count,omitempty"`
	ViewCount     *int64    `json:"view_count,omitempty"`
	IsAIGC        bool      `json:"is_aigc,omitempty"`
	FetchedAt     time.Time `json:"fetched_at"`
}

// viewVideo maps one upstream video to the wire shape. The counts are copied
// as pointers so a field TikTok omitted stays absent instead of becoming zero.
func viewVideo(video tiktokclient.Video, fetchedAt time.Time) VideoView {
	return VideoView{
		ID:            video.ID,
		CreateTime:    video.CreateTime,
		Title:         video.Title,
		Description:   video.Description,
		CoverImageURL: video.CoverImageURL,
		ShareURL:      video.ShareURL,
		Duration:      video.Duration,
		Height:        video.Height,
		Width:         video.Width,
		LikeCount:     ptr(video.LikeCount),
		CommentCount:  ptr(video.CommentCount),
		ShareCount:    ptr(video.ShareCount),
		ViewCount:     ptr(video.ViewCount),
		IsAIGC:        video.IsAIGC,
		FetchedAt:     fetchedAt,
	}
}

// VideoPage is one page of videos in our cursor envelope. TikTok returns a
// numeric cursor, the console passes it back as an opaque string.
type VideoPage struct {
	Videos  []VideoView `json:"videos"`
	Cursor  string      `json:"cursor,omitempty"`
	HasMore bool        `json:"has_more"`
}

// ProfileView is the creator profile: account counts plus the postable record.
type ProfileView struct {
	OpenID                  string    `json:"open_id"`
	DisplayName             string    `json:"display_name"`
	AvatarURL               string    `json:"avatar_url,omitempty"`
	BioDescription          string    `json:"bio_description,omitempty"`
	ProfileDeepLink         string    `json:"profile_deep_link,omitempty"`
	FollowerCount           *int64    `json:"follower_count,omitempty"`
	FollowingCount          *int64    `json:"following_count,omitempty"`
	LikesCount              *int64    `json:"likes_count,omitempty"`
	VideoCount              *int64    `json:"video_count,omitempty"`
	CreatorUsername         string    `json:"creator_username,omitempty"`
	PrivacyLevelOptions     []string  `json:"privacy_level_options"`
	CommentDisabled         bool      `json:"comment_disabled"`
	DuetDisabled            bool      `json:"duet_disabled"`
	StitchDisabled          bool      `json:"stitch_disabled"`
	MaxVideoPostDurationSec int       `json:"max_video_post_duration_sec"`
	FetchedAt               time.Time `json:"fetched_at"`
}

// snapshotFromVideo copies the counts into an unsaved snapshot row.
func snapshotFromVideo(video tiktokclient.Video, fetchedAt time.Time) StatSnapshot {
	return StatSnapshot{
		VideoID:      video.ID,
		ViewCount:    ptr(video.ViewCount),
		LikeCount:    ptr(video.LikeCount),
		CommentCount: ptr(video.CommentCount),
		ShareCount:   ptr(video.ShareCount),
		FetchedAt:    fetchedAt,
	}
}
