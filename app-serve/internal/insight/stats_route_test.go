// Package insight covers the stats series HTTP surface.
//
// @file      internal/insight/stats_route_test.go
// @for       Route tests for the ranged stats series.
// @uses      net/http/httptest, testing, time, internal/apierror, internal/tiktokclient
// @reason    Pins the series envelope, the closed range set, and history order.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-16
package insight

import (
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

func TestStatsRouteAnswersTheSeries(t *testing.T) {
	h := newHarness(t)
	h.tiktok.videos = []tiktokclient.Video{{ID: testVideo, ViewCount: count(11)}}
	rec := h.do(t, "GET", "/videos/"+testVideo+"/stats?account_id="+testAccount, "")
	if rec.Code != 200 {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	data := decodeData(t, rec)
	if data["video_id"] != testVideo || data["range"] != "30d" {
		t.Fatalf("series head = %+v", data)
	}
	points, ok := data["points"].([]any)
	if !ok || len(points) != 1 {
		t.Fatalf("points = %+v", data["points"])
	}
	if points[0].(map[string]any)["fetched_at"] != "2026-10-12T09:00:00Z" {
		t.Fatalf("fetched_at = %v", points[0])
	}
	if h.store.writes != 1 {
		t.Fatalf("snapshot writes = %d", h.store.writes)
	}
}

func TestStatsRouteRejectsAnUnknownRange(t *testing.T) {
	h := newHarness(t)
	rec := h.do(t, "GET", "/videos/"+testVideo+"/stats?account_id="+testAccount+"&range=1y", "")
	if rec.Code != 422 || errorCode(t, rec) != string(apierror.CodeValidationFailed) {
		t.Fatalf("status = %d code = %s", rec.Code, errorCode(t, rec))
	}
}

func TestStatsRouteKeepsStoredHistoryBehindTheFreshestPoint(t *testing.T) {
	h := newHarness(t)
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	h.store.stats = []StatSnapshot{
		{VideoID: testVideo, ViewCount: count(1), FetchedAt: now.Add(-time.Hour)},
		{VideoID: testVideo, ViewCount: count(2), FetchedAt: now.Add(-2 * time.Minute)},
	}
	h.tiktok.videos = []tiktokclient.Video{{ID: testVideo, ViewCount: count(11)}}
	rec := h.do(t, "GET", "/videos/"+testVideo+"/stats?account_id="+testAccount+"&range=7d", "")
	if rec.Code != 200 {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	data := decodeData(t, rec)
	points, ok := data["points"].([]any)
	if !ok || len(points) != 3 {
		t.Fatalf("points = %+v", data["points"])
	}
	// The stale newest point triggers exactly one refetch, and the older
	// stored points still come back with it, oldest first.
	if h.store.writes != 1 {
		t.Fatalf("snapshot writes = %d, want 1", h.store.writes)
	}
	first := points[0].(map[string]any)
	last := points[2].(map[string]any)
	if first["fetched_at"] != "2026-10-12T08:00:00Z" || last["fetched_at"] != "2026-10-12T09:00:00Z" {
		t.Fatalf("points are not in ascending order: %+v", points)
	}
}
