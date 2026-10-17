// Package auth_test pins the session and CSRF middleware.
//
// @file      internal/auth/middleware_test.go
// @for       Gin middleware tests for cookie auth and CSRF checks.
// @uses      testing, net/http/httptest, encoding/json, gin, miniredis/v2, redis/go-redis/v9, internal/apierror
// @reason    Proves missing, stale, and mismatched credentials are refused.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// constCookie is the session cookie name used across the middleware tests.
const constCookie = "genauto_session"

// newGinContext builds a test context with a real request.
func newGinContext(t *testing.T, method, path string) (*gin.Context, *httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(method, path, nil)
	c.Request = req
	return c, rec, req
}

// problemCode decodes the RFC 9457 body and returns its code member.
func problemCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding problem body %q: %v", rec.Body.String(), err)
	}
	return body.Code
}

// assertRejected checks the HTTP status and the problem body code together.
func assertRejected(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder, status int, code apierror.Code) {
	t.Helper()
	if !c.IsAborted() {
		t.Fatal("expected the request to abort")
	}
	if rec.Code != status {
		t.Fatalf("status = %d, want %d", rec.Code, status)
	}
	if got := problemCode(t, rec); got != string(code) {
		t.Fatalf("body code = %q, want %q", got, code)
	}
}

func TestRequireSessionAttachesSession(t *testing.T) {
	store, _ := newSessionStore(t, time.Hour)
	sess := Session{
		ID:         "sess-1",
		OperatorID: "op-1",
		CSRFToken:  "csrf-1",
		Scopes:     []string{"user.info.basic"},
		CreatedAt:  time.Now().UTC(),
	}
	if err := store.Create(context.Background(), sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	c, rec, req := newGinContext(t, http.MethodGet, "/session")
	req.AddCookie(&http.Cookie{Name: constCookie, Value: sess.ID})

	RequireSession(store, constCookie)(c)

	if c.IsAborted() {
		t.Fatalf("request aborted: %s", rec.Body.String())
	}
	got, ok := SessionFrom(c)
	if !ok {
		t.Fatal("session was not attached to the context")
	}
	if got.OperatorID != "op-1" {
		t.Fatalf("operator = %q, want op-1", got.OperatorID)
	}
}

func TestRequireSessionMissingCookieIsUnauthorized(t *testing.T) {
	store, _ := newSessionStore(t, time.Hour)
	c, rec, _ := newGinContext(t, http.MethodGet, "/session")

	RequireSession(store, constCookie)(c)

	assertRejected(t, c, rec, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

func TestRequireSessionStaleCookieIsUnauthorized(t *testing.T) {
	store, mr := newSessionStore(t, time.Hour)
	sess := Session{ID: "sess-stale", OperatorID: "op-1", CSRFToken: "csrf-1"}
	if err := store.Create(context.Background(), sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	mr.FastForward(2 * time.Hour)

	c, rec, req := newGinContext(t, http.MethodGet, "/session")
	req.AddCookie(&http.Cookie{Name: constCookie, Value: sess.ID})

	RequireSession(store, constCookie)(c)

	assertRejected(t, c, rec, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

// TestRequireSessionRedisDownIsServiceUnavailable closes Redis, so a cookie
// that looks valid must still map to a 503 outage, not a 401 sign out.
func TestRequireSessionRedisDownIsServiceUnavailable(t *testing.T) {
	store, mr := newSessionStore(t, time.Hour)
	mr.Close()
	c, rec, req := newGinContext(t, http.MethodGet, "/session")
	req.AddCookie(&http.Cookie{Name: constCookie, Value: "sess-1"})

	RequireSession(store, constCookie)(c)

	assertRejected(t, c, rec, http.StatusServiceUnavailable, apierror.CodeServiceUnavailable)
}

func TestRequireCSRFAcceptsMatchingToken(t *testing.T) {
	c, rec, req := newGinContext(t, http.MethodPost, "/session")
	req.Header.Set("X-CSRF-Token", "csrf-1")
	c.Set(sessionContextKey, &Session{CSRFToken: "csrf-1"})

	RequireCSRF()(c)

	if c.IsAborted() {
		t.Fatalf("request aborted: %s", rec.Body.String())
	}
}

func TestRequireCSRFRejectsMissingToken(t *testing.T) {
	c, rec, _ := newGinContext(t, http.MethodPost, "/session")
	c.Set(sessionContextKey, &Session{CSRFToken: "csrf-1"})

	RequireCSRF()(c)

	assertRejected(t, c, rec, http.StatusForbidden, apierror.CodeForbidden)
}

func TestRequireCSRFRejectsWrongToken(t *testing.T) {
	c, rec, req := newGinContext(t, http.MethodPost, "/session")
	req.Header.Set("X-CSRF-Token", "csrf-wrong")
	c.Set(sessionContextKey, &Session{CSRFToken: "csrf-1"})

	RequireCSRF()(c)

	assertRejected(t, c, rec, http.StatusForbidden, apierror.CodeForbidden)
}

func TestRequireCSRFRejectsWithoutSession(t *testing.T) {
	c, rec, req := newGinContext(t, http.MethodPost, "/session")
	req.Header.Set("X-CSRF-Token", "csrf-1")

	RequireCSRF()(c)

	assertRejected(t, c, rec, http.StatusForbidden, apierror.CodeForbidden)
}

// allowingLimiter records the key of every hit and allows the request.
type allowingLimiter struct {
	keys []string
}

func (l *allowingLimiter) Allow(_ context.Context, key string) (bool, error) {
	l.keys = append(l.keys, key)
	return true, nil
}

// TestThrottleIgnoresForwardedForWithoutTrustedProxies proves a client cannot
// rotate X-Forwarded-For to escape the budget when no proxy is trusted.
func TestThrottleIgnoresForwardedForWithoutTrustedProxies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := &allowingLimiter{}
	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		t.Fatalf("SetTrustedProxies: %v", err)
	}
	router.GET("/probe", Throttle(limiter, nil))

	for _, forwarded := range []string{"198.51.100.1", "198.51.100.2"} {
		req := httptest.NewRequest(http.MethodGet, "/probe", nil)
		req.RemoteAddr = "203.0.113.7:5000"
		req.Header.Set("X-Forwarded-For", forwarded)
		router.ServeHTTP(httptest.NewRecorder(), req)
	}

	want := authThrottlePrefix + "203.0.113.7"
	if len(limiter.keys) != 2 || limiter.keys[0] != want || limiter.keys[1] != want {
		t.Fatalf("limiter keys = %v, want two hits on %q", limiter.keys, want)
	}
}
