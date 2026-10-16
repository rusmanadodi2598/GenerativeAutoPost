// Package insight fakes the collaborators the insight tests depend on.
//
// @file      internal/insight/fakes_test.go
// @for       Fakes for the upstream client, the account resolver, and the snapshot store.
// @uses      context, testing, internal/apierror, internal/tiktokclient
// @reason    Keeps the service and handler tests free of PostgreSQL and TikTok.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package insight

import (
	"context"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

const testOperator = "11111111-1111-7111-8111-111111111111"

var (
	testAccount = "22222222-2222-7222-8222-222222222222"
	testVideo   = "7300000000000000000"
)

// count returns a pointer to a count value for the fixtures below.
func count(value int64) *int64 { return &value }

// fakeTikTok serves canned upstream reads and records the arguments it got.
type fakeTikTok struct {
	page         tiktokclient.VideoPage
	videos       []tiktokclient.Video
	info         tiktokclient.UserInfo
	creator      tiktokclient.CreatorInfo
	err          error
	listArgs     []string
	queryIDs     []string
	queryUser    []string
	creatorCalls int
}

// ListVideos records the cursor and returns the canned page.
func (f *fakeTikTok) ListVideos(_ context.Context, access, cursor string, _ int) (tiktokclient.VideoPage, error) {
	f.listArgs = append(f.listArgs, access, cursor)
	if f.err != nil {
		return tiktokclient.VideoPage{}, f.err
	}
	return f.page, nil
}

// QueryVideos records the ids and returns the canned videos.
func (f *fakeTikTok) QueryVideos(_ context.Context, _ string, ids, fields []string) ([]tiktokclient.Video, error) {
	f.queryIDs = append(f.queryIDs, ids...)
	f.queryUser = fields
	if f.err != nil {
		return nil, f.err
	}
	return f.videos, nil
}

// GetUserInfo returns the canned user info.
func (f *fakeTikTok) GetUserInfo(_ context.Context, _ string, fields []string) (tiktokclient.UserInfo, error) {
	f.queryUser = fields
	if f.err != nil {
		return tiktokclient.UserInfo{}, f.err
	}
	return f.info, nil
}

// QueryCreatorInfo returns the canned creator record.
func (f *fakeTikTok) QueryCreatorInfo(_ context.Context, _ string) (tiktokclient.CreatorInfo, error) {
	f.creatorCalls++
	if f.err != nil {
		return tiktokclient.CreatorInfo{}, f.err
	}
	return f.creator, nil
}

// fakeAccounts resolves any owned account to a fixed access token.
type fakeAccounts struct {
	access string
	err    error
}

// Owned returns the access token for an owned account.
func (f *fakeAccounts) Owned(context.Context, string, string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.access, nil
}

// fakeStore keeps the snapshots in memory and counts the writes.
type fakeStore struct {
	stats       []StatSnapshot
	creators    []CreatorSnapshot
	statsErr    error
	creatorsErr error
	writes      int
}

// InsertStats appends the snapshot.
func (f *fakeStore) InsertStats(_ context.Context, snap StatSnapshot) (StatSnapshot, error) {
	if f.statsErr != nil {
		return StatSnapshot{}, f.statsErr
	}
	f.writes++
	snap.ID = "snap-1"
	f.stats = append(f.stats, snap)
	return snap, nil
}

// ListStats returns the stored points inside the window, oldest first, capped
// at limit. It mirrors the repository's newest-first read and reverse.
func (f *fakeStore) ListStats(_ context.Context, videoID string, since time.Time, limit int) ([]StatSnapshot, bool, error) {
	var window []StatSnapshot
	for _, snap := range f.stats {
		if snap.VideoID == videoID && !snap.FetchedAt.Before(since) {
			window = append(window, snap)
		}
	}
	truncated := len(window) > limit
	if truncated {
		window = window[len(window)-limit:]
	}
	return window, truncated, nil
}

// InsertCreatorSnapshot appends the snapshot.
func (f *fakeStore) InsertCreatorSnapshot(_ context.Context, rec CreatorSnapshot) (CreatorSnapshot, error) {
	if f.creatorsErr != nil {
		return CreatorSnapshot{}, f.creatorsErr
	}
	f.creators = append(f.creators, rec)
	rec.ID = "creator-1"
	return rec, nil
}

// LatestCreatorSnapshot returns the newest snapshot, or a not found error.
func (f *fakeStore) LatestCreatorSnapshot(context.Context, string) (CreatorSnapshot, error) {
	if len(f.creators) == 0 {
		return CreatorSnapshot{}, apierror.New(apierror.CodeNotFound, 0, "Creator snapshot not found.")
	}
	return f.creators[len(f.creators)-1], nil
}

// DeleteStatsBefore drops every stored point older than the cutoff.
func (f *fakeStore) DeleteStatsBefore(_ context.Context, cutoff time.Time) (int64, error) {
	kept := f.stats[:0]
	var removed int64
	for _, snap := range f.stats {
		if snap.FetchedAt.Before(cutoff) {
			removed++
			continue
		}
		kept = append(kept, snap)
	}
	f.stats = kept
	return removed, nil
}

// svcHarness is the service level fixture for the business rule tests.
type svcHarness struct {
	svc    *Service
	tiktok *fakeTikTok
	store  *fakeStore
}

// newServiceHarness builds the service with a fixed clock and one minute cache.
func newServiceHarness(t *testing.T) *svcHarness {
	t.Helper()
	h := &svcHarness{tiktok: &fakeTikTok{}, store: &fakeStore{}}
	h.svc = NewService(Config{
		Videos:     h.tiktok,
		Accounts:   &fakeAccounts{access: "access-token"},
		Store:      h.store,
		Now:        func() time.Time { return time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC) },
		CreatorTTL: time.Minute,
	})
	return h
}
