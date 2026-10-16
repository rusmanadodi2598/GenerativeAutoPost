// Package insight covers the insight HTTP surface.
//
// @file      internal/insight/creator_info_http_test.go
// @for       Route tests for POST /accounts/{id}/creator-info.
// @uses      net/http/httptest, encoding/json, testing
// @reason    Pins the envelope and the ownership-before-cache order of the creator info route.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-16
package insight

import (
	"encoding/json"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestCreatorInfoRouteAnswersTheCachedSnapshot proves the route serves the
// whole contract shape, so app-ui can read the options and the freshness.
func TestCreatorInfoRouteAnswersTheCachedSnapshot(t *testing.T) {
	h := newHarness(t)
	h.store.creators = []CreatorSnapshot{{
		AccountID:               testAccount,
		PrivacyOptions:          []string{"SELF_ONLY"},
		CreatorUsername:         "creator.username",
		CreatorNickname:         "Creator Nick",
		CreatorAvatarURL:        "https://cdn.example.test/avatar.jpg",
		MaxVideoPostDurationSec: 600,
		FetchedAt:               h.svc.cfg.Now(),
	}}

	rec := h.do(t, "POST", "/accounts/"+testAccount+"/creator-info", "")
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	if envelope.Data["creator_username"] != "creator.username" {
		t.Fatalf("want the creator username, got %v", envelope.Data["creator_username"])
	}
	if _, ok := envelope.Data["fetched_at"]; !ok {
		t.Fatal("the response must carry fetched_at so app-ui can judge freshness")
	}
	if opts, ok := envelope.Data["privacy_level_options"].([]any); !ok || len(opts) != 1 {
		t.Fatalf("want one privacy option, got %v", envelope.Data["privacy_level_options"])
	}
}

// TestCreatorInfoRouteWithoutASessionAnswers401 proves the guard runs before
// the cache is read.
func TestCreatorInfoRouteWithoutASessionAnswers401(t *testing.T) {
	h := newHarnessWithSession(t, func(*gin.Context) (string, bool) { return "", false })
	rec := h.do(t, "POST", "/accounts/"+testAccount+"/creator-info", "")
	if rec.Code != 401 {
		t.Fatalf("want 401 without a session, got %d: %s", rec.Code, rec.Body.String())
	}
}
