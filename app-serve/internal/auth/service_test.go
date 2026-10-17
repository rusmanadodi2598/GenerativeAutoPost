// Package auth_test pins TikTok OAuth authorize URL construction.
//
// @file      internal/auth/service_test.go
// @for       Authorize URL tests and the shared OAuth test harness.
// @uses      testing, context, net/url, encoding/base64, strings, miniredis/v2
// @reason    The redirect must carry exact scopes, client key, and redirect URI.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package auth

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

const (
	testClientKey   = "test-client-key"
	testRedirectURI = "https://app.example.com/api/v1/auth/tiktok/callback"
	testScopes      = "user.info.basic,video.publish"
	testOperatorID  = "op-1"
)

// harness bundles a service with the stores its tests inspect.
type harness struct {
	svc      *Service
	states   *StateStore
	sessions *SessionStore
	mr       *miniredis.Miniredis
}

// newHarness wires a service over miniredis backed stores. The lifecycle
// fakes are defaults: callback tests never reach the refresher or the lock.
func newHarness(t *testing.T, tokens TokenExchanger, accounts AccountUpserter) *harness {
	t.Helper()
	states, _ := newStateStore(t)
	sessions, mr := newSessionStore(t, time.Hour)
	cfg := ServiceConfig{
		ClientKey:   testClientKey,
		RedirectURI: testRedirectURI,
		Scopes:      testScopes,
		OperatorID:  testOperatorID,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &harness{
		svc:      NewService(cfg, tokens, &fakeRefresher{}, accounts, states, sessions, newFakeLocker(), logger),
		states:   states,
		sessions: sessions,
		mr:       mr,
	}
}

// stateFromURL reads the state query parameter out of an authorize URL.
func stateFromURL(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parsing authorize url: %v", err)
	}
	return parsed.Query().Get("state")
}

// issueState runs one authorize call for operatorID and returns the state it stored.
func issueState(t *testing.T, svc *Service, operatorID string) string {
	t.Helper()
	raw, err := svc.AuthorizeURL(context.Background(), operatorID, "user.info.basic")
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	return stateFromURL(t, raw)
}

// assertRandomToken proves a token is 32 random bytes in base64url form.
func assertRandomToken(t *testing.T, token string) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("token %q is not base64url: %v", token, err)
	}
	if len(raw) != 32 {
		t.Fatalf("token decodes to %d bytes, want 32", len(raw))
	}
}

func TestServiceAuthorizeURL(t *testing.T) {
	cases := []struct {
		name      string
		requested string
		wantScope string
	}{
		{"requested subset", "user.info.basic", "user.info.basic"},
		{"empty subset uses config scopes", "", testScopes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, &fakeExchanger{}, &fakeUpserter{})
			raw, err := h.svc.AuthorizeURL(context.Background(), testOperatorID, tc.requested)
			if err != nil {
				t.Fatalf("AuthorizeURL: %v", err)
			}
			parsed, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("parsing url: %v", err)
			}
			if parsed.Scheme != "https" || parsed.Host != "www.tiktok.com" || parsed.Path != "/v2/auth/authorize/" {
				t.Fatalf("endpoint = %s, want the TikTok authorize endpoint", parsed)
			}
			q := parsed.Query()
			if got := q.Get("client_key"); got != testClientKey {
				t.Fatalf("client_key = %q, want %q", got, testClientKey)
			}
			if got := q.Get("response_type"); got != "code" {
				t.Fatalf("response_type = %q, want code", got)
			}
			if got := q.Get("scope"); got != tc.wantScope {
				t.Fatalf("scope = %q, want %q", got, tc.wantScope)
			}
			if got := q.Get("redirect_uri"); got != testRedirectURI {
				t.Fatalf("redirect_uri = %q, want %q", got, testRedirectURI)
			}
			if !strings.Contains(raw, url.QueryEscape(testRedirectURI)) {
				t.Fatalf("url %s does not carry the encoded redirect_uri", raw)
			}
			if q.Get("state") == "" {
				t.Fatal("state is missing from the authorize url")
			}
		})
	}
}

func TestServiceAuthorizeURLIssuesDistinctStates(t *testing.T) {
	h := newHarness(t, &fakeExchanger{}, &fakeUpserter{})
	ctx := context.Background()
	first, err := h.svc.AuthorizeURL(ctx, testOperatorID, "user.info.basic")
	if err != nil {
		t.Fatalf("first AuthorizeURL: %v", err)
	}
	second, err := h.svc.AuthorizeURL(ctx, testOperatorID, "user.info.basic")
	if err != nil {
		t.Fatalf("second AuthorizeURL: %v", err)
	}
	stateOne := stateFromURL(t, first)
	stateTwo := stateFromURL(t, second)
	if stateOne == stateTwo {
		t.Fatal("two authorize calls issued the same state")
	}
	for _, state := range []string{stateOne, stateTwo} {
		issuer, err := h.states.Consume(ctx, state)
		if err != nil {
			t.Fatalf("consuming issued state: %v", err)
		}
		if issuer != testOperatorID {
			t.Fatalf("stored issuer = %q, want %q", issuer, testOperatorID)
		}
	}
}
