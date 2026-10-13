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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
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
			time.Sleep(backoffFor(attempt, ""))
			continue
		}
		switch {
		case res.status >= 200 && res.status < 300:
			return res.body, nil
		case res.status == http.StatusTooManyRequests || res.status >= 500:
			bodyErr := parseErrorBody(res.body)
			if attempt == maxAttempts {
				if bodyErr != nil {
					return nil, mapWireError(*bodyErr)
				}
				return nil, apierror.New(apierror.CodeTikTokUnavailable, 0, "TikTok is temporarily unavailable. Try again shortly.")
			}
			if bodyErr != nil {
				if _, known := wireErrors[bodyErr.Code]; known && !retryableWireCode(bodyErr.Code) {
					return nil, mapWireError(*bodyErr)
				}
			}
			time.Sleep(backoffFor(attempt, res.retryAfter))
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
