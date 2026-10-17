// Package auth_test covers the browser facing Google sign in routes.
//
// @file      internal/auth/google_handler_test.go
// @for       Authorize and callback httptest cases over a mounted router.
// @uses      testing, context, net/http, net/http/httptest, net/url, time, gin
// @reason    The Google routes redirect on every branch and mint the session.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-17
package auth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// refusingLimiter denies every hit and records the key it was asked about.
type refusingLimiter struct {
	key   string
	calls int
}

func (l *refusingLimiter) Allow(_ context.Context, key string) (bool, error) {
	l.calls++
	l.key = key
	return false, nil
}

// googleHTTPHarness mounts the Google routes over the shared Google harness.
type googleHTTPHarness struct {
	*googleHarness
	router *gin.Engine
}

func newGoogleHTTPHarness(t *testing.T, throttle gin.HandlerFunc) *googleHTTPHarness {
	t.Helper()
	h := newGoogleHarness(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterGoogleRoutes(router, h.svc, RouterConfig{
		CookieName:   constCookie,
		CookieTTL:    time.Hour,
		AppUIBaseURL: "https://console.example.com",
		Throttle:     throttle,
	})
	return &googleHTTPHarness{googleHarness: h, router: router}
}

func (h *googleHTTPHarness) serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

func TestGoogleAuthorize_RedirectsToGoogle(t *testing.T) {
	h := newGoogleHTTPHarness(t, nil)
	rec := h.serve(httptest.NewRequest(http.MethodGet, "/auth/google/authorize", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parsing Location %q: %v", rec.Header().Get("Location"), err)
	}
	if loc.Host != "accounts.google.com" {
		t.Fatalf("Location host = %q, want accounts.google.com", loc.Host)
	}
}

func TestGoogleAuthorize_Throttled(t *testing.T) {
	limiter := &refusingLimiter{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := newGoogleHTTPHarness(t, Throttle(limiter, logger))
	rec := h.serve(httptest.NewRequest(http.MethodGet, "/auth/google/authorize", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if limiter.calls != 1 {
		t.Fatalf("limiter calls = %d, want once", limiter.calls)
	}
	if want := authThrottlePrefix + "192.0.2.1"; limiter.key != want {
		t.Fatalf("limiter key = %q, want %q", limiter.key, want)
	}
}

func TestGoogleCallback_SetsTheCookieAndRedirects(t *testing.T) {
	h := newGoogleHTTPHarness(t, nil)
	state := h.verifiedFor(t, "operator@example.com")
	rec := h.serve(httptest.NewRequest(http.MethodGet, "/auth/google/callback?code=auth-code-1&state="+state, nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "https://console.example.com/" {
		t.Fatalf("Location = %q, want the console root", got)
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
}

func TestGoogleCallback_RefusalRedirectsWithoutACookie(t *testing.T) {
	h := newGoogleHTTPHarness(t, nil)
	state := h.verifiedFor(t, "intruder@example.com")
	rec := h.serve(httptest.NewRequest(http.MethodGet, "/auth/google/callback?code=auth-code-1&state="+state, nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "https://console.example.com/auth/result?status=error" {
		t.Fatalf("Location = %q, want the error page", got)
	}
	if cookies := rec.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("cookies %v set on a refused sign in", cookies)
	}
}
