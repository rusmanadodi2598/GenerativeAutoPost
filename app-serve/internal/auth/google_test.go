// Package auth_test pins the Google sign in flow.
//
// @file      internal/auth/google_test.go
// @for       Google sign in tests over the real state and session stores.
// @uses      testing, context, bytes, errors, log/slog, net/url, strings, time, miniredis/v2, internal/apierror, internal/platform/googleoidc
// @reason    The Google flow is the interactive path that mints an operator session.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-17
package auth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/googleoidc"
)

// fakeGoogleProvider stands in for googleoidc.Client. It records the nonce it
// handed out and only accepts a token minted with that same nonce, which is
// what the real signature and nonce check enforces.
type fakeGoogleProvider struct {
	nonce         string
	tokenNonce    string
	raw           string
	claims        googleoidc.Claims
	exchangeErr   error
	verifyErr     error
	exchangeCalls int
}

func (f *fakeGoogleProvider) AuthorizeURL(state, nonce string) string {
	f.nonce = nonce
	return "https://accounts.google.com/o/oauth2/v2/auth?state=" + state + "&nonce=" + nonce
}

func (f *fakeGoogleProvider) ExchangeCode(_ context.Context, _ string) (string, error) {
	f.exchangeCalls++
	return f.raw, f.exchangeErr
}

func (f *fakeGoogleProvider) VerifyIDToken(_ context.Context, _ string, nonce string) (googleoidc.Claims, error) {
	if f.verifyErr != nil {
		return googleoidc.Claims{}, f.verifyErr
	}
	if nonce != f.tokenNonce {
		return googleoidc.Claims{}, errors.New("googleoidc: token nonce does not match")
	}
	return f.claims, nil
}

// fakeOperatorEnsurer records the row writes Ensure performs.
type fakeOperatorEnsurer struct {
	id    string
	email string
	calls int
	err   error
}

func (f *fakeOperatorEnsurer) Ensure(_ context.Context, id, email string) error {
	f.calls++
	f.id = id
	f.email = email
	return f.err
}

// googleHarness bundles the Google service with the stores and log its tests
// inspect.
type googleHarness struct {
	svc       *GoogleService
	provider  *fakeGoogleProvider
	operators *fakeOperatorEnsurer
	sessions  *SessionStore
	sessionMR *miniredis.Miniredis
	logs      *bytes.Buffer
}

func newGoogleHarness(t *testing.T) *googleHarness {
	t.Helper()
	states, _ := newStateStore(t)
	sessions, sessionMR := newSessionStore(t, time.Hour)
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	provider := &fakeGoogleProvider{raw: "raw-id-token"}
	operators := &fakeOperatorEnsurer{}
	cfg := GoogleConfig{OperatorID: "op-1", OperatorEmail: "operator@example.com"}
	svc := NewGoogleService(cfg, provider, operators, states, sessions, logger)
	return &googleHarness{
		svc:       svc,
		provider:  provider,
		operators: operators,
		sessions:  sessions,
		sessionMR: sessionMR,
		logs:      logs,
	}
}

// authorize runs one AuthorizeURL call and returns the state from the URL and
// the nonce the provider received.
func (h *googleHarness) authorize(t *testing.T) (state, nonce string) {
	t.Helper()
	raw, err := h.svc.AuthorizeURL(context.Background())
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parsing authorize url: %v", err)
	}
	return parsed.Query().Get("state"), h.provider.nonce
}

// verifiedFor returns a harness whose next token is minted with the nonce
// issued during authorize and carries the supplied email.
func (h *googleHarness) verifiedFor(t *testing.T, email string) string {
	t.Helper()
	state, nonce := h.authorize(t)
	h.provider.tokenNonce = nonce
	h.provider.claims = googleoidc.Claims{Subject: "sub-1", Email: email, EmailVerified: true}
	return state
}

