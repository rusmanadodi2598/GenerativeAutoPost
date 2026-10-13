// Package tiktokclient provides the HTTP transport for the TikTok open API.
//
// @file      internal/tiktokclient/client.go
// @for       OAuth and user info calls against the TikTok open API.
// @uses      net/http, encoding/json, internal/apierror, internal/tiktokclient schema
// @reason    One retrying transport keeps TikTok transport concerns out of services.
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
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// defaultBaseURL is the production TikTok open API host.
const defaultBaseURL = "https://open.tiktokapis.com"

// successBodyCap caps a successful response body read.
const successBodyCap = 4 << 20

// Config carries app credentials and the injectable API host.
type Config struct {
	ClientKey    string
	ClientSecret string
	RedirectURI  string
	BaseURL      string       // empty means defaultBaseURL; tests point this at httptest
	HTTPClient   *http.Client // optional; nil gets a client with no global timeout
}

// Client calls the TikTok Content Posting API.
type Client struct {
	cfg Config
}

// New builds a client, filling in the default host and HTTP client.
func New(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	return &Client{cfg: cfg}
}

// httpResult carries what the retry loop needs from one attempt.
type httpResult struct {
	body       []byte
	status     int
	retryAfter string
}

// send performs one HTTP attempt and reads the whole response body.
func (c *Client) send(ctx context.Context, method, endpoint string, body io.Reader, headers map[string]string, contentType string) (httpResult, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return httpResult{}, fmt.Errorf("building request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return httpResult{}, fmt.Errorf("calling tiktok: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	limit := int64(successBodyCap)
	if resp.StatusCode >= 400 {
		limit = maxErrorBody
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return httpResult{}, fmt.Errorf("reading response: %w", err)
	}
	return httpResult{body: raw, status: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After")}, nil
}

// bodyReader rebuilds a reader per attempt so retries resend the same bytes.
func bodyReader(payload []byte) io.Reader {
	if payload == nil {
		return nil
	}
	return bytes.NewReader(payload)
}

// parseErrorBody extracts the wire error from a failure body, or nil.
func parseErrorBody(raw []byte) *apiErrorBody {
	if len(raw) == 0 {
		return nil
	}
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil
	}
	if env.Error == nil || env.Error.Code == "" || env.Error.Code == "ok" {
		return nil
	}
	return env.Error
}

// parseEnvelope decodes a response envelope and maps any wire error.
func parseEnvelope(raw []byte) (apiEnvelope, *apierror.Error) {
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return env, badUpstream(err)
	}
	if env.Error != nil && env.Error.Code != "" && env.Error.Code != "ok" {
		return env, mapWireError(*env.Error)
	}
	return env, nil
}

// tokenForm renders a token request as form fields.
func tokenForm(req tokenRequest) url.Values {
	form := url.Values{}
	form.Set("client_key", req.ClientKey)
	form.Set("client_secret", req.ClientSecret)
	form.Set("grant_type", req.GrantType)
	if req.Code != "" {
		form.Set("code", req.Code)
	}
	if req.RedirectURI != "" {
		form.Set("redirect_uri", req.RedirectURI)
	}
	if req.RefreshToken != "" {
		form.Set("refresh_token", req.RefreshToken)
	}
	return form
}

// ExchangeCode trades an authorization code for a token set.
func (c *Client) ExchangeCode(ctx context.Context, code string) (TokenSet, error) {
	form := tokenForm(tokenRequest{
		ClientKey:    c.cfg.ClientKey,
		ClientSecret: c.cfg.ClientSecret,
		Code:         code,
		GrantType:    "authorization_code",
		RedirectURI:  c.cfg.RedirectURI,
	})
	raw, err := c.do(ctx, http.MethodPost, "/v2/oauth/token/", nil, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return TokenSet{}, err
	}
	var out TokenSet
	if err := decodeData(raw, &out); err != nil {
		return TokenSet{}, err
	}
	return out, nil
}

// RefreshToken renews an access token from a refresh token.
func (c *Client) RefreshToken(ctx context.Context, refresh string) (TokenSet, error) {
	form := tokenForm(tokenRequest{
		ClientKey:    c.cfg.ClientKey,
		ClientSecret: c.cfg.ClientSecret,
		GrantType:    "refresh_token",
		RefreshToken: refresh,
	})
	raw, err := c.do(ctx, http.MethodPost, "/v2/oauth/token/", nil, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return TokenSet{}, err
	}
	var out TokenSet
	if err := decodeData(raw, &out); err != nil {
		return TokenSet{}, err
	}
	return out, nil
}

// Revoke invalidates an access token.
func (c *Client) Revoke(ctx context.Context, access string) error {
	form := url.Values{}
	form.Set("client_key", c.cfg.ClientKey)
	form.Set("client_secret", c.cfg.ClientSecret)
	form.Set("access_token", access)
	raw, err := c.do(ctx, http.MethodPost, "/v2/oauth/revoke/", nil, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	if _, wireErr := parseEnvelope(raw); wireErr != nil {
		return wireErr
	}
	return nil
}

// GetUserInfo fetches the authenticated user with the requested fields.
func (c *Client) GetUserInfo(ctx context.Context, access string, fields []string) (UserInfo, error) {
	path := "/v2/user/info/?fields=" + url.QueryEscape(strings.Join(fields, ","))
	headers := map[string]string{"Authorization": "Bearer " + access}
	raw, err := c.do(ctx, http.MethodGet, path, headers, nil, "application/json; charset=UTF-8")
	if err != nil {
		return UserInfo{}, err
	}
	var out userInfoData
	if err := decodeData(raw, &out); err != nil {
		return UserInfo{}, err
	}
	return out.User, nil
}
