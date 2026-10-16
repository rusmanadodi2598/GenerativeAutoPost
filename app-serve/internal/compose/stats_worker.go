// Package compose adapts the account service to the stats sweep seams.
//
// @file      internal/compose/stats_worker.go
// @for       Stats worker adapters: the connected account id lister.
// @uses      context, internal/account
// @reason    The worker declares a narrow lister port that must meet the account service here.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-16
package compose

import (
	"context"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/account"
)

// accountLister is the account service slice the stats sweep reads.
type accountLister interface {
	List(ctx context.Context, operatorID string) ([]account.View, error)
}

// accountIDs adapts the account service to the worker's id lister.
type accountIDs struct {
	svc accountLister
}

// List returns the connected account ids in creation order.
func (a accountIDs) List(ctx context.Context, operatorID string) ([]string, error) {
	views, err := a.svc.List(ctx, operatorID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(views))
	for _, view := range views {
		ids = append(ids, view.ID)
	}
	return ids, nil
}
