// Package insight covers the video stats sweep worker.
//
// @file      internal/insight/worker_test.go
// @for       Sweep tests for refresh, paging, batching, and the list budget.
// @uses      context, errors, fmt, io, log/slog, testing, time, internal/tiktokclient
// @reason    Pins per-account isolation, the batch cap, and the prune cutoff.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     worker
// @stability stable
// @since     2026-10-16
package insight

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// fakeRefresher records refreshes and can fail one account.
type fakeRefresher struct {
	calls  []string
	errors map[string]error
}

// Refresh records the account and returns its scripted error, if any.
func (f *fakeRefresher) Refresh(_ context.Context, _, accountID string) error {
	f.calls = append(f.calls, accountID)
	if f.errors != nil {
		return f.errors[accountID]
	}
	return nil
}

// fakeLister returns a fixed account list.
type fakeLister struct {
	accounts []string
}

func (f *fakeLister) List(context.Context, string) ([]string, error) {
	return f.accounts, nil
}

// fakeGate allows a fixed number of lists per account.
type fakeGate struct {
	calls   []string
	allowed map[string]int
}

// Allow records the account and spends one unit of its list budget.
func (f *fakeGate) Allow(_ context.Context, accountID string) error {
	f.calls = append(f.calls, accountID)
	if f.allowed == nil {
		return nil
	}
	if f.allowed[accountID] <= 0 {
		return errors.New("list budget spent")
	}
	f.allowed[accountID]--
	return nil
}

// workerHarness wires a worker over the insight fakes.
type workerHarness struct {
	worker  *StatsWorker
	videos  *fakeTikTok
	store   *fakeStore
	refresh *fakeRefresher
	gate    *fakeGate
	now     time.Time
}

// newWorkerHarness builds a worker with a fixed clock and the given accounts.
func newWorkerHarness(t *testing.T, accounts []string) *workerHarness {
	t.Helper()
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	h := &workerHarness{
		videos:  &fakeTikTok{},
		store:   &fakeStore{},
		refresh: &fakeRefresher{},
		gate:    &fakeGate{},
		now:     now,
	}
	h.worker = NewStatsWorker(StatsWorkerConfig{
		OperatorID: testOperator,
		Videos:     h.videos,
		Accounts:   &fakeAccounts{access: "access-token"},
		Refresher:  h.refresh,
		Lister:     &fakeLister{accounts: accounts},
		Gate:       h.gate,
		Store:      h.store,
		Now:        func() time.Time { return now },
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return h
}

func TestSweepOnceGivenTwoAccounts_ThenEachIsRefreshedAndAppended(t *testing.T) {
	h := newWorkerHarness(t, []string{"a1", "a2"})
	h.videos.pages = []tiktokclient.VideoPage{
		{Videos: []tiktokclient.Video{{ID: "v1"}}},
		{Videos: []tiktokclient.Video{{ID: "v2"}}},
	}
	h.videos.videos = []tiktokclient.Video{{ID: "v1"}, {ID: "v2"}}
	h.videos.filterQueries = true
	if err := h.worker.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce returned %v", err)
	}
	if len(h.refresh.calls) != 2 || h.refresh.calls[0] != "a1" || h.refresh.calls[1] != "a2" {
		t.Fatalf("refresher calls = %v", h.refresh.calls)
	}
	if h.store.writes != 2 {
		t.Fatalf("store writes = %d, want 2", h.store.writes)
	}
	if h.store.prunes != 1 {
		t.Fatalf("prune calls = %d, want 1", h.store.prunes)
	}
	if want := h.now.Add(-168 * time.Hour); !h.store.pruneCutoff.Equal(want) {
		t.Fatalf("prune cutoff = %s, want %s", h.store.pruneCutoff, want)
	}
}

func TestSweepOnceGivenOneAccountRefreshFails_ThenOtherAccountsStillSweep(t *testing.T) {
	h := newWorkerHarness(t, []string{"a1", "a2"})
	h.refresh.errors = map[string]error{"a1": errors.New("token refresh failed")}
	h.videos.pages = []tiktokclient.VideoPage{
		{Videos: []tiktokclient.Video{{ID: "v1"}}},
	}
	h.videos.videos = []tiktokclient.Video{{ID: "v1"}}
	if err := h.worker.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce returned %v", err)
	}
	if h.store.writes != 1 {
		t.Fatalf("store writes = %d, want 1", h.store.writes)
	}
	if h.videos.listCalls != 1 {
		t.Fatalf("list calls = %d, want 1", h.videos.listCalls)
	}
}

