// Package auth_test pins the session and CSRF middleware.
//
// @file      internal/auth/middleware_test.go
// @for       Gin middleware tests for cookie auth and CSRF checks.
// @uses      testing, net/http/httptest, gin, miniredis/v2, redis/go-redis/v9
// @reason    Proves missing, stale, and mismatched credentials are refused.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
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

	if !c.IsAborted() {
		t.Fatal("expected the request to abort")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
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

	if !c.IsAborted() {
		t.Fatal("expected the stale session to abort")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
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

	if !c.IsAborted() {
		t.Fatal("expected a missing token to abort")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRequireCSRFRejectsWrongToken(t *testing.T) {
	c, rec, req := newGinContext(t, http.MethodPost, "/session")
	req.Header.Set("X-CSRF-Token", "csrf-wrong")
	c.Set(sessionContextKey, &Session{CSRFToken: "csrf-1"})

	RequireCSRF()(c)

	if !c.IsAborted() {
		t.Fatal("expected a mismatched token to abort")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRequireCSRFRejectsWithoutSession(t *testing.T) {
	c, rec, req := newGinContext(t, http.MethodPost, "/session")
	req.Header.Set("X-CSRF-Token", "csrf-1")

	RequireCSRF()(c)

	if !c.IsAborted() {
		t.Fatal("expected a missing session to abort")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}
