// Package tiktokclient reads the account's videos from the TikTok open API.
//
// @file      internal/tiktokclient/videos.go
// @for       Video list and video query calls.
// @uses      net/http, net/url, internal/apierror, tiktokclient transport
// @reason    Video reads are separate from publishing so each file keeps one concern.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package tiktokclient

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// Video list page size bounds from 002 section 7.
const (
	defaultMaxCount = 10
	maxMaxCount     = 20
)

// maxQueryIDs caps a single video query, per 002 section 7.
const maxQueryIDs = 20

// ListVideos reads one page of the account's videos.
func (c *Client) ListVideos(ctx context.Context, access string, cursor string, maxCount int) (VideoPage, error) {
	switch {
	case maxCount <= 0:
		maxCount = defaultMaxCount
	case maxCount > maxMaxCount:
		maxCount = maxMaxCount
	}
	raw, err := c.postJSON(ctx, access, "/v2/video/list/", videoListRequest{Cursor: cursor, MaxCount: maxCount})
	if err != nil {
		return VideoPage{}, err
	}
	var out VideoPage
	if err := decodeData(raw, &out); err != nil {
		return VideoPage{}, err
	}
	return out, nil
}

// QueryVideos fetches the videos matching ids with the requested fields.
func (c *Client) QueryVideos(ctx context.Context, access string, ids []string, fields []string) ([]Video, error) {
	if len(ids) > maxQueryIDs {
		return nil, apierror.New(apierror.CodeTikTokInvalidParam, 0, "At most 20 video ids may be queried at once.")
	}
	encoded, err := json.Marshal(videoQueryRequest{VideoIDs: ids})
	if err != nil {
		return nil, badUpstream(err)
	}
	path := "/v2/video/query/?fields=" + url.QueryEscape(strings.Join(fields, ","))
	headers := map[string]string{"Authorization": "Bearer " + access}
	raw, err := c.do(ctx, http.MethodPost, path, headers, bytes.NewReader(encoded), "application/json; charset=UTF-8")
	if err != nil {
		return nil, err
	}
	var out videoQueryData
	if err := decodeData(raw, &out); err != nil {
		return nil, err
	}
	return out.Videos, nil
}
