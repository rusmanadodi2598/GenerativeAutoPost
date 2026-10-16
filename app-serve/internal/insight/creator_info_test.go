// Package insight serves the privacy options the publish guard validates against.
//
// @file      internal/insight/creator_info_test.go
// @for       Tests for the full creator info read behind POST /accounts/{id}/creator-info.
// @uses      testing, context, time
// @reason    The console needs the profile fields alongside the privacy options, and both must come from one cache.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-16
package insight

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// errUpstreamDown stands in for a TikTok failure on the refetch path.
var errUpstreamDown = errors.New("upstream unavailable")

// TestCreatorInfoReturnsTheCachedProfileInsideTheWindow proves a fresh snapshot
// answers without touching TikTok, since SPEC 003 7.2 serves it from a cache.
func TestCreatorInfoReturnsTheCachedProfileInsideTheWindow(t *testing.T) {
	h := newServiceHarness(t)
	h.store.creators = []CreatorSnapshot{{
		AccountID:               testAccount,
		PrivacyOptions:          []string{"PUBLIC_TO_FOLLOWERS"},
		CreatorUsername:         "creator.username",
		CreatorNickname:         "Creator Nick",
		CreatorAvatarURL:        "https://cdn.example.test/avatar.jpg",
		MaxVideoPostDurationSec: 600,
		FetchedAt:               h.svc.cfg.Now(),
	}}

	info, err := h.svc.CreatorInfo(context.Background(), testOperator, testAccount)
	if err != nil {
		t.Fatalf("CreatorInfo returned %v", err)
	}
	if h.tiktok.creatorCalls != 0 {
		t.Fatalf("a fresh snapshot must not call TikTok, got %d calls", h.tiktok.creatorCalls)
	}
	if info.CreatorUsername != "creator.username" || info.CreatorNickname != "Creator Nick" {
		t.Fatalf("cached profile did not survive: %+v", info)
	}
	if info.CreatorAvatarURL != "https://cdn.example.test/avatar.jpg" {
		t.Fatalf("cached avatar did not survive: %+v", info)
	}
	if len(info.PrivacyLevelOptions) != 1 {
		t.Fatalf("cached options did not survive: %+v", info)
	}
}

// TestCreatorInfoRefetchesWhenTheSnapshotIsStale proves the read refreshes once
// the window passes, otherwise the console would publish with expired options.
func TestCreatorInfoRefetchesWhenTheSnapshotIsStale(t *testing.T) {
	h := newServiceHarness(t)
	h.store.creators = []CreatorSnapshot{{
		AccountID:      testAccount,
		PrivacyOptions: []string{"MUTUAL_ONLY_FOLLOWERS"},
		FetchedAt:      h.svc.cfg.Now().Add(-2 * time.Hour),
	}}
	h.tiktok.creator = tiktokclient.CreatorInfo{
		CreatorUsername:         "fresh.username",
		PrivacyLevelOptions:     []string{"PUBLIC_TO_FOLLOWERS", "SELF_ONLY"},
		MaxVideoPostDurationSec: 900,
	}

	info, err := h.svc.CreatorInfo(context.Background(), testOperator, testAccount)
	if err != nil {
		t.Fatalf("CreatorInfo returned %v", err)
	}
	if h.tiktok.creatorCalls != 1 {
		t.Fatalf("want one upstream refetch, got %d", h.tiktok.creatorCalls)
	}
	if info.CreatorUsername != "fresh.username" {
		t.Fatalf("want the refreshed profile, got %+v", info)
	}
	if len(h.store.creators) != 2 {
		t.Fatalf("the refetch must be cached for the next reader, got %d snapshots", len(h.store.creators))
	}
}

// TestCreatorInfoFallsBackToTheStaleSnapshotWhenTikTokFails proves a TikTok
// outage does not blank the publish form, since the cached options are still
// the last thing TikTok confirmed.
func TestCreatorInfoFallsBackToTheStaleSnapshotWhenTikTokFails(t *testing.T) {
	h := newServiceHarness(t)
	h.store.creators = []CreatorSnapshot{{
		AccountID:      testAccount,
		PrivacyOptions: []string{"FRIENDS"},
		FetchedAt:      h.svc.cfg.Now().Add(-2 * time.Hour),
	}}
	h.tiktok.err = errUpstreamDown

	info, err := h.svc.CreatorInfo(context.Background(), testOperator, testAccount)
	if err != nil {
		t.Fatalf("CreatorInfo returned %v", err)
	}
	if len(info.PrivacyLevelOptions) != 1 || info.PrivacyLevelOptions[0] != "FRIENDS" {
		t.Fatalf("want the stale options, got %+v", info)
	}
	// The old fetched_at is the signal the console reads for freshness, so it
	// must not be restamped when the answer came from the cache.
	if !info.FetchedAt.Before(h.svc.cfg.Now()) {
		t.Fatalf("the fallback must keep the original fetched_at, got %v", info.FetchedAt)
	}
}
