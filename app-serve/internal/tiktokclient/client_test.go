// Package tiktokclient_test pins the TikTok client wire behavior.
//
// @file      internal/tiktokclient/client_test.go
// @for       Table tests for OAuth token, revoke, and user info calls.
// @uses      testing, reflect, internal/apierror, tiktokclient
// @reason    Proves token round trips, retry counts, and wire error mapping.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package tiktokclient

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"runtime"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// tokenBody is a full token endpoint success payload.
const tokenBody = `{"access_token":"act-1","expires_in":3600,"refresh_token":"ref-1",` +
	`"refresh_expires_in":7200,"open_id":"oid-1","scope":"user.info.basic","token_type":"Bearer"}`

// wantTokens is the expected decode of tokenBody.
func wantTokens() TokenSet {
	return TokenSet{"act-1", 3600, "ref-1", 7200, "oid-1", "user.info.basic", "Bearer"}
}

// exchangeCase is one scripted token call scenario.
type exchangeCase struct {
	name      string
	responses []fakeResponse
	wantErr   apierror.Code
	wantLogID string
	wantHits  int64
	form      map[string]string
	absent    []string
}

// runExchange scripts the token endpoint and asserts request and result.
func (tc exchangeCase) runExchange(t *testing.T, refresh bool) {
	t.Helper()
	f := newFakeTikTok(t)
	f.queue(tokenPath, tc.responses...)
	c := newTestClient(t, f)
	var got TokenSet
	var err error
	if refresh {
		got, err = c.RefreshToken(context.Background(), "old-ref")
	} else {
		got, err = c.ExchangeCode(context.Background(), "the-code")
	}
	if tc.wantErr != "" {
		wantError(t, err, tc.wantErr, tc.wantLogID)
	} else {
		if err != nil {
			t.Fatalf("token call: %v", err)
		}
		if !reflect.DeepEqual(got, wantTokens()) {
			t.Fatalf("token set = %+v, want %+v", got, wantTokens())
		}
	}
	form := parseForm(t, f)
	assertForm(t, form, tc.form)
	for _, key := range tc.absent {
		if form.Has(key) {
			t.Fatalf("refresh must not send %s", key)
		}
	}
	wantHits(t, f, tc.wantHits)
}

func TestExchangeCode(t *testing.T) {
	tests := []exchangeCase{
		{
			name: "happy path round trips and posts the auth form", responses: []fakeResponse{successEnvelope(tokenBody)}, wantHits: 1,
			form: map[string]string{"grant_type": "authorization_code", "client_key": "test-key", "client_secret": "test-secret", "code": "the-code", "redirect_uri": "https://example.test/callback"},
		},
		{name: "invalid_param maps to invalid param", responses: []fakeResponse{wireErrorResponse(400, "invalid_param", "bad code", "")}, wantErr: apierror.CodeTikTokInvalidParam, wantHits: 1},
		{name: "access_token_invalid maps to reauth and carries log id", responses: []fakeResponse{wireErrorResponse(401, "access_token_invalid", "expired", "log-77")}, wantErr: apierror.CodeTikTokReauthRequired, wantLogID: "log-77", wantHits: 1},
		{name: "malformed json maps to bad upstream", responses: []fakeResponse{malformedResponse(200)}, wantErr: apierror.CodeTikTokBadUpstream, wantHits: 1},
		{name: "429 then success retries once", responses: []fakeResponse{wireErrorResponse(429, "rate_limit_exceeded", "slow down", ""), successEnvelope(tokenBody)}, wantHits: 2},
		{name: "503 then 503 then success retries twice", responses: []fakeResponse{{status: 503}, {status: 503}, successEnvelope(tokenBody)}, wantHits: 3},
		{name: "429 on every attempt maps to rate limited", responses: []fakeResponse{wireErrorResponse(429, "rate_limit_exceeded", "slow down", "")}, wantErr: apierror.CodeTikTokRateLimited, wantHits: 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { tc.runExchange(t, false) })
	}
}

