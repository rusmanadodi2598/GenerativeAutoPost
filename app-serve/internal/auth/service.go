// Package auth issues and checks operator sessions and OAuth state.
//
// @file      internal/auth/service.go
// @for       TikTok OAuth authorize URL and callback exchange.
// @uses      context, crypto/sha256, encoding/hex, log/slog, net/url, strings, time, internal/apierror, internal/tiktokclient
// @reason    One place turns a browser redirect into an operator session.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// authorizeEndpoint is the TikTok Login Kit browser redirect target.
const authorizeEndpoint = "https://www.tiktok.com/v2/auth/authorize/"

// TokenExchanger trades an authorization code for a token set.
type TokenExchanger interface {
	ExchangeCode(ctx context.Context, code string) (tiktokclient.TokenSet, error)
}

// TokenRefresher renews and revokes the TikTok credentials of an account.
type TokenRefresher interface {
	RefreshToken(ctx context.Context, refresh string) (tiktokclient.TokenSet, error)
	Revoke(ctx context.Context, access string) error
}

// AccountUpserter persists the account token state the auth flow mutates.
type AccountUpserter interface {
	UpsertAccount(ctx context.Context, operatorID string, tokens tiktokclient.TokenSet, grantedScopes []string) (string, error)
	// RefreshMaterial returns the token material for one account. The store
	// enforces that the account belongs to operatorID and denies by default,
	// answering a foreign or unknown account with a not-found error.
	RefreshMaterial(ctx context.Context, operatorID string, accountID string) (refreshToken string, accessToken string, err error)
	StoreTokens(ctx context.Context, operatorID string, accountID string, tokens tiktokclient.TokenSet) error
	MarkNeedsReauth(ctx context.Context, operatorID string, accountID string) error
	// Forget clears the stored token material and marks the account revoked.
	// It is the revoke path: stamping the flag alone would leave a usable
	// ciphertext behind for a credential TikTok already invalidated.
	Forget(ctx context.Context, operatorID string, accountID string) error
}

// ServiceConfig carries the settings the OAuth flow reads.
type ServiceConfig struct {
	ClientKey    string
	RedirectURI  string
	Scopes       string
	AppUIBaseURL string
	LockTTL      time.Duration
}

// Service orchestrates the TikTok sign in flow.
type Service struct {
	cfg       ServiceConfig
	tokens    TokenExchanger
	refresher TokenRefresher
	accounts  AccountUpserter
	states    *StateStore
	sessions  *SessionStore
	locker    Locker
	logger    *slog.Logger
}

// NewService wires the OAuth service. A nil logger falls back to
// slog.Default so a wiring mistake is not read as a storage outage.
func NewService(
	cfg ServiceConfig,
	tokens TokenExchanger,
	refresher TokenRefresher,
	accounts AccountUpserter,
	states *StateStore,
	sessions *SessionStore,
	locker Locker,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		cfg:       cfg,
		tokens:    tokens,
		refresher: refresher,
		accounts:  accounts,
		states:    states,
		sessions:  sessions,
		locker:    locker,
		logger:    logger,
	}
}

// errSessionIssue is the client safe answer for a session mint failure. It is
// INTERNAL rather than SERVICE_UNAVAILABLE because no Redis call is involved.
func errSessionIssue(cause error) *apierror.Error {
	return apierror.New(apierror.CodeInternal, 0, "Could not start a session. Try again.").Wrap(cause)
}

// sessionBinding derives the per browser value the OAuth state binds to. The
// operator id is identical for every session in the single operator model, so
// it cannot tell two browsers apart; the session id can. Hashing keeps the raw
// session id out of the state store.
func sessionBinding(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:])
}

// AuthorizeURL issues a single use state bound to the browser session and
// builds the TikTok redirect; empty requestedScopes falls back to the
// configured set.
func (s *Service) AuthorizeURL(ctx context.Context, sessionID, requestedScopes string) (string, error) {
	scopes := requestedScopes
	if scopes == "" {
		scopes = s.cfg.Scopes
	}
	state, err := s.states.Issue(ctx, sessionBinding(sessionID))
	if err != nil {
		return "", err
	}
	query := url.Values{}
	query.Set("client_key", s.cfg.ClientKey)
	query.Set("scope", scopes)
	query.Set("redirect_uri", s.cfg.RedirectURI)
	query.Set("state", state)
	query.Set("response_type", "code")
	return authorizeEndpoint + "?" + query.Encode(), nil
}

// CallbackURL builds the browser return URL for the OAuth result page. An
// empty app base URL yields the path only, so a same origin deployment works.
func (s *Service) CallbackURL(status string) string {
	path := "/auth/result?status=" + url.QueryEscape(status)
	if s.cfg.AppUIBaseURL == "" {
		return path
	}
	return s.cfg.AppUIBaseURL + path
}

// CompleteCallback proves the state was issued to this browser session, then
// exchanges the code and upserts the account for that operator. A foreign or
// spent state fails before the exchange, and no session is minted here.
func (s *Service) CompleteCallback(ctx context.Context, operatorID, sessionID, code, state string) error {
	issuedTo, err := s.states.Consume(ctx, state)
	if err != nil {
		return err
	}
	if issuedTo != sessionBinding(sessionID) {
		return errStateUnauthorized
	}
	tokens, err := s.tokens.ExchangeCode(ctx, code)
	if err != nil {
		return err
	}
	granted := splitScopes(tokens.Scope)
	if _, err := s.accounts.UpsertAccount(ctx, operatorID, tokens, granted); err != nil {
		s.logger.ErrorContext(ctx, "upserting tiktok account after oauth callback", "error", err)
		return err
	}
	return nil
}

// splitScopes turns the comma joined scope string from TikTok into a list,
// dropping empty entries so a trailing comma adds no blank scope.
func splitScopes(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
