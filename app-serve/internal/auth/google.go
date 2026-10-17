// Package auth issues and checks operator sessions and OAuth state.
//
// @file      internal/auth/google.go
// @for       Google OIDC sign in that mints an operator session.
// @uses      context, log/slog, strings, internal/apierror, internal/platform/googleoidc
// @reason    Operator login must prove the Google identity before a session exists.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-17
package auth

import (
	"context"
	"log/slog"
	"strings"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/googleoidc"
)

// GoogleProvider is the OIDC client the Google flow calls.
type GoogleProvider interface {
	AuthorizeURL(state, nonce string) string
	ExchangeCode(ctx context.Context, code string) (string, error)
	VerifyIDToken(ctx context.Context, raw, nonce string) (googleoidc.Claims, error)
}

// OperatorEnsurer persists the operator identity row before a session points at it.
type OperatorEnsurer interface {
	Ensure(ctx context.Context, id, email string) error
}

// GoogleConfig carries the allowlisted operator identity.
type GoogleConfig struct {
	OperatorID    string
	OperatorEmail string
}

// GoogleService orchestrates the Google sign in flow.
type GoogleService struct {
	cfg       GoogleConfig
	provider  GoogleProvider
	operators OperatorEnsurer
	states    *StateStore
	sessions  *SessionStore
	logger    *slog.Logger
}

// errGoogleForbidden is the client safe answer for a refused account. It stays
// generic on purpose: naming the checked address would confirm whether an
// operator account exists.
var errGoogleForbidden = apierror.New(apierror.CodeForbidden, 0, "This Google account is not allowed to sign in.")

// NewGoogleService wires the Google flow. A nil logger falls back to
// slog.Default so a wiring mistake is not read as a denied sign in.
func NewGoogleService(cfg GoogleConfig, provider GoogleProvider, operators OperatorEnsurer, states *StateStore, sessions *SessionStore, logger *slog.Logger) *GoogleService {
	if logger == nil {
		logger = slog.Default()
	}
	return &GoogleService{
		cfg:       cfg,
		provider:  provider,
		operators: operators,
		states:    states,
		sessions:  sessions,
		logger:    logger,
	}
}

// AuthorizeURL mints a nonce, stores it as the single use state payload, and
// returns the Google consent URL for the browser.
func (s *GoogleService) AuthorizeURL(ctx context.Context) (string, error) {
	nonce, err := randomToken()
	if err != nil {
		return "", errStateUnavailable(err)
	}
	state, err := s.states.Issue(ctx, nonce)
	if err != nil {
		return "", err
	}
	return s.provider.AuthorizeURL(state, nonce), nil
}

// Complete consumes the single use state, verifies the id token against the
// nonce it stored, and mints a session once the verified email matches the
// allowlist. A refused account is logged at warn for the operator to review.
func (s *GoogleService) Complete(ctx context.Context, code, state string) (*Session, error) {
	nonce, err := s.states.Consume(ctx, state)
	if err != nil {
		return nil, err
	}
	raw, err := s.provider.ExchangeCode(ctx, code)
	if err != nil {
		return nil, err
	}
	claims, err := s.provider.VerifyIDToken(ctx, raw, nonce)
	if err != nil {
		return nil, err
	}
	if !s.allowed(claims.Email) {
		s.logger.WarnContext(ctx, "refusing google sign in for a non-allowlisted account", "email", claims.Email)
		return nil, errGoogleForbidden
	}
	if err := s.operators.Ensure(ctx, s.cfg.OperatorID, claims.Email); err != nil {
		return nil, err
	}
	sess, err := mintSession(s.cfg.OperatorID, []string{})
	if err != nil {
		return nil, err
	}
	if err := s.sessions.Create(ctx, *sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// allowed reports whether the verified email matches the configured operator
// address, trimming surrounding space and ignoring case.
func (s *GoogleService) allowed(email string) bool {
	return strings.EqualFold(strings.TrimSpace(email), strings.TrimSpace(s.cfg.OperatorEmail))
}
