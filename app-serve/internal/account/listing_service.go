// Package account holds the TikTok account aggregate and its persistence shapes.
//
// @file      internal/account/listing_service.go
// @for       The operator account listing and the credential clearing write.
// @uses      context, time
// @reason    The console reads its accounts and deletes them, so both paths need the ownership proof the aggregate already defines.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-16
package account

import (
	"context"
	"time"
)

// List returns the operator's accounts in created order as token free views.
// The list reads only one operator's rows, so a foreign account id is never on it.
func (s *Service) List(ctx context.Context, operatorID string) ([]View, error) {
	records, err := s.store.ListOwned(ctx, operatorID)
	if err != nil {
		return nil, err
	}
	views := make([]View, 0, len(records))
	for _, rec := range records {
		acc := newAccount(rec)
		views = append(views, acc.View())
	}
	return views, nil
}

// Forget clears the account's token material and marks it revoked. The
// ownership checked read runs first, so a foreign or unknown account answers
// not found before any write. It is idempotent by design: the Revoke method
// is a no-op on an already revoked account and the store's COALESCE keeps the
// original timestamp, so a retried DELETE still succeeds.
func (s *Service) Forget(ctx context.Context, operatorID string, accountID string) error {
	rec, err := s.store.GetOwned(ctx, operatorID, accountID)
	if err != nil {
		return err
	}
	acc := newAccount(rec)
	if err := acc.Revoke(time.Now().UTC()); err != nil {
		return err
	}
	return s.store.ClearCredentials(ctx, accountID, time.Now().UTC())
}
