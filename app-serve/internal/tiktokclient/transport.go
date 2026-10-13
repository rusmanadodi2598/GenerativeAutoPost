// Package tiktokclient provides the HTTP transport for the TikTok open API.
//
// @file      internal/tiktokclient/transport.go
// @for       Retrying HTTP transport and envelope decoding for TikTok calls.
// @uses      net/http, encoding/json, math/rand, internal/apierror
// @reason    Keeps timeout, retry, and decoding plumbing out of endpoint methods.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package tiktokclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// requestTimeout bounds one outbound call, derived from the caller context.
const requestTimeout = 15 * time.Second

// maxAttempts caps retries for network errors, 429, and 5xx responses.
const maxAttempts = 3

// baseBackoff is the first retry delay before exponential growth.
const baseBackoff = 300 * time.Millisecond

// maxBackoff caps the computed retry delay.
const maxBackoff = 5 * time.Second

// maxErrorBody caps how much of an upstream error body we read.
const maxErrorBody = 64 << 10

// baseURL returns the configured host without a trailing slash.
func (c *Client) baseURL() string {
	return strings.TrimRight(c.cfg.BaseURL, "/")
}

// do runs a request with bounded retries and maps failures to typed errors.
func (c *Client) do(ctx context.Context, method, path string, headers map[string]string, body io.Reader, contentType string) ([]byte, error) {
	var payload []byte
	if body != nil {
		var err error
		payload, err = io.ReadAll(body)
		if err != nil {
			return nil, badUpstream(err)
		}
	}
	endpoint := c.baseURL() + path
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		res, err := c.send(reqCtx, method, endpoint, bodyReader(payload), headers, contentType)
		cancel()
		if err != nil {
			if attempt == maxAttempts {
				return nil, apierror.New(apierror.CodeTikTokUnavailable, 0, "TikTok is temporarily unavailable. Try again shortly.").Wrap(err)
			}
			if sleepErr := sleepBackoff(ctx, attempt, ""); sleepErr != nil {
				return nil, sleepErr
			}
			continue
		}
		switch {
		case res.status >= 200 && res.status < 300:
			return res.body, nil
		case res.status == http.StatusTooManyRequests:
			bodyErr := parseErrorBody(res.body)
			if attempt == maxAttempts {
				apiErr := apierror.New(apierror.CodeTikTokRateLimited, 0, "Too many requests. Try again shortly.")
				if bodyErr != nil {
					apiErr = apiErr.WithLogID(bodyErr.LogID)
				}
				return nil, apiErr
			}
			if bodyErr != nil {
				if _, known := wireErrors[bodyErr.Code]; known && !retryableWireCode(bodyErr.Code) {
					return nil, mapWireError(*bodyErr)
				}
			}
			if err := sleepBackoff(ctx, attempt, res.retryAfter); err != nil {
				return nil, err
			}
			continue
		case res.status >= 500:
			bodyErr := parseErrorBody(res.body)
			if attempt == maxAttempts {
				if bodyErr != nil {
					if _, known := wireErrors[bodyErr.Code]; known {
						return nil, mapWireError(*bodyErr)
					}
				}
				return nil, apierror.New(apierror.CodeTikTokUnavailable, 0, "TikTok is temporarily unavailable. Try again shortly.")
			}
			if bodyErr != nil {
				if _, known := wireErrors[bodyErr.Code]; known && !retryableWireCode(bodyErr.Code) {
					return nil, mapWireError(*bodyErr)
				}
			}
			if err := sleepBackoff(ctx, attempt, res.retryAfter); err != nil {
				return nil, err
			}
			continue
		case res.status >= 400:
			bodyErr := parseErrorBody(res.body)
			if bodyErr == nil {
				return nil, badUpstream(errors.New("missing error body"))
			}
			return nil, mapWireError(*bodyErr)
		default:
			return nil, badUpstream(fmt.Errorf("unexpected status %d", res.status))
		}
	}
	return nil, apierror.New(apierror.CodeTikTokUnavailable, 0, "TikTok is temporarily unavailable. Try again shortly.")
}

// backoffFor returns the exponential delay with full jitter for one attempt.
func backoffFor(attempt int, retryAfter string) time.Duration {
	delay := baseBackoff << (attempt - 1)
	if delay > maxBackoff || delay <= 0 {
		delay = maxBackoff
	}
	if retryAfter != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs > 0 {
			if wait := time.Duration(secs) * time.Second; wait < maxBackoff {
				delay = wait
			}
		}
	}
	return time.Duration(rand.Int63n(int64(delay) + 1))
}

// sleepBackoff waits out the retry delay, returning early on context cancellation.
func sleepBackoff(ctx context.Context, attempt int, retryAfter string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(backoffFor(attempt, retryAfter)):
	}
	return nil
}

// httpResult carries what the retry loop needs from one attempt.
type httpResult struct {
	body       []byte
	status     int
	retryAfter string
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

// decodeData unwraps the data member of an API envelope.
func decodeData(raw []byte, out any) error {
	env, wireErr := parseEnvelope(raw)
	if wireErr != nil {
		return wireErr
	}
	if len(env.Data) == 0 {
		return badUpstream(errors.New("empty data"))
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return badUpstream(err)
	}
	return nil
}
