// Package compose builds the runnable HTTP engine over one config.
//
// @file      internal/compose/build_test.go
// @for       Build tests for the production wiring surface.
// @uses      testing, gin, internal/config
// @reason    Every constructor the wiring touches is exercised here, so a signature drift fails this package first.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     router
// @stability stable
// @since     2026-10-15
package compose

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/auth"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/config"
)

// testPool returns a lazy pool: Build never issues a query, so no server is
// needed to prove the wiring constructs.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), "postgres://localhost:5432/genauto?sslmode=disable")
	if err != nil {
		t.Fatalf("lazy pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// testRedis returns a client over an in-memory server.
func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// testConfig returns a config that passes validation for wiring tests.
func testConfig(t *testing.T) config.Config {
	t.Helper()
	for k, v := range map[string]string{
		"APP_ENV": "development", "APP_PORT": "8080",
		"DATABASE_URL":      "postgres://localhost:5432/genauto?sslmode=disable",
		"REDIS_URL":         "redis://localhost:6379/0",
		"TIKTOK_CLIENT_KEY": "key123", "TIKTOK_REDIRECT_URI": "https://app.example.com/cb",
		"TIKTOK_SCOPES":        "user.info.basic",
		"GOOGLE_CLIENT_ID":     "client-id.apps.googleusercontent.com",
		"GOOGLE_CLIENT_SECRET": "client-secret",
		"GOOGLE_REDIRECT_URI":  "https://app.example.com/auth/google/callback",
		"OPERATOR_EMAIL":       "operator@example.com",
		"TOKEN_ENC_KEY":        strings.Repeat("k", 32), "TOKEN_ENC_KEY_ID": "k1",
		"OPERATOR_ID":  "0191e2c4-0000-7000-8000-000000000001",
		"MEDIA_DRIVER": "disk", "MEDIA_ROOT": t.TempDir(),
		"APP_UI_BASE_URL":       "https://console.example.com",
		"MEDIA_PUBLIC_BASE_URL": "https://media.example.com",
		"SESSION_TTL":           "24h", "LOG_LEVEL": "info",
	} {
		t.Setenv(k, v)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load returned %v", err)
	}
	return cfg
}

// TestBuildMountsAQueryableHealthProbe proves the wired engine answers its
// only dependency-free probe.
func TestBuildMountsAQueryableHealthProbe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app, err := Build(testConfig(t), testPool(t), testRedis(t), nil)
	if err != nil {
		t.Fatalf("Build returned %v", err)
	}
	rec := httptest.NewRecorder()
	app.Engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", rec.Code)
	}
	if app.StatsWorker == nil {
		t.Fatal("App.StatsWorker is nil, the stats sweep is not wired")
	}
}

// TestBuildFailsFastOnABadKey proves a keyring validation fault surfaces
// instead of building an engine that cannot decrypt anything.
func TestBuildFailsFastOnABadKey(t *testing.T) {
	cfg := testConfig(t)
	cfg.TokenEncKey = []byte("short")
	if _, err := Build(cfg, testPool(t), testRedis(t), nil); err == nil {
		t.Fatal("Build succeeded on a short encryption key")
	}
}

// TestBuildTrustsNoProxyByDefault proves the wired engine ignores a spoofed
// X-Forwarded-For when TRUSTED_PROXIES is unset.
func TestBuildTrustsNoProxyByDefault(t *testing.T) {
	app, err := Build(testConfig(t), testPool(t), testRedis(t), nil)
	if err != nil {
		t.Fatalf("Build returned %v", err)
	}
	if got := clientIPFor(t, app.Engine, "203.0.113.7:5000", "198.51.100.1"); got != "203.0.113.7" {
		t.Fatalf("ClientIP = %q, want the peer address", got)
	}
}

// TestBuildTrustsConfiguredProxy proves TRUSTED_PROXIES reaches the engine.
func TestBuildTrustsConfiguredProxy(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "198.51.100.0/24")
	app, err := Build(testConfig(t), testPool(t), testRedis(t), nil)
	if err != nil {
		t.Fatalf("Build returned %v", err)
	}
	if got := clientIPFor(t, app.Engine, "198.51.100.1:5000", "203.0.113.9"); got != "203.0.113.9" {
		t.Fatalf("ClientIP = %q, want the forwarded address", got)
	}
}

// TestBuild_WiresGoogleLogin proves the Google routes are mounted and the auth
// service redirects to the console base, not the media host.
func TestBuild_WiresGoogleLogin(t *testing.T) {
	cfg := testConfig(t)
	client := testRedis(t)
	app, err := Build(cfg, testPool(t), client, nil)
	if err != nil {
		t.Fatalf("Build returned %v", err)
	}
	rec := httptest.NewRecorder()
	app.Engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/authorize", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("GET /api/v1/auth/google/authorize status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "https://accounts.google.com/") {
		t.Fatalf("google authorize redirect = %q, want the google consent page", loc)
	}

	sessions := auth.NewSessionStore(client, cfg.SessionTTL)
	sess := auth.Session{ID: "wired-session", OperatorID: cfg.OperatorID, CSRFToken: "csrf", CreatedAt: time.Now().UTC()}
	if err := sessions.Create(t.Context(), sess); err != nil {
		t.Fatalf("seeding session: %v", err)
	}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/tiktok/callback?error=denied", nil)
	req.AddCookie(&http.Cookie{Name: cfg.SessionCookieName, Value: sess.ID})
	app.Engine.ServeHTTP(rec, req)
	want := cfg.AppUIBaseURL + "/auth/result?status=error"
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("tiktok callback redirect = %q, want %q", got, want)
	}
}

// clientIPFor serves one probe that echoes ClientIP for a peer and forwarded
// pair, so the trusted proxy setting is observable over HTTP.
func clientIPFor(t *testing.T, engine *gin.Engine, remoteAddr, forwarded string) string {
	t.Helper()
	engine.GET("/probe-client-ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/probe-client-ip", nil)
	req.RemoteAddr = remoteAddr
	req.Header.Set("X-Forwarded-For", forwarded)
	engine.ServeHTTP(rec, req)
	return rec.Body.String()
}
