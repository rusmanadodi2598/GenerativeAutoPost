// Package webhook decodes TikTok webhook envelopes and event payloads.
//
// @file      internal/webhook/schema.go
// @for       Typed webhook envelope and per-event content structs.
// @uses      encoding/json, app-serve/internal/apierror
// @reason    Gives later webhook tasks typed payloads without free maps.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     schema
// @stability stable
// @since     2026-10-12
package webhook

import (
	"encoding/json"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// Event names follow 002 section 5.3 exactly. Video Kit events
// (video.upload.failed, video.publish.completed) are unused by the
// Content Posting flow and are ignored with a logged acknowledgement.
const (
	EventInboxDelivered    = "post.publish.inbox_delivered"
	EventComplete          = "post.publish.complete"
	EventPubliclyAvailable = "post.publish.publicly_available"
	EventNoLongerPublicly  = "post.publish.no_longer_publicaly_available"
	EventFailed            = "post.publish.failed"
	EventAuthRemoved       = "authorization.removed"
	EventVideoUploadFailed = "video.upload.failed"
	EventVideoPublishDone  = "video.publish.completed"
)

// Envelope is the outer TikTok webhook object. Content arrives as a
// JSON object serialized as a string, so it is decoded in a second step.
type Envelope struct {
	ClientKey  string          `json:"client_key"`
	Event      string          `json:"event"`
	CreateTime int64           `json:"create_time"`
	UserOpenID string          `json:"user_openid"`
	Content    json.RawMessage `json:"content"`
}

// PostPublishContent carries the inner payload for post.publish events.
type PostPublishContent struct {
	PublishID   string `json:"publish_id"`
	PostID      string `json:"post_id,omitempty"`
	PublishType string `json:"publish_type,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// AuthorizationRemovedContent carries the inner payload for
// authorization.removed. Reason codes: 0 unknown, 1 disconnected,
// 2 deleted, 3 age changed, 4 banned, 5 developer revoked.
type AuthorizationRemovedContent struct {
	Reason int `json:"reason"`
}

// decodedContent decodes the envelope content string into out. The any
// parameter is the json.Unmarshal boundary; the caller always passes a
// typed struct from this package.
func decodedContent(env Envelope, out any) error {
	var inner string
	if err := json.Unmarshal(env.Content, &inner); err == nil {
		if err := json.Unmarshal([]byte(inner), out); err != nil {
			return apierror.New(apierror.CodeBadGateway, 0, "The webhook payload is invalid.")
		}
		return nil
	}
	if err := json.Unmarshal(env.Content, out); err != nil {
		return apierror.New(apierror.CodeBadGateway, 0, "The webhook payload is invalid.")
	}
	return nil
}
