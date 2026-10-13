// Package auth_test provides the shared lifecycle test fakes.
//
// @file      internal/auth/lifecycle_fakes_test.go
// @for       Recording fakes for the token refresher and the Redis lock.
// @uses      testing, sync, bytes, log/slog, miniredis/v2, internal/tiktokclient
// @reason    Lifecycle tests need to observe call counts without a real backend.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package auth

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// fakeExchanger and fakeUpserter are the OAuth flow fakes the callback
// tests use; the lifecycle methods record the calls Refresh and Revoke make.

type fakeExchanger struct {
	code   string
	tokens tiktokclient.TokenSet
	err    error
	calls  int
}

func (f *fakeExchanger) ExchangeCode(_ context.Context, code string) (tiktokclient.TokenSet, error) {
	f.calls++
	f.code = code
	return f.tokens, f.err
}

type fakeUpserter struct {
	operatorID string
	tokens     tiktokclient.TokenSet
	granted    []string
	id         string
	err        error
	calls      int

	refreshToken     string
	accessToken      string
	materialErr      error
	materialCalls    int
	materialOperator string
	materialAccount  string
	storeTokens      tiktokclient.TokenSet
	storeCalls       int
	storeErr         error
	needsReauth      int
	revoked          int
	revokeOperator   string
	revokeAccount    string
	markErr          error
}

func (f *fakeUpserter) UpsertAccount(_ context.Context, operatorID string, tokens tiktokclient.TokenSet, grantedScopes []string) (string, error) {
	f.calls++
	f.operatorID = operatorID
	f.tokens = tokens
	f.granted = grantedScopes
	return f.id, f.err
}

func (f *fakeUpserter) RefreshMaterial(_ context.Context, operatorID string, accountID string) (string, string, error) {
	f.materialCalls++
	f.materialOperator = operatorID
	f.materialAccount = accountID
	return f.refreshToken, f.accessToken, f.materialErr
}

func (f *fakeUpserter) StoreTokens(_ context.Context, _ string, tokens tiktokclient.TokenSet) error {
	f.storeCalls++
	f.storeTokens = tokens
	return f.storeErr
}

func (f *fakeUpserter) MarkNeedsReauth(_ context.Context, _ string) error {
	f.needsReauth++
	return f.markErr
}

func (f *fakeUpserter) MarkRevoked(_ context.Context, operatorID string, accountID string) error {
	f.revoked++
	f.revokeOperator = operatorID
	f.revokeAccount = accountID
	return f.markErr
}

// fakeRefresher records refresh and revoke calls. A non-nil gate blocks each
// refresh until it is closed, so a concurrency test can hold the lock open.
type fakeRefresher struct {
	mu           sync.Mutex
	tokens       tiktokclient.TokenSet
	refreshErr   error
	revokeErr    error
	gate         chan struct{}
	cancel       func()
	refreshCalls int
	refreshArg   string
	revokeCalls  int
	revokeArg    string
}

func (f *fakeRefresher) RefreshToken(_ context.Context, refresh string) (tiktokclient.TokenSet, error) {
	f.mu.Lock()
	f.refreshCalls++
	f.refreshArg = refresh
	gate := f.gate
	cancel := f.cancel
	f.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if gate != nil {
		<-gate
	}
	return f.tokens, f.refreshErr
}

func (f *fakeRefresher) Revoke(_ context.Context, access string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revokeCalls++
	f.revokeArg = access
	return f.revokeErr
}

func (f *fakeRefresher) refreshCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshCalls
}

func (f *fakeRefresher) revokeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.revokeCalls
}

// fakeLocker is an in-process Locker shared by concurrent tests.
type fakeLocker struct {
	mu           sync.Mutex
	held         map[string]bool
	acqErr       error
	relErr       error
	acquireCalls int
	released     []string
}

func newFakeLocker() *fakeLocker {
	return &fakeLocker{held: map[string]bool{}}
}

func (f *fakeLocker) Acquire(_ context.Context, key string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquireCalls++
	if f.acqErr != nil {
		return false, f.acqErr
	}
	if f.held[key] {
		return false, nil
	}
	f.held[key] = true
	return true, nil
}

func (f *fakeLocker) Release(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.held, key)
	f.released = append(f.released, key)
	return f.relErr
}

func (f *fakeLocker) releaseCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.released)
}

// recordingLocker captures the context state each Release runs under, so a
// test can prove the detached release still lands after the caller cancels.
type recordingLocker struct {
	releaseErr    error
	releaseCalls  int
	releaseCtxErr error
}

func (l *recordingLocker) Acquire(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return true, nil
}

func (l *recordingLocker) Release(ctx context.Context, _ string) error {
	l.releaseCalls++
	// Snapshot Err inside the call: the service cancels the context on return.
	l.releaseCtxErr = ctx.Err()
	return l.releaseErr
}

// newLifecycleService wires a service with the supplied lifecycle fakes.
func newLifecycleService(t *testing.T, refresher TokenRefresher, accounts AccountUpserter, locker Locker, logger *slog.Logger) (*Service, *SessionStore, *miniredis.Miniredis) {
	t.Helper()
	states, _ := newStateStore(t)
	sessions, mr := newSessionStore(t, time.Hour)
	cfg := ServiceConfig{
		ClientKey:   testClientKey,
		RedirectURI: testRedirectURI,
		Scopes:      testScopes,
		OperatorID:  testOperatorID,
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil))
	}
	svc := NewService(cfg, &fakeExchanger{}, refresher, accounts, states, sessions, locker, logger)
	return svc, sessions, mr
}
