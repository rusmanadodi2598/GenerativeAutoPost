// Package auth_test pins the OAuth callback exchange.
//
// @file      internal/auth/service_callback_test.go
// @for       CompleteCallback session creation and failure tests.
// @uses      testing, context, reflect, time, internal/apierror, internal/tiktokclient
// @reason    The callback is the only path that turns a browser into a session.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package auth

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

func TestServiceCompleteCallbackHappyPath(t *testing.T) {
	tokens := tiktokclient.TokenSet{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		OpenID:       "open-1",
		Scope:        "user.info.basic,video.publish",
	}
	exchanger := &fakeExchanger{tokens: tokens}
	upserter := &fakeUpserter{id: "acct-1"}
	h := newHarness(t, exchanger, upserter)
	state := issueState(t, h.svc)

	sess, err := h.svc.CompleteCallback(context.Background(), "auth-code-1", state)
	if err != nil {
		t.Fatalf("CompleteCallback: %v", err)
	}
	if sess.OperatorID != testOperatorID {
		t.Fatalf("operator = %q, want %q", sess.OperatorID, testOperatorID)
	}
	assertRandomToken(t, sess.ID)
	assertRandomToken(t, sess.CSRFToken)
	want := []string{"user.info.basic", "video.publish"}
	if !reflect.DeepEqual(sess.Scopes, want) {
		t.Fatalf("scopes = %v, want %v", sess.Scopes, want)
	}
	if sess.CreatedAt.Location() != time.UTC || sess.CreatedAt.IsZero() {
		t.Fatalf("created at = %s, want a UTC timestamp", sess.CreatedAt)
	}
	if exchanger.code != "auth-code-1" {
		t.Fatalf("exchanged code = %q, want auth-code-1", exchanger.code)
	}
	if upserter.operatorID != testOperatorID {
		t.Fatalf("upsert operator = %q, want %q", upserter.operatorID, testOperatorID)
	}
	if !reflect.DeepEqual(upserter.granted, want) {
		t.Fatalf("upsert granted = %v, want %v", upserter.granted, want)
	}
	if !reflect.DeepEqual(upserter.tokens, tokens) {
		t.Fatalf("upsert tokens = %+v, want %+v", upserter.tokens, tokens)
	}
	stored, err := h.sessions.Get(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("reading stored session: %v", err)
	}
	if !reflect.DeepEqual(stored.Scopes, want) {
		t.Fatalf("stored scopes = %v, want %v", stored.Scopes, want)
	}
}

func TestServiceCompleteCallbackRejectsReusedState(t *testing.T) {
	exchanger := &fakeExchanger{tokens: tiktokclient.TokenSet{Scope: "user.info.basic"}}
	h := newHarness(t, exchanger, &fakeUpserter{id: "acct-1"})
	ctx := context.Background()
	state := issueState(t, h.svc)

	if _, err := h.svc.CompleteCallback(ctx, "auth-code-1", state); err != nil {
		t.Fatalf("first CompleteCallback: %v", err)
	}
	_, err := h.svc.CompleteCallback(ctx, "auth-code-1", state)
	wantCode(t, err, apierror.CodeUnauthorized)
}

func TestServiceCompleteCallbackRejectsUnknownState(t *testing.T) {
	exchanger := &fakeExchanger{tokens: tiktokclient.TokenSet{Scope: "user.info.basic"}}
	h := newHarness(t, exchanger, &fakeUpserter{id: "acct-1"})

	_, err := h.svc.CompleteCallback(context.Background(), "auth-code-1", "never-issued")
	wantCode(t, err, apierror.CodeUnauthorized)
	if exchanger.calls != 0 {
		t.Fatalf("exchange ran %d times on an unknown state", exchanger.calls)
	}
}

func TestServiceCompleteCallbackKeepsPartialScopes(t *testing.T) {
	exchanger := &fakeExchanger{tokens: tiktokclient.TokenSet{Scope: "user.info.basic"}}
	upserter := &fakeUpserter{id: "acct-1"}
	h := newHarness(t, exchanger, upserter)
	state := issueState(t, h.svc)

	sess, err := h.svc.CompleteCallback(context.Background(), "auth-code-1", state)
	if err != nil {
		t.Fatalf("CompleteCallback: %v", err)
	}
	want := []string{"user.info.basic"}
	if !reflect.DeepEqual(sess.Scopes, want) {
		t.Fatalf("scopes = %v, want only the granted set %v", sess.Scopes, want)
	}
	if !reflect.DeepEqual(upserter.granted, want) {
		t.Fatalf("upsert granted = %v, want only the granted set %v", upserter.granted, want)
	}
}

func TestServiceCompleteCallbackExchangeFailure(t *testing.T) {
	exchanger := &fakeExchanger{err: apierror.New(apierror.CodeTikTokUnavailable, 0, "TikTok is down.")}
	upserter := &fakeUpserter{id: "acct-1"}
	h := newHarness(t, exchanger, upserter)
	state := issueState(t, h.svc)

	sess, err := h.svc.CompleteCallback(context.Background(), "auth-code-1", state)
	wantCode(t, err, apierror.CodeTikTokUnavailable)
	if sess != nil {
		t.Fatalf("session = %+v, want nil on exchange failure", sess)
	}
	if upserter.calls != 0 {
		t.Fatalf("upsert ran %d times on exchange failure", upserter.calls)
	}
	if keys := h.mr.Keys(); len(keys) != 0 {
		t.Fatalf("session keys %v were written on exchange failure", keys)
	}
}
