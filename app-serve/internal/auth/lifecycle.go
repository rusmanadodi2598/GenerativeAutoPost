// Package auth issues and checks operator sessions and OAuth state.
//
// @file      internal/auth/lifecycle.go
// @for       Token refresh, credential revoke, and operator logout.
// @uses      context, errors, time, log/slog, internal/apierror
// @reason    Credentials must refresh once per account and revoke cleanly.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// refreshLockKeyPrefix namespaces the per-account refresh lock.
const refreshLockKeyPrefix = "genauto:auth:refresh-lock:"

// defaultLockTTL bounds a refresh claim when config sets no override.
const defaultLockTTL = 30 * time.Second

// refreshLockKey builds the per-account lock key.
func refreshLockKey(accountID string) string {
	return refreshLockKeyPrefix + accountID
}

// lockTTL returns the configured refresh lock lifetime or the default.
func (s *Service) lockTTL() time.Duration {
	if s.cfg.LockTTL > 0 {
		return s.cfg.LockTTL
	}
	return defaultLockTTL
}

// errRefreshInFlight is the client safe answer for a concurrent refresh. A
// fresh value per call keeps concurrent Wrap calls off shared state.
func errRefreshInFlight() *apierror.Error {
	return apierror.New(apierror.CodeConflict, 0, "A refresh for this account is already running.")
}

// isReauthRequired reports whether a TikTok error demands a new sign in.
func isReauthRequired(err error) bool {
	var apiErr *apierror.Error
	return errors.As(err, &apiErr) && apiErr.Code == apierror.CodeTikTokReauthRequired
}

// Refresh rotates the TikTok tokens of one account behind a per-account lock.
// A reauth failure marks the account and returns at once; there is no retry
// loop, because a rejected refresh token stays rejected.
func (s *Service) Refresh(ctx context.Context, accountID string) error {
	key := refreshLockKey(accountID)
	acquired, err := s.locker.Acquire(ctx, key, s.lockTTL())
	if err != nil {
		return err
	}
	if !acquired {
		return errRefreshInFlight()
	}
	defer func() {
		if relErr := s.locker.Release(ctx, key); relErr != nil {
			s.logger.WarnContext(ctx, "releasing refresh lock", "account_id", accountID, "error", relErr)
		}
	}()

	refreshToken, _, err := s.accounts.RefreshMaterial(ctx, accountID)
	if err != nil {
		return err
	}
	tokens, err := s.refresher.RefreshToken(ctx, refreshToken)
	if err != nil {
		if isReauthRequired(err) {
			if markErr := s.accounts.MarkNeedsReauth(ctx, accountID); markErr != nil {
				s.logger.WarnContext(ctx, "marking account needs reauth", "account_id", accountID, "error", markErr)
			}
		}
		return err
	}
	return s.accounts.StoreTokens(ctx, accountID, tokens)
}

// Revoke invalidates the TikTok credentials and clears the local token state.
// A failed upstream revoke is logged and dropped: the local record is cleared
// regardless, so a dead token never lingers in our store.
func (s *Service) Revoke(ctx context.Context, accountID string) error {
	_, accessToken, err := s.accounts.RefreshMaterial(ctx, accountID)
	if err != nil {
		return err
	}
	if err := s.refresher.Revoke(ctx, accessToken); err != nil {
		s.logger.WarnContext(ctx, "revoking tiktok access token", "account_id", accountID, "error", err)
	}
	return s.accounts.MarkRevoked(ctx, accountID)
}

// Logout deletes the operator session. A store failure is propagated so a
// logged out browser never keeps a live server side session.
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	return s.sessions.Delete(ctx, sessionID)
}

// compile time check that the Redis lock satisfies Locker.
var _ Locker = (*RedisLocker)(nil)
