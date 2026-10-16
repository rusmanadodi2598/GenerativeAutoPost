// Package media serves upload, lookup, and delete endpoints over one Service.
//
// @file      internal/media/handler_test.go
// @for       Coverage for the media upload handler status codes.
// @uses      net/http, httptest, testing
// @reason    Locks the upload status codes and the envelope shape clients see.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package media

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestShowHandlerRequiresSession(t *testing.T) {
	store := newFakeStore()
	requireSessionCase(t, http.MethodGet, seedObject(t, store, "acct-1"), store)
}

func TestDeleteHandlerRequiresSession(t *testing.T) {
	store := newFakeStore()
	id := seedObject(t, store, "acct-1")

	requireSessionCase(t, http.MethodDelete, id, store)

	if store.deleteCalls != 0 {
		t.Errorf("deleteCalls = %d, want 0", store.deleteCalls)
	}
	if _, ok := store.objects[id]; !ok {
		t.Errorf("object %q was deleted, want it kept", id)
	}
}

// TestUploadHandlerFailsWithoutConfigCap locks Fix 1: a non-positive service cap
// fails fast instead of running the upload uncapped.
func TestUploadHandlerFailsWithoutConfigCap(t *testing.T) {
	store := newFakeStore()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	svc := NewService(store, ServiceConfig{
		Driver:        string(MediaStorageDisk),
		MaxBytes:      0,
		MaxDurationMs: 60_000,
		AllowedTypes:  []string{"video/mp4"},
	}, slog.New(slog.DiscardHandler))
	session := func(*gin.Context) (string, bool) { return "acct-1", true }
	RegisterRoutes(router, NewHandler(svc, store, nil, session))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, uploadRequest(t, "video/mp4", "clip.mp4", "x",
		map[string]string{"kind": "video"}))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if len(store.objects) != 0 {
		t.Errorf("store holds %d objects, want 0", len(store.objects))
	}
}

func TestUploadHandlerRejectsBodyOverCap(t *testing.T) {
	store := newFakeStore()
	router := testRouter(t, store, "acct-1", 16)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, uploadRequest(t, "video/mp4", "clip.mp4", strings.Repeat("a", 4096),
		map[string]string{"kind": "video"}))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if len(store.objects) != 0 {
		t.Errorf("store holds %d objects, want 0", len(store.objects))
	}
}
