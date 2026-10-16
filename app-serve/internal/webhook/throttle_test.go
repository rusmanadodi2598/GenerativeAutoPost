// Package webhook proves the intake throttles by source IP.
//
// @file      internal/webhook/throttle_test.go
// @for       Tests for the per-IP intake throttle middleware.
// @uses      bytes, net/http, httptest, testing, gin, log/slog
// @reason    The 429 and the fail-open path are the contract SPEC 003 section 9 names.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-16
package webhook

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// fakeThrottle answers from a fixed verdict and records the keys it saw.
type fakeThrottle struct {
	allow bool
	err   error
	keys  []string
}

// Allow records the key and returns the canned verdict.
func (f *fakeThrottle) Allow(_ context.Context, key string) (bool, error) {
	f.keys = append(f.keys, key)
	return f.allow, f.err
}

// throttleRouter wires the middleware in front of a counting handler.
func throttleRouter(t *testing.T, throttle IPThrottle, logs *bytes.Buffer) (*gin.Engine, *int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(logs, nil))
	hits := 0
	router := gin.New()
	router.Use(Throttle(throttle, logger))
	router.POST("/webhooks/tiktok", func(c *gin.Context) {
		hits++
		c.Status(http.StatusOK)
	})
	return router, &hits
}

// TestThrottleDeniesOverBudgetIPWith429 proves an over-limit source is turned
// away before the handler runs, and the refusal is logged.
func TestThrottleDeniesOverBudgetIPWith429(t *testing.T) {
	var logs bytes.Buffer
	throttle := &fakeThrottle{allow: false}
	router, hits := throttleRouter(t, throttle, &logs)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/tiktok", nil)
	req.RemoteAddr = "203.0.113.7:4200"
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d: %s", rec.Code, rec.Body.String())
	}
	if *hits != 0 {
		t.Fatal("the handler must not run for a throttled request")
	}
	if !bytes.Contains(logs.Bytes(), []byte("throttled")) {
		t.Fatalf("the refusal was not logged: %q", logs.String())
	}
}

// TestThrottlePassesAnAllowedIP proves an inside-budget delivery reaches the
// handler, since the throttle must not become the intake.
func TestThrottlePassesAnAllowedIP(t *testing.T) {
	var logs bytes.Buffer
	throttle := &fakeThrottle{allow: true}
	router, hits := throttleRouter(t, throttle, &logs)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/tiktok", nil)
	req.RemoteAddr = "203.0.113.7:4200"
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || *hits != 1 {
		t.Fatalf("want one allowed hit, got status %d hits %d", rec.Code, *hits)
	}
}

// TestThrottleFailsOpenOnAStoreError proves a broken throttle does not drop
// deliveries: the signature check still guards the route.
func TestThrottleFailsOpenOnAStoreError(t *testing.T) {
	var logs bytes.Buffer
	throttle := &fakeThrottle{err: errors.New("redis down")}
	router, hits := throttleRouter(t, throttle, &logs)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/tiktok", nil)
	req.RemoteAddr = "203.0.113.7:4200"
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || *hits != 1 {
		t.Fatalf("want the delivery to proceed, got status %d hits %d", rec.Code, *hits)
	}
}

// TestThrottleKeysOnTheClientIPAlone proves two sources do not share a budget.
func TestThrottleKeysOnTheClientIPAlone(t *testing.T) {
	var logs bytes.Buffer
	throttle := &fakeThrottle{allow: true}
	router, _ := throttleRouter(t, throttle, &logs)

	for _, addr := range []string{"203.0.113.7:4200", "203.0.113.8:4200"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/webhooks/tiktok", nil)
		req.RemoteAddr = addr
		router.ServeHTTP(rec, req)
	}
	if len(throttle.keys) != 2 || throttle.keys[0] == throttle.keys[1] {
		t.Fatalf("want one distinct key per IP, got %v", throttle.keys)
	}
}
