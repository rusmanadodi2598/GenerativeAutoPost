// Package insight covers the privacy options lookup publish guards on.
//
// @file      internal/insight/creator_test.go
// @for       CreatorOptions tests for the cache-first privacy level lookup.
// @uses      context, errors, testing, time, internal/apierror, internal/tiktokclient
// @reason    Publish rejects a privacy level outside these options, so the lookup must never answer from an unowned account.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-15
package insight

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// TestCreatorOptionsGivenFreshSnapshot_ThenUpstreamIsNotCalled proves the
// cache answers inside its window, so an init burst spends no upstream quota.
func TestCreatorOptionsGivenFreshSnapshot_ThenUpstreamIsNotCalled(t *testing.T) {
	h := newServiceHarness(t)
	h.store.creators = []CreatorSnapshot{{
		AccountID:      testAccount,
		PrivacyOptions: []string{"SELF_ONLY", "FOLLOWERS"},
		FetchedAt:      h.svc.cfg.Now(),
	}}
	options, err := h.svc.CreatorOptions(context.Background(), testOperator, testAccount)
	if err != nil {
		t.Fatalf("CreatorOptions returned %v", err)
	}
	if len(options) != 2 || options[0] != "SELF_ONLY" {
		t.Fatalf("options = %v", options)
	}
	if h.tiktok.creatorCalls != 0 {
		t.Fatalf("QueryCreatorInfo calls = %d, want 0", h.tiktok.creatorCalls)
	}
}

// TestCreatorOptionsGivenNoSnapshot_ThenFetchedAndStored proves a cold cache
// reads upstream and appends the snapshot for the next lookup.
func TestCreatorOptionsGivenNoSnapshot_ThenFetchedAndStored(t *testing.T) {
	h := newServiceHarness(t)
	h.tiktok.creator = tiktokclient.CreatorInfo{PrivacyLevelOptions: []string{"PUBLIC_TO_FOLLOWERS"}}
	options, err := h.svc.CreatorOptions(context.Background(), testOperator, testAccount)
	if err != nil {
		t.Fatalf("CreatorOptions returned %v", err)
	}
	if len(options) != 1 || options[0] != "PUBLIC_TO_FOLLOWERS" {
		t.Fatalf("options = %v", options)
	}
	if h.tiktok.creatorCalls != 1 {
		t.Fatalf("QueryCreatorInfo calls = %d, want 1", h.tiktok.creatorCalls)
	}
	if len(h.store.creators) != 1 || h.store.creators[0].AccountID != testAccount {
		t.Fatalf("creator snapshot rows = %+v", h.store.creators)
	}
}

// TestCreatorOptionsGivenStaleSnapshot_ThenRefetched proves a window older than
// the TTL is refetched, since TikTok changes the allowed options itself.
func TestCreatorOptionsGivenStaleSnapshot_ThenRefetched(t *testing.T) {
	h := newServiceHarness(t)
	h.store.creators = []CreatorSnapshot{{
		AccountID:      testAccount,
		PrivacyOptions: []string{"SELF_ONLY"},
		FetchedAt:      h.svc.cfg.Now().Add(-2 * time.Minute),
	}}
	h.tiktok.creator = tiktokclient.CreatorInfo{PrivacyLevelOptions: []string{"SELF_ONLY", "FRIENDS"}}
	options, err := h.svc.CreatorOptions(context.Background(), testOperator, testAccount)
	if err != nil {
		t.Fatalf("CreatorOptions returned %v", err)
	}
	if len(options) != 2 {
		t.Fatalf("options = %v, want the refetched pair", options)
	}
	if h.tiktok.creatorCalls != 1 {
		t.Fatalf("QueryCreatorInfo calls = %d, want 1", h.tiktok.creatorCalls)
	}
}

// TestCreatorOptionsGivenNoSnapshotAndUpstreamError_ThenErrorPropagates proves
// an empty option list is never returned, since that would reject every
// privacy level as a mismatch.
func TestCreatorOptionsGivenNoSnapshotAndUpstreamError_ThenErrorPropagates(t *testing.T) {
	h := newServiceHarness(t)
	h.tiktok.err = apierror.New(apierror.CodeTikTokUnavailable, 0, "Upstream is down.")
	options, err := h.svc.CreatorOptions(context.Background(), testOperator, testAccount)
	if err == nil {
		t.Fatalf("CreatorOptions returned %v with no error", options)
	}
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) || apiErr.Code != apierror.CodeTikTokUnavailable {
		t.Fatalf("err = %v, want the upstream error unchanged", err)
	}
	if options != nil {
		t.Fatalf("options = %v alongside the error", options)
	}
}

// TestCreatorOptionsGivenStaleSnapshotAndFailedRefetch_ThenStaleOptionsAreServed
// proves a TikTok outage degrades to the stored options rather than blocking
// every init with an error.
func TestCreatorOptionsGivenStaleSnapshotAndFailedRefetch_ThenStaleOptionsAreServed(t *testing.T) {
	h := newServiceHarness(t)
	h.store.creators = []CreatorSnapshot{{
		AccountID:      testAccount,
		PrivacyOptions: []string{"SELF_ONLY"},
		FetchedAt:      h.svc.cfg.Now().Add(-2 * time.Minute),
	}}
	h.tiktok.err = apierror.New(apierror.CodeTikTokUnavailable, 0, "Upstream is down.")
	options, err := h.svc.CreatorOptions(context.Background(), testOperator, testAccount)
	if err != nil {
		t.Fatalf("CreatorOptions returned %v", err)
	}
	if len(options) != 1 || options[0] != "SELF_ONLY" {
		t.Fatalf("options = %v, want the stale pair", options)
	}
}

// TestCreatorOptionsGivenForeignAccount_ThenOwnershipIsCheckedFirst proves the
// upstream call never runs for an account the operator does not own.
func TestCreatorOptionsGivenForeignAccount_ThenOwnershipIsCheckedFirst(t *testing.T) {
	h := newServiceHarness(t)
	h.svc.cfg.Accounts = &fakeAccounts{err: apierror.New(apierror.CodeNotFound, 0, "Account not found.")}
	options, err := h.svc.CreatorOptions(context.Background(), testOperator, testAccount)
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) || apiErr.Code != apierror.CodeNotFound {
		t.Fatalf("err = %v", err)
	}
	if options != nil {
		t.Fatalf("options = %v alongside the denial", options)
	}
	if h.tiktok.creatorCalls != 0 {
		t.Fatalf("QueryCreatorInfo calls = %d, want 0", h.tiktok.creatorCalls)
	}
}

// TestCreatorOptionsGivenUpstreamOmittingOptions_ThenEmptySlice proves the
// answer is an empty list, not nil, so a caller cannot mistake it for a fault.
func TestCreatorOptionsGivenUpstreamOmittingOptions_ThenEmptySlice(t *testing.T) {
	h := newServiceHarness(t)
	h.tiktok.creator = tiktokclient.CreatorInfo{}
	options, err := h.svc.CreatorOptions(context.Background(), testOperator, testAccount)
	if err != nil {
		t.Fatalf("CreatorOptions returned %v", err)
	}
	if options == nil || len(options) != 0 {
		t.Fatalf("options = %v, want an empty slice", options)
	}
}
