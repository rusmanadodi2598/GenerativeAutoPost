// Package tiktokclient_test extends the scripted TikTok server for publish calls.
//
// @file      internal/tiktokclient/faketiktok_publish_test.go
// @for       Publish endpoint paths and chunk capture helpers on the fake server.
// @uses      net/http, testing
// @reason    Keeps publish fixtures out of the already long faketiktok_test.go.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package tiktokclient

// Publish endpoint paths the fake routes.
const (
	creatorInfoPath = "/v2/post/publish/creator_info/query/"
	videoInitPath   = "/v2/post/publish/video/init/"
	photoInitPath   = "/v2/post/publish/content/init/"
	draftInitPath   = "/v2/post/publish/inbox/video/init/"
	statusPath      = "/v2/post/publish/status/fetch/"
	uploadPath      = "/upload"
)
