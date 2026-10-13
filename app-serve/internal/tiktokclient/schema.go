// Package tiktokclient provides HTTP clients for the TikTok open API.
//
// @file      internal/tiktokclient/schema.go
// @for       TikTok open API wire shapes for tokens, errors, and user info.
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
