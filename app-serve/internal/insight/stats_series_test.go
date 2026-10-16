// Package insight covers the stats series service rules.
//
// @file      internal/insight/stats_series_test.go
// @for       Service tests for the ranged stats series and range parsing.
// @uses      context, errors, testing, time, internal/apierror, internal/tiktokclient
// @reason    Pins the window cut, the single refetch, and the range rejection.
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

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

func TestStatsSeriesGivenPointsAcrossWindows_ThenOnlyTheWindowIsReturned(t *testing.T) {
	h := newServiceHarness(t)
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	h.svc.cfg.Now = func() time.Time { return now }
	h.store.stats = []StatSnapshot{
		{VideoID: testVideo, ViewCount: count(int64(1)), FetchedAt: now.Add(-8 * 24 * time.Hour)},
		{VideoID: testVideo, ViewCount: count(int64(2)), FetchedAt: now.Add(-2 * 24 * time.Hour)},
		{VideoID: testVideo, ViewCount: count(int64(3)), FetchedAt: now.Add(-30 * time.Second)},
	}
	h.tiktok.videos = []tiktokclient.Video{{ID: testVideo, ViewCount: count(int64(3))}}
	series, err := h.svc.StatsSeries(context.Background(), testOperator, testAccount, testVideo, StatRange7d)
	if err != nil {
		t.Fatalf("StatsSeries: %v", err)
	}
	if series.Range != StatRange7d || len(series.Points) != 2 {
		t.Fatalf("series = %+v", series)
	}
	if *series.Points[0].ViewCount != 2 || *series.Points[1].ViewCount != 3 {
		t.Fatalf("points = %+v", series.Points)
	}
	if series.Truncated {
		t.Fatal("short history must not be marked truncated")
	}
	if len(h.tiktok.queryIDs) != 0 {
		t.Fatalf("fresh latest point must skip the upstream, ids = %v", h.tiktok.queryIDs)
	}
}

func TestStatsSeriesGivenForeignWindow_ThenValidationFails(t *testing.T) {
	h := newServiceHarness(t)
	_, err := h.svc.StatsSeries(context.Background(), testOperator, testAccount, testVideo, StatRange("1y"))
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) || apiErr.Code != apierror.CodeValidationFailed {
		t.Fatalf("err = %v, want VALIDATION_FAILED", err)
	}
}

func TestStatsSeriesGivenStaleLatestPoint_ThenExactlyOneRefetch(t *testing.T) {
	h := newServiceHarness(t)
	h.store.stats = []StatSnapshot{{
		VideoID: testVideo, ViewCount: count(int64(5)),
		FetchedAt: time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC),
	}}
	h.tiktok.videos = []tiktokclient.Video{{ID: testVideo, ViewCount: count(int64(9))}}
	series, err := h.svc.StatsSeries(context.Background(), testOperator, testAccount, testVideo, StatRange7d)
	if err != nil {
		t.Fatalf("StatsSeries: %v", err)
	}
	if len(series.Points) != 2 {
		t.Fatalf("points = %+v", series.Points)
	}
	if h.store.writes != 1 {
		t.Fatalf("writes = %d, want exactly one refetch", h.store.writes)
	}
}

func TestRangeParsingAcceptsTheClosedSet(t *testing.T) {
	for raw, want := range map[string]StatRange{
		"": StatRange30d, "7d": StatRange7d, "30d": StatRange30d, "90d": StatRange90d,
	} {
		got, err := parseRange(raw)
		if err != nil || got != want {
			t.Errorf("parseRange(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	if _, err := parseRange("1y"); err == nil {
		t.Error("parseRange(1y) accepted, want rejection")
	}
}