func TestGoogleComplete_MintsASessionForTheAllowlistedEmail(t *testing.T) {
	h := newGoogleHarness(t)
	state := h.verifiedFor(t, "operator@example.com")

	sess, err := h.svc.Complete(context.Background(), "auth-code-1", state)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if sess.OperatorID != "op-1" {
		t.Fatalf("operator = %q, want op-1", sess.OperatorID)
	}
	if len(sess.Scopes) != 0 {
		t.Fatalf("scopes = %v, want an empty list", sess.Scopes)
	}
	assertRandomToken(t, sess.ID)
	assertRandomToken(t, sess.CSRFToken)
	if sess.CreatedAt.Location() != time.UTC || sess.CreatedAt.IsZero() {
		t.Fatalf("created at = %s, want a UTC timestamp", sess.CreatedAt)
	}
	stored, err := h.sessions.Get(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("reading stored session: %v", err)
	}
	if stored.OperatorID != "op-1" {
		t.Fatalf("stored operator = %q, want op-1", stored.OperatorID)
	}
}

func TestGoogleComplete_RejectsANonAllowlistedEmail(t *testing.T) {
	h := newGoogleHarness(t)
	state := h.verifiedFor(t, "intruder@example.com")

	sess, err := h.svc.Complete(context.Background(), "auth-code-1", state)
	wantCode(t, err, apierror.CodeForbidden)
	if sess != nil {
		t.Fatalf("session = %+v, want nil", sess)
	}
	if h.operators.calls != 0 {
		t.Fatalf("Ensure ran %d times for a refused account", h.operators.calls)
	}
	if !strings.Contains(h.logs.String(), "intruder@example.com") {
		t.Fatalf("warn log %q does not carry the refused address", h.logs.String())
	}
	if keys := h.sessionMR.Keys(); len(keys) != 0 {
		t.Fatalf("session keys %v were written for a refused account", keys)
	}
}

func TestGoogleComplete_MatchesTheAllowlistCaseInsensitively(t *testing.T) {
	h := newGoogleHarness(t)
	state := h.verifiedFor(t, "  Operator@Example.COM  ")

	sess, err := h.svc.Complete(context.Background(), "auth-code-1", state)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if sess == nil || sess.OperatorID != "op-1" {
		t.Fatalf("session = %+v, want a session for op-1", sess)
	}
}

func TestGoogleComplete_RejectsAReplayedState(t *testing.T) {
	h := newGoogleHarness(t)
	state := h.verifiedFor(t, "operator@example.com")
	ctx := context.Background()

	if _, err := h.svc.Complete(ctx, "auth-code-1", state); err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	_, err := h.svc.Complete(ctx, "auth-code-1", state)
	wantCode(t, err, apierror.CodeUnauthorized)
	if h.provider.exchangeCalls != 1 {
		t.Fatalf("exchange ran %d times, want once for the first callback", h.provider.exchangeCalls)
	}
}

func TestGoogleComplete_RejectsATokenWithTheWrongNonce(t *testing.T) {
	h := newGoogleHarness(t)
	state, _ := h.authorize(t)
	// The token was minted in another attempt, so its nonce never matched the
	// one this state issued.
	h.provider.tokenNonce = "a-nonce-from-another-attempt"
	h.provider.claims = googleoidc.Claims{Email: "operator@example.com", EmailVerified: true}

	sess, err := h.svc.Complete(context.Background(), "auth-code-1", state)
	if err == nil {
		t.Fatal("expected a wrong nonce to fail the callback")
	}
	if sess != nil {
		t.Fatalf("session = %+v, want nil", sess)
	}
	if h.operators.calls != 0 {
		t.Fatalf("Ensure ran %d times for a rejected token", h.operators.calls)
	}
}

func TestGoogleComplete_EnsuresTheOperatorRow(t *testing.T) {
	h := newGoogleHarness(t)
	state := h.verifiedFor(t, "operator@example.com")

	if _, err := h.svc.Complete(context.Background(), "auth-code-1", state); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if h.operators.calls != 1 {
		t.Fatalf("Ensure ran %d times, want once", h.operators.calls)
	}
	if h.operators.id != "op-1" {
		t.Fatalf("Ensure id = %q, want op-1", h.operators.id)
	}
	if h.operators.email != "operator@example.com" {
		t.Fatalf("Ensure email = %q, want operator@example.com", h.operators.email)
	}
}
