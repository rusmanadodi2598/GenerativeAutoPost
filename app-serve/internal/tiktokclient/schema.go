// Package tiktokclient provides HTTP clients for the TikTok open API.
//
// @file      internal/tiktokclient/schema.go
// @for       TikTok open API wire shapes for tokens, errors, user info, publishing, and videos.
// @uses      encoding/json
// @reason    Keeps request and response types separate from transport logic.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     schema
// @stability stable
// @since     2026-10-12
package tiktokclient

import "encoding/json"

type apiEnvelope struct {
	Data  json.RawMessage `json:"data"`
	Error *apiErrorBody   `json:"error,omitempty"`
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	LogID   string `json:"log_id"`
}

// TokenSet holds OAuth credentials from the token endpoint, which is form-encoded, not JSON (002 section 2.3).
type TokenSet struct {
	AccessToken      string `json:"access_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
	OpenID           string `json:"open_id"`
	Scope            string `json:"scope"`
	TokenType        string `json:"token_type"`
}

type tokenRequest struct {
	ClientKey    string `json:"client_key"`
	ClientSecret string `json:"client_secret"`
	Code         string `json:"code,omitempty"`
	GrantType    string `json:"grant_type"`
	RedirectURI  string `json:"redirect_uri,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

type UserInfo struct {
	OpenID          string `json:"open_id"`
	UnionID         string `json:"union_id"`
	AvatarURL       string `json:"avatar_url"`
	DisplayName     string `json:"display_name"`
	BioDescription  string `json:"bio_description,omitempty"`
	ProfileDeepLink string `json:"profile_deep_link,omitempty"`
	FollowerCount   int64  `json:"follower_count,omitempty"`
	FollowingCount  int64  `json:"following_count,omitempty"`
	LikesCount      int64  `json:"likes_count,omitempty"`
	VideoCount      int64  `json:"video_count,omitempty"`
}

type userInfoData struct {
	User UserInfo `json:"user"`
}

// CreatorInfo is the postable creator profile and its allowed privacy levels.
type CreatorInfo struct {
	CreatorUsername         string   `json:"creator_username"`
	CreatorNickname         string   `json:"creator_nickname"`
	CreatorAvatarURL        string   `json:"creator_avatar_url"`
	PrivacyLevelOptions     []string `json:"privacy_level_options"`
	CommentDisabled         bool     `json:"comment_disabled"`
	DuetDisabled            bool     `json:"duet_disabled"`
	StitchDisabled          bool     `json:"stitch_disabled"`
	MaxVideoPostDurationSec int      `json:"max_video_post_duration_sec"`
}

// PostInfo carries the shared video post settings.
type PostInfo struct {
	Title                 string `json:"title"`
	PrivacyLevel          string `json:"privacy_level"`
	DisableDuet           bool   `json:"disable_duet"`
	DisableComment        bool   `json:"disable_comment"`
	DisableStitch         bool   `json:"disable_stitch"`
	VideoCoverTimestampMS int64  `json:"video_cover_timestamp_ms,omitempty"`
	BrandContentToggle    bool   `json:"brand_content_toggle,omitempty"`
	BrandOrganicToggle    bool   `json:"brand_organic_toggle,omitempty"`
	IsAIGC                bool   `json:"is_aigc"`
}

// PhotoPostInfo is PostInfo plus the photo only description and music switch.
type PhotoPostInfo struct {
	PostInfo
	Description  string `json:"description,omitempty"`
	AutoAddMusic bool   `json:"auto_add_music,omitempty"`
}

// SourceInfo tells TikTok where the media comes from and how it is chunked.
type SourceInfo struct {
	Source          string `json:"source"`
	VideoSize       int64  `json:"video_size,omitempty"`
	ChunkSize       int64  `json:"chunk_size,omitempty"`
	TotalChunkCount int    `json:"total_chunk_count,omitempty"`
	VideoURL        string `json:"video_url,omitempty"`
}

// VideoInitRequest is the direct video post init payload.
type VideoInitRequest struct {
	PostInfo   PostInfo   `json:"post_info"`
	SourceInfo SourceInfo `json:"source_info"`
}

// VideoInitResponse carries the publish id and, for file uploads, the upload URL.
type VideoInitResponse struct {
	PublishID string `json:"publish_id"`
	UploadURL string `json:"upload_url,omitempty"`
}

// PhotoInitRequest is the direct photo post init payload.
type PhotoInitRequest struct {
	PostInfo    PhotoPostInfo `json:"post_info"`
	SourceInfo  SourceInfo    `json:"source_info"`
	PostMode    string        `json:"post_mode"`
	MediaType   string        `json:"media_type"`
	PhotoImages []string      `json:"photo_images"`
}

// PhotoInitResponse carries the publish id for a photo post.
type PhotoInitResponse struct {
	PublishID string `json:"publish_id"`
}

// DraftInitRequest is the inbox draft init payload; it has no post_info.
type DraftInitRequest struct {
	SourceInfo SourceInfo `json:"source_info"`
}

// DraftInitResponse carries the publish id and upload URL for a draft.
type DraftInitResponse struct {
	PublishID string `json:"publish_id"`
	UploadURL string `json:"upload_url,omitempty"`
}

// PublishStatus is the polled state of one publish job.
type PublishStatus struct {
	Status     string `json:"status"`
	FailReason string `json:"fail_reason,omitempty"`
	// PublicalyAvailablePostID keeps TikTok's misspelling on the wire while the Go name is correct (002 section 5.3).
	PublicalyAvailablePostID []string `json:"publicaly_available_post_id,omitempty"`
	UploadedBytes            int64    `json:"uploaded_bytes,omitempty"`
	DownloadedBytes          int64    `json:"downloaded_bytes,omitempty"`
}

// publishIDRequest is the status fetch body.
type publishIDRequest struct {
	PublishID string `json:"publish_id"`
}

// Video is one TikTok video.
type Video struct {
	ID            string `json:"id"`
	CreateTime    int64  `json:"create_time,omitempty"`
	CoverImageURL string `json:"cover_image_url,omitempty"`
	ShareURL      string `json:"share_url,omitempty"`
	Description   string `json:"video_description,omitempty"`
	Duration      int64  `json:"duration,omitempty"`
	Height        int64  `json:"height,omitempty"`
	Width         int64  `json:"width,omitempty"`
	Title         string `json:"title,omitempty"`
	EmbedHTML     string `json:"embed_html,omitempty"`
	EmbedLink     string `json:"embed_link,omitempty"`
	// Counts are pointers so a field TikTok omits stays nil downstream, never a
	// zero the console would read as a real measurement (002 section 7).
	LikeCount    *int64 `json:"like_count,omitempty"`
	CommentCount *int64 `json:"comment_count,omitempty"`
	ShareCount   *int64 `json:"share_count,omitempty"`
	ViewCount    *int64 `json:"view_count,omitempty"`
	IsAIGC       bool   `json:"is_aigc,omitempty"`
}

// VideoPage is one page of the account's video list.
type VideoPage struct {
	Videos  []Video `json:"videos"`
	Cursor  int64   `json:"cursor,omitempty"`
	HasMore bool    `json:"has_more,omitempty"`
}

// videoListRequest is the video list body.
type videoListRequest struct {
	Cursor   string `json:"cursor,omitempty"`
	MaxCount int    `json:"max_count,omitempty"`
}

// videoQueryRequest is the video query body.
type videoQueryRequest struct {
	VideoIDs []string `json:"video_ids"`
}

// videoQueryData unwraps the videos member of a query response.
type videoQueryData struct {
	Videos []Video `json:"videos"`
}
