// Package compose wires every module into one HTTP surface.
//
// @file      internal/compose/routes_test.go
// @for       Route table tests for the mounted console API.
// @uses      net/http, httptest, testing, gin, internal/auth, internal/insight, internal/media, internal/publish
// @reason    The composition root is the only place the module tables meet, so its output is the contract app-ui codes against.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     router
// @stability stable
// @since     2026-10-15
package compose

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/auth"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/insight"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/media"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/publish"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/webhook"
)

// realRouteTables binds each module's own registration function with a
// handler carrying only the session resolver. Mounting answers a status per
// path because the guard runs before any service is touched.
func realRouteTables(session func(*gin.Context) (string, bool)) Routes {
	return Routes{
		Auth: func(r gin.IRouter) {
			auth.RegisterRoutes(r, auth.NewService(auth.ServiceConfig{},
				nil, nil, nil, nil, nil, nil, nil,
			), nil, auth.RouterConfig{CookieName: "genauto_session"})
		},
		Media: func(r gin.IRouter) { media.RegisterRoutes(r, media.NewHandler(nil, nil, session)) },
		Publish: func(r gin.IRouter) {
			publish.RegisterRoutes(r, publish.NewHandler(nil, nil, nil, session, publish.HandlerConfig{}))
		},
		Insight: func(r gin.IRouter) { insight.RegisterRoutes(r, insight.NewHandler(nil, session)) },
		Webhook: func(r gin.IRouter) { webhook.RegisterRoutes(r, webhook.NewHandler(nil, webhook.HandlerConfig{}, nil)) },
	}
}

// testRouter mounts the real tables behind the recovery middleware.
func testRouter(session func(*gin.Context) (string, bool)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())
	Mount(router, realRouteTables(session))
	return router
}

// signedIn resolves an operator so the mount is asserted, not the session rules.
func signedIn(*gin.Context) (string, bool) { return "operator-1", true }

// anonymous resolves no session, so guarded routes answer 401.
func anonymous(*gin.Context) (string, bool) { return "", false }

// TestMountAnswersEveryConsolePath asserts the paths from SPEC 003 resolve
// under /api/v1 rather than falling through to 404.
func TestMountAnswersEveryConsolePath(t *testing.T) {
	router := testRouter(signedIn)
	paths := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/healthz"},
		{http.MethodGet, "/api/v1/auth/tiktok/authorize"},
		{http.MethodGet, "/api/v1/auth/tiktok/callback"},
		{http.MethodPost, "/api/v1/auth/tiktok/refresh"},
		{http.MethodPost, "/api/v1/auth/tiktok/revoke"},
		{http.MethodPost, "/api/v1/auth/logout"},
		{http.MethodGet, "/api/v1/auth/session"},
		{http.MethodPost, "/api/v1/media/uploads"},
		{http.MethodGet, "/api/v1/media/one"},
		{http.MethodDelete, "/api/v1/media/one"},
		{http.MethodPost, "/api/v1/publish/videos"},
		{http.MethodPost, "/api/v1/publish/photos"},
		{http.MethodPost, "/api/v1/publish/drafts"},
		{http.MethodPut, "/api/v1/publish/one/chunks"},
		{http.MethodGet, "/api/v1/publish/one"},
		{http.MethodGet, "/api/v1/publish"},
		{http.MethodPost, "/api/v1/publish/one/reconcile"},
		{http.MethodGet, "/api/v1/videos"},
		{http.MethodPost, "/api/v1/videos/query"},
		{http.MethodGet, "/api/v1/videos/7300000000000000000"},
		{http.MethodGet, "/api/v1/videos/7300000000000000000/stats"},
		{http.MethodPost, "/api/v1/videos/7300000000000000000/stats/refresh"},
		{http.MethodGet, "/api/v1/accounts/22222222-2222-7222-8222-222222222222/profile"},
	}
	for _, tc := range paths {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s %s is not mounted (status %d)", tc.method, tc.path, rec.Code)
		}
	}
}

// TestMountKeepsTheWebhookOutsideVersioning proves the intake stays at its
// root path, so an API version bump never moves TikTok's contract.
func TestMountKeepsTheWebhookOutsideVersioning(t *testing.T) {
	router := testRouter(signedIn)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/webhooks/tiktok", nil))
	if rec.Code == http.StatusNotFound {
		t.Fatal("POST /webhooks/tiktok is not mounted")
	}
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/tiktok", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/v1/webhooks/tiktok status = %d, want 404", rec.Code)
	}
}

// TestMountKeepsGuardedRoutesGuarded proves the module guards run after the
// tables are wired together.
func TestMountKeepsGuardedRoutesGuarded(t *testing.T) {
	router := testRouter(anonymous)
	guarded := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/publish/videos"},
		{http.MethodGet, "/api/v1/publish"},
		{http.MethodGet, "/api/v1/videos"},
		{http.MethodGet, "/api/v1/media/one"},
	}
	for _, tc := range guarded {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

// TestMountKeepsBrowserFacingAuthOpen proves the OAuth redirect pair is not
// behind a session, or the callback can never complete.
func TestMountKeepsBrowserFacingAuthOpen(t *testing.T) {
	router := testRouter(anonymous)
	for _, path := range []string{"/api/v1/auth/tiktok/authorize", "/api/v1/auth/tiktok/callback"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("GET %s status = 401, want the browser route reachable", path)
		}
	}
}
