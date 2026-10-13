// Package auth_test covers the browser facing TikTok OAuth routes.
//
// @file      internal/auth/handler_oauth_test.go
// @for       Authorize and callback httptest cases, including error redaction.
// @uses      testing, net/http/httptest, encoding/json, strings, time
// @reason    The callback must always redirect and never echo upstream text.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// callbackState issues one authorize URL and returns its OAuth state.
func (h *httpHarness) callbackState(t *testing.T) string {
	t.Helper()
	raw, err := h.svc.AuthorizeURL(t.Context(), "user.info.basic")
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	return stateFromURL(t, raw)
}

func TestRouterAuthorizeRedirectsWithOAuthParams(t *testing.T) {
	h := newHTTPHarness(t)
	rec := h.serve(httptest.NewRequest(http.MethodGet, "/auth/tiktok/authorize?scope=user.info.basic", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	for _, want := range []string{"client_key=", "state=", "response_type=code"} {
		if !strings.Contains(loc, want) {
			t.Fatalf("Location %q does not contain %q", loc, want)
		}
	}
}

func TestRouterCallbackHappyPathSetsSessionCookie(t *testing.T) {
	h := newHTTPHarness(t)
	state := h.callbackState(t)
	rec := h.serve(httptest.NewRequest(http.MethodGet, "/auth/tiktok/callback?code=auth-code-1&state="+state, nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/auth/result?status=ok" {
		t.Fatalf("Location = %q, want the ok page", got)
	}
	cookie := findCookie(t, rec, constCookie)
	if cookie.Value == "" {
		t.Fatal("session cookie value is empty")
	}
	if !cookie.HttpOnly || !cookie.Secure {
		t.Fatalf("cookie HttpOnly=%v Secure=%v, want both true", cookie.HttpOnly, cookie.Secure)
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.MaxAge != int(time.Hour/time.Second) {
		t.Fatalf("MaxAge = %d, want %d", cookie.MaxAge, int(time.Hour/time.Second))
	}
}

func TestRouterCallbackReusedStateRedirectsToError(t *testing.T) {
	h := newHTTPHarness(t)
	state := h.callbackState(t)
	path := "/auth/tiktok/callback?code=auth-code-1&state=" + state
	h.serve(httptest.NewRequest(http.MethodGet, path, nil))
	rec := h.serve(httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/auth/result?status=error" {
		t.Fatalf("Location = %q, want the error page", got)
	}
}

func TestRouterCallbackHidesUpstreamErrorText(t *testing.T) {
	h := newHTTPHarness(t)
	rec := h.serve(httptest.NewRequest(http.MethodGet,
		"/auth/tiktok/callback?error=access_denied&error_description=secret-detail&code=x&state=y", nil))
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "status=error") {
		t.Fatalf("Location = %q, want the error page", loc)
	}
	if strings.Contains(loc, "secret-detail") || strings.Contains(loc, "access_denied") {
		t.Fatalf("Location %q leaks upstream error text", loc)
	}
}

func TestRouterCallbackMissingCodeIsNotJSON(t *testing.T) {
	h := newHTTPHarness(t)
	rec := h.serve(httptest.NewRequest(http.MethodGet, "/auth/tiktok/callback?state=some-state", nil))
	if got := rec.Header().Get("Location"); got != "/auth/result?status=error" {
		t.Fatalf("Location = %q, want the error page", got)
	}
	if json.Valid(rec.Body.Bytes()) {
		t.Fatalf("body %q is JSON; the callback must not write JSON", rec.Body.String())
	}
}
