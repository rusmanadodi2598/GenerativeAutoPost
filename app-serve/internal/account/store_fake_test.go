// Package account holds the TikTok account aggregate and its persistence shapes.
//
// @file      internal/account/store_fake_test.go
// @for       The in-memory Store fake shared by the account service tests.
// @uses      context, sync, time, internal/apierror
// @reason    Every service test needs a store that enforces the same ownership scoping as the repository.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     util
// @stability stable
// @since     2026-10-16
package account

import (
	"context"
	"sync"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// fakeStore is a hand-written Store. Records are keyed by open id so an upsert
// for a seen open id behaves like a database unique constraint.
type fakeStore struct {
	mu      sync.Mutex
	records map[string]Record
	updates []tokenUpdate

	upsertErr error
	writeErr  error

	getCalls    int
	getOperator string
	needsCalls  map[string]bool
	revokeCalls []string
	clearCalls  []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{records: map[string]Record{}, needsCalls: map[string]bool{}}
}

func (f *fakeStore) UpsertEncrypted(_ context.Context, rec Record) (Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsertErr != nil {
		return Record{}, f.upsertErr
	}
	if prev, ok := f.records[rec.OpenID]; ok {
		rec.ID = prev.ID
	} else {
		rec.ID = "acct-" + rec.OpenID
	}
	f.records[rec.OpenID] = rec
	return rec, nil
}

func (f *fakeStore) GetOwned(_ context.Context, operatorID string, accountID string) (Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	f.getOperator = operatorID
	for _, rec := range f.records {
		if rec.ID == accountID && rec.OperatorID == operatorID {
			return rec, nil
		}
	}
	return Record{}, errTestNotFound
}

// ListOwned returns every record owned by the operator.
func (f *fakeStore) ListOwned(_ context.Context, operatorID string) ([]Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Record
	for _, rec := range f.records {
		if rec.OperatorID == operatorID {
			out = append(out, rec)
		}
	}
	return out, nil
}

// GetSystem loads by id with no operator filter, which is the sweep path.
func (f *fakeStore) GetSystem(_ context.Context, accountID string) (Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rec := range f.records {
		if rec.ID == accountID {
			return rec, nil
		}
	}
	return Record{}, errTestNotFound
}

func (f *fakeStore) UpdateTokens(_ context.Context, accountID string, accessEnc, refreshEnc []byte, keyID string, accessExp, refreshExp time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.updates = append(f.updates, tokenUpdate{accountID, accessEnc, refreshEnc, keyID, accessExp, refreshExp})
	return nil
}

func (f *fakeStore) SetNeedsReauth(_ context.Context, accountID string, needs bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.needsCalls[accountID] = needs
	return nil
}

func (f *fakeStore) SetRevoked(_ context.Context, accountID string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.revokeCalls = append(f.revokeCalls, accountID)
	stamp := at.UTC()
	for openID, rec := range f.records {
		if rec.ID == accountID {
			rec.RevokedAt = &stamp
			f.records[openID] = rec
		}
	}
	return nil
}

// ClearCredentials drops the stored token material and stamps the revoke time.
func (f *fakeStore) ClearCredentials(_ context.Context, accountID string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.clearCalls = append(f.clearCalls, accountID)
	stamp := at.UTC()
	for openID, rec := range f.records {
		if rec.ID == accountID {
			rec.RevokedAt = &stamp
			rec.AccessEnc = nil
			rec.RefreshEnc = nil
			f.records[openID] = rec
		}
	}
	return nil
}

// errTestNotFound is the store denial the vault must propagate unchanged.
var errTestNotFound = apierror.New(apierror.CodeNotFound, 0, "Account not found.")
