// Package googleoidc builds the Google consent URL and exchanges the code.
//
// @file      internal/platform/googleoidc/exchange.go
// @for       Build the Google authorize URL and trade a code for the id token.
// @uses      net/http, net/url, encoding/json, context
// @reason    Login needs the consent redirect and the code-to-token round trip.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-17
package googleoidc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// exchangeTimeout bounds one token exchange, derived from the caller context.
// It mirrors RequestTimeout so the client and the exchange share one budget.
const exchangeTimeout = RequestTimeout

// tokenBodyCap caps the token response read; a token response is a few KB.
const tokenBodyCap = 1 << 20

type tokenResponse struct {
	IDToken string `json:"id_token"`
}

// AuthorizeURL builds the Google consent URL for a state and nonce pair. An
// unparsable configured AuthURL is returned unchanged rather than half-built.
func (c *Client) AuthorizeURL(state, nonce string) string {
	target, err := url.Parse(c.cfg.AuthURL)
	if err != nil {
		return c.cfg.AuthURL
	}
	query := target.Query()
	query.Set("client_id", c.cfg.ClientID)
	query.Set("redirect_uri", c.cfg.RedirectURI)
	query.Set("response_type", "code")
	query.Set("scope", "openid email")
	query.Set("state", state)
	query.Set("nonce", nonce)
	target.RawQuery = query.Encode()
	return target.String()
}

// ExchangeCode trades an authorization code for the raw id token. Access and
// refresh tokens are discarded; only the id token reaches the verifier.
func (c *Client) ExchangeCode(ctx context.Context, code string) (string, error) {
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)
	form.Set("redirect_uri", c.cfg.RedirectURI)
	form.Set("grant_type", "authorization_code")

	// The caller context may have no deadline, so the exchange always runs
	// under the package bound.
	reqCtx, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("googleoidc: building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("googleoidc: exchanging authorization code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, tokenBodyCap))
	if err != nil {
		return "", fmt.Errorf("googleoidc: reading token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("googleoidc: token endpoint returned status %d", resp.StatusCode)
	}
	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return "", fmt.Errorf("googleoidc: decoding token response: %w", err)
	}
	if token.IDToken == "" {
		return "", fmt.Errorf("googleoidc: token response carried no id token")
	}
	return token.IDToken, nil
}
