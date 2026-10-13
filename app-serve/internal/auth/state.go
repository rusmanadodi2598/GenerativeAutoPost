// Package auth issues and checks operator sessions and OAuth state.
//
// @file      internal/auth/state.go
// @for       Single use OAuth state values stored in Redis.
// @uses      context, crypto/rand, encoding/base64, redis/go-redis/v9, internal/apierror, internal/platform/redisx
// @reason    The TikTok callback needs a replay proof bound to the browser.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/redisx"
)

// stateTTL bounds how long a sign in attempt may sit half finished.
const stateTTL = 10 * time.Minute

// errStateUnauthorized is the client safe answer for a spent or absent state.
var errStateUnauthorized = apierror.New(apierror.CodeUnauthorized, 0, "The authorization attempt expired or was already used.")

// errStateUnavailable is the client safe answer for a Redis outage.
var errStateUnavailable = apierror.New(apierror.CodeServiceUnavailable, 0, "Authorization storage is unavailable. Try again shortly.")

// StateStore keeps OAuth state values in Redis until the callback spends them.
type StateStore struct {
	client *redis.Client
}

// NewStateStore wires a state store over the shared Redis client.
func NewStateStore(client *redis.Client) *StateStore {
	return &StateStore{client: client}
}

// Issue mints a random state bound to the requested scopes and stores it.
func (s *StateStore) Issue(ctx context.Context, requestedScopes string) (string, error) {
	state, err := randomToken()
	if err != nil {
		return "", errStateUnavailable.Wrap(err)
	}
	if err := s.client.Set(ctx, redisx.KeyOAuthState+state, requestedScopes, stateTTL).Err(); err != nil {
		return "", errStateUnavailable.Wrap(err)
	}
	return state, nil
}

// Consume atomically reads and deletes the state, returning its scopes.
func (s *StateStore) Consume(ctx context.Context, state string) (string, error) {
	if state == "" {
		return "", errStateUnauthorized
	}
	scopes, err := s.client.GetDel(ctx, redisx.KeyOAuthState+state).Result()
	if errors.Is(err, redis.Nil) {
		return "", errStateUnauthorized
	}
	if err != nil {
		return "", errStateUnavailable.Wrap(err)
	}
	return scopes, nil
}

// randomToken returns 32 random bytes as base64url without padding.
func randomToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}
