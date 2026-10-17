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

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

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
		"SESSION_TTL": "24h", "LOG_LEVEL": "info",
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
