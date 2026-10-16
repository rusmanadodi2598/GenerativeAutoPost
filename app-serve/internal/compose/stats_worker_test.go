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

// fakeAccountLister replays one account listing.
type fakeAccountLister struct{ views []account.View }

// List returns the canned views, ignoring the operator filter.
func (f fakeAccountLister) List(context.Context, string) ([]account.View, error) {
	return f.views, nil
}

// TestAccountIDsMapsTheOwnedAccountsInOrder proves the adapter drops the views
// and keeps only the ids, in the order the account service returned them.
func TestAccountIDsMapsTheOwnedAccountsInOrder(t *testing.T) {
	ids, err := accountIDs{svc: fakeAccountLister{views: []account.View{{ID: "a1"}, {ID: "a2"}}}}.List(context.Background(), "op")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 2 || ids[0] != "a1" || ids[1] != "a2" {
		t.Fatalf("ids = %v", ids)
	}
}
