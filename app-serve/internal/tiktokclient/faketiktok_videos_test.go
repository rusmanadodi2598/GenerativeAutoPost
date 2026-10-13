// Package tiktokclient_test extends the scripted TikTok server for video reads.
//
// @file      internal/tiktokclient/faketiktok_videos_test.go
// @for       Video read endpoint path constants the fake server routes.
// @uses      none (path constants only)
// @reason    Keeps video fixtures out of the already long faketiktok_test.go.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package tiktokclient

// Video read endpoint paths the fake routes.
const (
	videoListPath  = "/v2/video/list/"
	videoQueryPath = "/v2/video/query/"
)
