// Package googleoidc verifies Google id tokens from their signed claims.
//
// @file      internal/platform/googleoidc/oidc.go
// @for       Verify a Google id token and expose the claims auth needs.
// @uses      net/http, encoding/json, encoding/base64, crypto/rsa, crypto/sha256
// @reason    Login must prove the operator identity before any session is minted.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-17
package googleoidc

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Google's fixed endpoints. Tests point Config at httptest servers.
const (
	defaultAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultTokenURL = "https://oauth2.googleapis.com/token"
	defaultJWKSURL  = "https://www.googleapis.com/oauth2/v3/certs"
)

// Config carries the OAuth client credentials and the injectable endpoints.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	AuthURL      string       // empty means the Google default
	TokenURL     string       // empty means the Google default
	JWKSURL      string       // empty means the Google default
	HTTPClient   *http.Client // nil means a client with no global timeout
}

// Claims is the subset of the verified id token that auth consumes.
type Claims struct {
	Subject       string
	Email         string
	EmailVerified bool
}

// Client verifies Google id tokens against the published JWKS.
type Client struct {
	cfg        Config
	httpClient *http.Client
	cache      keyCache
}

// New builds a client and fills in the Google defaults for empty endpoints.
func New(cfg Config) *Client {
	if cfg.AuthURL == "" {
		cfg.AuthURL = defaultAuthURL
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = defaultTokenURL
	}
	if cfg.JWKSURL == "" {
		cfg.JWKSURL = defaultJWKSURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{
		cfg:        cfg,
		httpClient: httpClient,
		cache:      keyCache{keys: map[string]*rsa.PublicKey{}},
	}
}

type joseHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type idTokenClaims struct {
	Issuer        string `json:"iss"`
	Audience      string `json:"aud"`
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Nonce         string `json:"nonce"`
	ExpiresAt     int64  `json:"exp"`
	IssuedAt      int64  `json:"iat"`
}

// VerifyIDToken checks the signature and every claim Google guarantees, and
// returns the identity only after every check passes.
func (c *Client) VerifyIDToken(ctx context.Context, raw string, nonce string) (Claims, error) {
	segments := strings.Split(raw, ".")
	if len(segments) != 3 {
		return Claims{}, fmt.Errorf("googleoidc: id token is not a jws")
	}
	header, err := decodeHeader(segments[0])
	if err != nil {
		return Claims{}, err
	}
	if header.Alg != "RS256" {
		return Claims{}, fmt.Errorf("googleoidc: unsupported token algorithm %q", header.Alg)
	}
	if header.Kid == "" {
		return Claims{}, fmt.Errorf("googleoidc: token header carries no key id")
	}

	key, err := c.publicKey(ctx, header.Kid)
	if err != nil {
		return Claims{}, err
	}
	if err := verifySignature(key, raw, segments); err != nil {
		return Claims{}, err
	}

	claims, err := decodeClaims(segments[1])
	if err != nil {
		return Claims{}, err
	}
	if err := c.checkClaims(claims, nonce); err != nil {
		return Claims{}, err
	}
	return Claims{
		Subject:       claims.Subject,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
	}, nil
}

func decodeHeader(segment string) (joseHeader, error) {
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return joseHeader{}, fmt.Errorf("googleoidc: decoding token header: %w", err)
	}
	var header joseHeader
	if err := json.Unmarshal(raw, &header); err != nil {
		return joseHeader{}, fmt.Errorf("googleoidc: decoding token header: %w", err)
	}
	return header, nil
}

func decodeClaims(segment string) (idTokenClaims, error) {
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return idTokenClaims{}, fmt.Errorf("googleoidc: decoding token claims: %w", err)
	}
	var claims idTokenClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return idTokenClaims{}, fmt.Errorf("googleoidc: decoding token claims: %w", err)
	}
	return claims, nil
}

// verifySignature checks the RS256 signature over the header and payload.
func verifySignature(key *rsa.PublicKey, raw string, segments []string) error {
	signature, err := base64.RawURLEncoding.DecodeString(segments[2])
	if err != nil {
		return fmt.Errorf("googleoidc: decoding token signature: %w", err)
	}
	signed := raw[:len(raw)-len(segments[2])-1]
	digest := sha256.Sum256([]byte(signed))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return fmt.Errorf("googleoidc: verifying token signature: %w", err)
	}
	return nil
}

// checkClaims enforces issuer, audience, freshness, nonce, and verified email.
func (c *Client) checkClaims(claims idTokenClaims, nonce string) error {
	if claims.Issuer != "https://accounts.google.com" && claims.Issuer != "accounts.google.com" {
		return fmt.Errorf("googleoidc: unexpected token issuer %q", claims.Issuer)
	}
	if claims.Audience != c.cfg.ClientID {
		return fmt.Errorf("googleoidc: token audience does not match this client")
	}
	now := time.Now()
	if claims.ExpiresAt <= now.Unix() {
		return fmt.Errorf("googleoidc: token has expired")
	}
	if claims.IssuedAt > now.Unix() {
		return fmt.Errorf("googleoidc: token issued in the future")
	}
	if claims.Nonce != nonce {
		return fmt.Errorf("googleoidc: token nonce does not match")
	}
	if !claims.EmailVerified {
		return fmt.Errorf("googleoidc: email is not verified")
	}
	if claims.Email == "" {
		return fmt.Errorf("googleoidc: token carries no email")
	}
	return nil
}
