// Package media defines upload object shapes and the resumable chunk planner.
//
// @file      internal/media/schema.go
// @for       Media upload DTOs for planning and recording object state.
// @uses      time
// @reason    Provides shared object types used by chunk planning and stores.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     schema
// @stability stable
// @since     2026-10-12
package media

import "time"

// MediaKind identifies the media payload type.
type MediaKind string

// Supported media kinds.
const (
	MediaKindVideo MediaKind = "video"
	MediaKindPhoto MediaKind = "photo"
)

// MediaStorage identifies where object bytes live.
type MediaStorage string

// Supported media storage backends.
const (
	MediaStorageDisk MediaStorage = "disk"
	MediaStorageS3   MediaStorage = "s3"
)

// MediaObject describes one uploaded media payload.
type MediaObject struct {
	ID             string       `json:"id"`
	OwnerID        string       `json:"owner_id"`
	Kind           MediaKind    `json:"kind"`
	ContentType    string       `json:"content_type"`
	ByteSize       int64        `json:"byte_size"`
	ChecksumSHA256 string       `json:"checksum_sha256"`
	DurationMs     int64        `json:"duration_ms"`
	Width          int          `json:"width"`
	Height         int          `json:"height"`
	Storage        MediaStorage `json:"storage"`
	PublicURL      string       `json:"public_url,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
}

// ChunkRecord describes one persisted chunk of a media object.
type ChunkRecord struct {
	ObjectID string `json:"object_id"`
	Index    int    `json:"index"`
	First    int64  `json:"first"`
	Last     int64  `json:"last"`
	Total    int64  `json:"total"`
}