func TestSweepOnceGivenListBudgetSpent_ThenPartialIDsAreAppended(t *testing.T) {
	h := newWorkerHarness(t, []string{"a1", "a2"})
	h.gate.allowed = map[string]int{"a1": 1, "a2": 1}
	h.videos.pages = []tiktokclient.VideoPage{
		{Videos: []tiktokclient.Video{{ID: "a1v1"}, {ID: "a1v2"}}, Cursor: 9, HasMore: true},
		{Videos: []tiktokclient.Video{{ID: "a2v"}}},
	}
	h.videos.videos = []tiktokclient.Video{{ID: "a1v1"}, {ID: "a1v2"}, {ID: "a2v"}}
	h.videos.filterQueries = true
	if err := h.worker.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce returned %v", err)
	}
	if len(h.videos.queryBatches) != 2 {
		t.Fatalf("query batches = %v", h.videos.queryBatches)
	}
	if got := h.videos.queryBatches[0]; len(got) != 2 || got[0] != "a1v1" || got[1] != "a1v2" {
		t.Fatalf("first batch = %v, want the first account's partial ids", got)
	}
	if got := h.videos.queryBatches[1]; len(got) != 1 || got[0] != "a2v" {
		t.Fatalf("second batch = %v, want the second account's video", got)
	}
	if h.store.writes != 3 {
		t.Fatalf("store writes = %d, want 3", h.store.writes)
	}
	if len(h.gate.calls) == 0 || h.gate.calls[len(h.gate.calls)-1] != "a2" {
		t.Fatalf("gate calls = %v, want the second account swept", h.gate.calls)
	}
}

func TestSweepOnceGivenMoreThanTwentyIDs_ThenQueriedInBatches(t *testing.T) {
	h := newWorkerHarness(t, []string{"a1"})
	videos := make([]tiktokclient.Video, 25)
	for i := range videos {
		videos[i] = tiktokclient.Video{ID: fmt.Sprintf("v%02d", i)}
	}
	h.videos.pages = []tiktokclient.VideoPage{{Videos: videos}}
	h.videos.videos = videos
	h.videos.filterQueries = true
	if err := h.worker.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce returned %v", err)
	}
	if len(h.videos.queryBatches) != 2 {
		t.Fatalf("query batches = %d, want 2", len(h.videos.queryBatches))
	}
	if len(h.videos.queryBatches[0]) != 20 || len(h.videos.queryBatches[1]) != 5 {
		t.Fatalf("batch sizes = %d and %d, want 20 and 5",
			len(h.videos.queryBatches[0]), len(h.videos.queryBatches[1]))
	}
	if h.store.writes != 25 {
		t.Fatalf("store writes = %d, want 25", h.store.writes)
	}
}

func TestSweepOnceGivenPagedCatalog_ThenEveryPageIsWalked(t *testing.T) {
	h := newWorkerHarness(t, []string{"a1"})
	h.videos.pages = []tiktokclient.VideoPage{
		{Videos: []tiktokclient.Video{{ID: "v1"}}, Cursor: 5, HasMore: true},
		{Videos: []tiktokclient.Video{{ID: "v2"}}},
	}
	h.videos.videos = []tiktokclient.Video{{ID: "v1"}, {ID: "v2"}}
	h.videos.filterQueries = true
	if err := h.worker.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce returned %v", err)
	}
	if h.videos.listCalls != 2 {
		t.Fatalf("list calls = %d, want 2", h.videos.listCalls)
	}
	if len(h.videos.listArgs) != 4 || h.videos.listArgs[3] != "5" {
		t.Fatalf("list args = %v, want the second call cursor 5", h.videos.listArgs)
	}
	if len(h.videos.queryBatches) != 1 || len(h.videos.queryBatches[0]) != 2 {
		t.Fatalf("query batches = %v, want both ids in one batch", h.videos.queryBatches)
	}
	if h.store.writes != 2 {
		t.Fatalf("store writes = %d, want 2", h.store.writes)
	}
}