// TestExchangeCodeRateLimitedOnExhaustionWithoutBody proves a body-less 429 maps
// to the typed rate limited code once retries are exhausted.
func TestExchangeCodeRateLimitedOnExhaustionWithoutBody(t *testing.T) {
	f := newFakeTikTok(t)
	f.queue(tokenPath, fakeResponse{status: http.StatusTooManyRequests})
	c := newTestClient(t, f)
	_, err := c.ExchangeCode(context.Background(), "the-code")
	wantError(t, err, apierror.CodeTikTokRateLimited, "")
	wantHits(t, f, 3)
}

// TestExchangeCodeUnavailableOnExhaustionWithUnknownCode proves a 503 carrying
// an unclassifiable code maps to unavailable, not unknown.
func TestExchangeCodeUnavailableOnExhaustionWithUnknownCode(t *testing.T) {
	f := newFakeTikTok(t)
	f.queue(tokenPath, wireErrorResponse(http.StatusServiceUnavailable, "brand_new_code", "?", "log-3"))
	c := newTestClient(t, f)
	_, err := c.ExchangeCode(context.Background(), "the-code")
	wantError(t, err, apierror.CodeTikTokUnavailable, "")
	wantHits(t, f, 3)
}

// TestRetryBackoffHonorsContextCancellation proves the retry sleep returns the
// raw context error instead of a TikTok unavailable error.
func TestRetryBackoffHonorsContextCancellation(t *testing.T) {
	f := newFakeTikTok(t)
	f.handle(tokenPath, wireErrorResponse(http.StatusTooManyRequests, "rate_limit_exceeded", "slow down", ""))
	c := newTestClient(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for f.hits() == 0 {
			runtime.Gosched()
		}
		cancel()
	}()
	_, err := c.ExchangeCode(ctx, "the-code")
	if err == nil {
		t.Fatal("expected a cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	var apiErr *apierror.Error
	if errors.As(err, &apiErr) {
		t.Fatalf("cancellation must not be *apierror.Error, got %v", apiErr)
	}
}

func TestExchangeCodeNoRetryOnSuccess(t *testing.T) {
	f := newFakeTikTok(t)
	f.handle(tokenPath, successEnvelope(tokenBody))
	c := newTestClient(t, f)
	if _, err := c.ExchangeCode(context.Background(), "the-code"); err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	wantHits(t, f, 1)
}

func TestRefreshToken(t *testing.T) {
	tests := []exchangeCase{
		{
			name: "happy path sends refresh grant and no auth code", responses: []fakeResponse{successEnvelope(tokenBody)}, wantHits: 1,
			form:   map[string]string{"grant_type": "refresh_token", "refresh_token": "old-ref", "client_key": "test-key", "client_secret": "test-secret"},
			absent: []string{"code", "redirect_uri"},
		},
		{name: "access_token_invalid maps to reauth", responses: []fakeResponse{wireErrorResponse(401, "access_token_invalid", "expired", "log-9")}, wantErr: apierror.CodeTikTokReauthRequired, wantHits: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { tc.runExchange(t, true) })
	}
}

func TestRevoke(t *testing.T) {
	tests := []struct {
		name      string
		responses []fakeResponse
		wantErr   apierror.Code
		wantHits  int64
	}{
		{name: "happy path carries the access token", responses: []fakeResponse{successEnvelope(`{}`)}, wantHits: 1},
		{name: "invalid_param maps to invalid param", responses: []fakeResponse{wireErrorResponse(400, "invalid_param", "bad token", "")}, wantErr: apierror.CodeTikTokInvalidParam, wantHits: 1},
		{name: "5xx on all attempts maps to unavailable", responses: []fakeResponse{{status: 503}}, wantErr: apierror.CodeTikTokUnavailable, wantHits: 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeTikTok(t)
			f.queue(revokePath, tc.responses...)
			c := newTestClient(t, f)
			err := c.Revoke(context.Background(), "act-1")
			if tc.wantErr != "" {
				wantError(t, err, tc.wantErr, "")
			} else if err != nil {
				t.Fatalf("Revoke: %v", err)
			}
			assertForm(t, parseForm(t, f), map[string]string{"access_token": "act-1"})
			wantHits(t, f, tc.wantHits)
		})
	}
}
