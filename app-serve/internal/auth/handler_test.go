// Package auth_test drives the auth routes over a real Gin router.
//
// @file      internal/auth/handler_test.go
// @for       The router harness plus the protected route and cookie tests.
// @uses      testing, net/http/httptest, context, io, log/slog, strings, time, gin, internal/tiktokclient, internal/apierror
// @reason    The guard layout, cookie behavior, and reply shapes must hold end to end.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package auth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// testSessionID and testCSRF name the cookie session the protected route
// tests authenticate with.
const (
	testSessionID = "sess-http-1"
	testCSRF      = "csrf-http-1"
)

// httpHarness bundles the router under test with the stores its endpoints
// mutate, plus the account fake the refresh routes read.
type httpHarness struct {
	router    *gin.Engine
	svc       *Service
	sessions  *SessionStore
	accounts  *fakeUpserter
	refresher *fakeRefresher
	sess      Session
}

// newHTTPHarness wires one service behind a real Gin router.
func newHTTPHarness(t *testing.T) *httpHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	states, _ := newStateStore(t)
	sessions, _ := newSessionStore(t, time.Hour)
	exchanger := &fakeExchanger{tokens: tiktokclient.TokenSet{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		Scope:        testScopes,
	}}
	// The refresh route needs material to succeed; the fake returns it by
	// default so the happy path reaches the token store instead of denying.
	upserter := &fakeUpserter{id: "acct-1", refreshToken: "refresh-1", accessToken: "access-1"}
	refresher := &fakeRefresher{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := ServiceConfig{
		ClientKey:   testClientKey,
		RedirectURI: testRedirectURI,
		Scopes:      testScopes,
	}
	svc := NewService(cfg, exchanger, refresher, upserter, states, sessions, newFakeLocker(), logger)
	router := gin.New()
	// The request id middleware stands in for the platform one, so the success
	// envelope carries a populated request_id in the route tests.
	router.Use(requestIDMiddleware())
	RegisterRoutes(router, svc, sessions, RouterConfig{CookieName: constCookie, CookieTTL: time.Hour})
	return &httpHarness{router: router, svc: svc, sessions: sessions, accounts: upserter, refresher: refresher}
}

// requestIDMiddleware sets the request_id context key the envelope reads.
func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("request_id", "test-request-id")
		c.Next()
	}
}

// serve runs one request through the harness router.
func (h *httpHarness) serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

// authed builds a request carrying the cookie session and its CSRF header.
func (h *httpHarness) authed(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	sess := h.createSession(t)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", sess.CSRFToken)
	req.AddCookie(&http.Cookie{Name: constCookie, Value: sess.ID})
	return req
}

// createSession stores the fixed test session.
func (h *httpHarness) createSession(t *testing.T) Session {
	t.Helper()
	sess := Session{
		ID:         testSessionID,
		OperatorID: testOperatorID,
		CSRFToken:  testCSRF,
		Scopes:     []string{"user.info.basic"},
		CreatedAt:  time.Now().UTC(),
	}
	if err := h.sessions.Create(context.Background(), sess); err != nil {
		t.Fatalf("creating session: %v", err)
	}
	return sess
}

// findCookie returns the named response cookie or fails the test.
func findCookie(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("cookie %q was not set", name)
	return nil
}

func TestRouterRefreshMissingAccountIDIsValidationFailed(t *testing.T) {
	h := newHTTPHarness(t)
	rec := h.serve(h.authed(t, http.MethodPost, "/auth/tiktok/refresh", "{}"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if got := problemCode(t, rec); got != string(apierror.CodeValidationFailed) {
		t.Fatalf("code = %q, want %q", got, apierror.CodeValidationFailed)
	}
}

func TestRouterProtectedRoutesWithoutCookieAreUnauthorized(t *testing.T) {
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/auth/tiktok/refresh"},
		{http.MethodPost, "/auth/tiktok/revoke"},
		{http.MethodPost, "/auth/logout"},
		{http.MethodGet, "/auth/session"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			h := newHTTPHarness(t)
			rec := h.serve(httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if got := problemCode(t, rec); got != string(apierror.CodeUnauthorized) {
				t.Fatalf("code = %q, want %q", got, apierror.CodeUnauthorized)
			}
		})
	}
}

func TestRouterSessionReturnsOperatorWithoutTokens(t *testing.T) {
	h := newHTTPHarness(t)
	sess := h.createSession(t)
	req := httptest.NewRequest(http.MethodGet, "/auth/session", nil)
	req.AddCookie(&http.Cookie{Name: constCookie, Value: sess.ID})
	rec := h.serve(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, testOperatorID) {
		t.Fatalf("body %q does not carry the operator id", body)
	}
	if strings.Contains(body, "access_token") || strings.Contains(body, "refresh_token") {
		t.Fatalf("body %q leaks token material", body)
	}
}

func TestRouterLogoutExpiresSessionCookie(t *testing.T) {
	h := newHTTPHarness(t)
	rec := h.serve(h.authed(t, http.MethodPost, "/auth/logout", ""))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if cookie := findCookie(t, rec, constCookie); cookie.MaxAge >= 0 {
		t.Fatalf("logout cookie MaxAge = %d, want an expired cookie", cookie.MaxAge)
	}
}
