// Package publish defines the request and response shapes for publish routes.
//
// @file      internal/publish/schema.go
// @for       Typed DTOs for the init and chunk upload publish routes.
// @uses      nothing beyond the Go standard library
// @reason    Contract first DTOs keep handlers and validation explicit.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     schema
// @stability stable
// @since     2026-10-12
package publish

// InitVideoRequest starts a direct video post or a media upload session.
type InitVideoRequest struct {
	AccountID string          `json:"account_id" validate:"required,uuid"`
	MediaID   string          `json:"media_id" validate:"required,uuid"`
	Source    string          `json:"source" validate:"required,oneof=FILE_UPLOAD PULL_FROM_URL"`
	PostInfo  PostInfoRequest `json:"post_info" validate:"required"`
	MediaURL  string          `json:"media_url,omitempty"`
}

// InitPhotoRequest starts a direct photo post or a photo upload session.
type InitPhotoRequest struct {
	AccountID string           `json:"account_id" validate:"required,uuid"`
	MediaID   string           `json:"media_id" validate:"required,uuid"`
	PostMode  string           `json:"post_mode" validate:"required,oneof=DIRECT_POST MEDIA_UPLOAD"`
	PostInfo  PhotoInfoRequest `json:"post_info" validate:"required"`
	PhotoURLs []string         `json:"photo_urls" validate:"required,min=1"`
}

// InitDraftRequest starts a draft that lands in the TikTok user inbox.
type InitDraftRequest struct {
	AccountID string `json:"account_id" validate:"required,uuid"`
	MediaID   string `json:"media_id" validate:"required,uuid"`
	Source    string `json:"source" validate:"required,oneof=FILE_UPLOAD PULL_FROM_URL"`
	MediaURL  string `json:"media_url,omitempty"`
}

// PostInfoRequest carries the video post settings sent to TikTok.
type PostInfoRequest struct {
	Title                 string `json:"title" validate:"required"`
	PrivacyLevel          string `json:"privacy_level"`
	DisableDuet           bool   `json:"disable_duet"`
	DisableComment        bool   `json:"disable_comment"`
	DisableStitch         bool   `json:"disable_stitch"`
	VideoCoverTimestampMs int64  `json:"video_cover_timestamp_ms"`
	BrandContentToggle    bool   `json:"brand_content_toggle"`
	BrandOrganicToggle    bool   `json:"brand_organic_toggle"`
}

// PhotoInfoRequest extends the video post settings with photo fields.
// Embedding keeps the wire shape flat while sharing the common block.
type PhotoInfoRequest struct {
	PostInfoRequest
	Description  string `json:"description"`
	AutoAddMusic bool   `json:"auto_add_music"`
}

// ChunkPutResponse reports chunk progress back to the uploader.
type ChunkPutResponse struct {
	UploadedBytes  int64 `json:"uploaded_bytes"`
	TotalBytes     int64 `json:"total_bytes"`
	NextChunkIndex int   `json:"next_chunk_index"`
	Complete       bool  `json:"complete"`
}
