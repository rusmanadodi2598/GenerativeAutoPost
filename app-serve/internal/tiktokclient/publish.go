// Package tiktokclient provides the HTTP transport for the TikTok open API.
//
// @file      internal/tiktokclient/publish.go
// @for       Content Posting calls: creator info, video, photo, draft, chunk, status.
// @uses      net/http, encoding/json, internal/apierror, tiktokclient transport
// @reason    Publishing endpoints stay out of client.go so each file keeps one concern.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package tiktokclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// Chunk is one byte range of a resumable upload.
type Chunk struct {
	Index       int
	First       int64
	Last        int64
	Total       int64
	ContentType string
	Data        []byte
}

// ContentRange renders the HTTP Content-Range header for this chunk.
func (c Chunk) ContentRange() string {
	return "bytes " + strconv.FormatInt(c.First, 10) + "-" + strconv.FormatInt(c.Last, 10) + "/" + strconv.FormatInt(c.Total, 10)
}

// valid reports whether the chunk range and payload length agree.
func (c Chunk) valid() bool {
	if c.Index < 0 || c.First < 0 || c.Total <= 0 || c.Last < c.First || c.Last >= c.Total {
		return false
	}
	return int64(len(c.Data)) == c.Last-c.First+1
}

// QueryCreatorInfo fetches the creator profile and allowed privacy levels.
func (c *Client) QueryCreatorInfo(ctx context.Context, access string) (CreatorInfo, error) {
	headers := map[string]string{"Authorization": "Bearer " + access}
	raw, err := c.do(ctx, http.MethodPost, "/v2/post/publish/creator_info/query/", headers, strings.NewReader("{}"), "application/json; charset=UTF-8")
	if err != nil {
		return CreatorInfo{}, err
	}
	var out CreatorInfo
	if err := decodeData(raw, &out); err != nil {
		return CreatorInfo{}, err
	}
	return out, nil
}

// InitVideoPost starts a direct video post and returns its publish id.
func (c *Client) InitVideoPost(ctx context.Context, access string, req VideoInitRequest) (VideoInitResponse, error) {
	raw, err := c.postJSON(ctx, access, "/v2/post/publish/video/init/", req)
	if err != nil {
		return VideoInitResponse{}, err
	}
	var out VideoInitResponse
	if err := decodeData(raw, &out); err != nil {
		return VideoInitResponse{}, err
	}
	return out, nil
}

// InitPhotoPost starts a direct photo post and returns its publish id.
func (c *Client) InitPhotoPost(ctx context.Context, access string, req PhotoInitRequest) (PhotoInitResponse, error) {
	raw, err := c.postJSON(ctx, access, "/v2/post/publish/content/init/", req)
	if err != nil {
		return PhotoInitResponse{}, err
	}
	var out PhotoInitResponse
	if err := decodeData(raw, &out); err != nil {
		return PhotoInitResponse{}, err
	}
	return out, nil
}

// InitDraftUpload starts an inbox draft upload and returns its publish id.
func (c *Client) InitDraftUpload(ctx context.Context, access string, req DraftInitRequest) (DraftInitResponse, error) {
	raw, err := c.postJSON(ctx, access, "/v2/post/publish/inbox/video/init/", req)
	if err != nil {
		return DraftInitResponse{}, err
	}
	var out DraftInitResponse
	if err := decodeData(raw, &out); err != nil {
		return DraftInitResponse{}, err
	}
	return out, nil
}

// UploadChunk PUTs one byte range to the absolute upload URL TikTok returned.
func (c *Client) UploadChunk(ctx context.Context, uploadURL string, chunk Chunk) error {
	if !chunk.valid() {
		return apierror.New(apierror.CodeTikTokInvalidParam, 0, "The upload chunk range is invalid.")
	}
	endpoint := uploadURL
	if !strings.HasPrefix(uploadURL, "http") {
		endpoint = c.baseURL() + uploadURL
	}
	headers := map[string]string{
		"Content-Range":  chunk.ContentRange(),
		"Content-Length": strconv.Itoa(len(chunk.Data)),
	}
	_, err := c.doEndpoint(ctx, http.MethodPut, endpoint, headers, bytes.NewReader(chunk.Data), chunk.ContentType)
	return err
}

// FetchStatus polls the state of a publish job.
func (c *Client) FetchStatus(ctx context.Context, access string, publishID string) (PublishStatus, error) {
	raw, err := c.postJSON(ctx, access, "/v2/post/publish/status/fetch/", publishIDRequest{PublishID: publishID})
	if err != nil {
		return PublishStatus{}, err
	}
	var out PublishStatus
	if err := decodeData(raw, &out); err != nil {
		return PublishStatus{}, err
	}
	return out, nil
}

// postJSON sends a JSON body with the bearer token and returns the raw response.
// payload is typed by the caller and only crosses the JSON encode boundary here.
func (c *Client) postJSON(ctx context.Context, access, path string, payload any) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, badUpstream(fmt.Errorf("encoding request: %w", err))
	}
	headers := map[string]string{"Authorization": "Bearer " + access}
	return c.do(ctx, http.MethodPost, path, headers, bytes.NewReader(encoded), "application/json; charset=UTF-8")
}
