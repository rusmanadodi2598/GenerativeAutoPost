// Package compose adapts the account service to the stats sweep seams.
//
// @file      internal/compose/stats_worker_test.go
// @for       Adapter tests for the stats sweep account id lister.
// @uses      context, testing, internal/account
// @reason    The id lister is the only place the worker meets the account service.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-16
package compose

import (
	"context"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/account"
)

// fakeAccountLister replays one account listing and records the operator filter.
type fakeAccountLister struct {
	views      []account.View
	operatorID string
}

// List returns the canned views and records the operator filter.
func (f *fakeAccountLister) List(_ context.Context, operatorID string) ([]account.View, error) {
	f.operatorID = operatorID
	return f.views, nil
}

// TestAccountIDsSkipsRevokedAndKeepsNeedsReauth proves a revoked view drops out
// while a needs-reauth view stays, in the order the account service returned them.
func TestAccountIDsSkipsRevokedAndKeepsNeedsReauth(t *testing.T) {
	fake := &fakeAccountLister{views: []account.View{
		{ID: "revoked-1", Revoked: true},
		{ID: "reauth-1", NeedsReauth: true},
		{ID: "active-1"},
	}}
	ids, err := accountIDs{svc: fake}.List(context.Background(), "op-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 2 || ids[0] != "reauth-1" || ids[1] != "active-1" {
		t.Fatalf("ids = %v", ids)
	}
	if fake.operatorID != "op-1" {
		t.Fatalf("operator = %q, want op-1", fake.operatorID)
	}
}
