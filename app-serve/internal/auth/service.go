// Package auth issues and checks operator sessions and OAuth state.
//
// @file      internal/auth/service.go
// @for       TikTok OAuth authorize URL and callback exchange.
// @uses      context, log/slog, net/url, strings, time, internal/apierror, internal/tiktokclient
// @reason    One place turns a browser redirect into an operator session.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package auth

import (
	"context"
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
	RefreshMaterial(ctx context.Context, accountID string) (refreshToken string, accessToken string, err error)
	StoreTokens(ctx context.Context, accountID string, tokens tiktokclient.TokenSet) error
	MarkNeedsReauth(ctx context.Context, accountID string) error
	MarkRevoked(ctx context.Context, accountID string) error
}

// ServiceConfig carries the settings the OAuth flow reads.
type ServiceConfig struct {
	ClientKey    string
	RedirectURI  string
	Scopes       string
	AppUIBaseURL string
	OperatorID   string
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

// AuthorizeURL issues a single use state and builds the TikTok redirect. An
// empty requestedScopes falls back to the configured default set.
func (s *Service) AuthorizeURL(ctx context.Context, requestedScopes string) (string, error) {
	scopes := requestedScopes
	if scopes == "" {
		scopes = s.cfg.Scopes
	}
	state, err := s.states.Issue(ctx, scopes)
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

// CompleteCallback consumes the state, exchanges the code, upserts the
// account, and creates the operator session. A spent or unknown state fails
// before the code is exchanged, so a replayed callback does no TikTok work.
func (s *Service) CompleteCallback(ctx context.Context, code, state string) (*Session, error) {
	if _, err := s.states.Consume(ctx, state); err != nil {
		return nil, err
	}
	tokens, err := s.tokens.ExchangeCode(ctx, code)
	if err != nil {
		return nil, err
	}
	granted := splitScopes(tokens.Scope)
	if _, err := s.accounts.UpsertAccount(ctx, s.cfg.OperatorID, tokens, granted); err != nil {
		s.logger.ErrorContext(ctx, "upserting tiktok account after oauth callback", "error", err)
		return nil, err
	}
	sess, err := s.newSession(granted)
	if err != nil {
		return nil, err
	}
	if err := s.sessions.Create(ctx, *sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// newSession mints a session id, CSRF token, and creation time for the
// configured operator and the granted scopes.
func (s *Service) newSession(granted []string) (*Session, error) {
	id, err := NewSessionID()
	if err != nil {
		return nil, errSessionIssue(err)
	}
	csrf, err := NewCSRFToken()
	if err != nil {
		return nil, errSessionIssue(err)
	}
	return &Session{
		ID:         id,
		OperatorID: s.cfg.OperatorID,
		CSRFToken:  csrf,
		Scopes:     granted,
		CreatedAt:  time.Now().UTC(),
	}, nil
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
