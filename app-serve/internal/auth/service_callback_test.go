// Package auth_test pins the OAuth callback account upsert.
//
// @file      internal/auth/service_callback_test.go
// @for       CompleteCallback upsert, session binding, and failure tests.
// @uses      testing, context, reflect, internal/apierror, internal/tiktokclient
// @reason    The callback connects an account and must not mint a session.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package auth

import (
	"context"
	"reflect"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// browserSession builds one session value for operatorID under the given id,
// so a test can hand the callback two distinct browsers that share one
// operator. It is not stored: the callback under test must not write sessions.
func browserSession(t *testing.T, operatorID, id string) Session {
	t.Helper()
	sess, err := mintSession(operatorID, nil)
	if err != nil {
		t.Fatalf("mint session: %v", err)
	}
	sess.ID = id
	return *sess
}

func TestCompleteCallback_UpsertsOnly(t *testing.T) {
	tokens := tiktokclient.TokenSet{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		OpenID:       "open-1",
		Scope:        "user.info.basic,video.publish",
	}
	exchanger := &fakeExchanger{tokens: tokens}
	upserter := &fakeUpserter{id: "acct-1"}
	h := newHarness(t, exchanger, upserter)
	sess := browserSession(t, "op-9", "browser-9")
	state := issueState(t, h.svc, sess.ID)

	if err := h.svc.CompleteCallback(context.Background(), "op-9", sess.ID, "auth-code-1", state); err != nil {
		t.Fatalf("CompleteCallback: %v", err)
	}
	want := []string{"user.info.basic", "video.publish"}
	if exchanger.code != "auth-code-1" {
		t.Fatalf("exchanged code = %q, want auth-code-1", exchanger.code)
	}
	if upserter.operatorID != "op-9" {
		t.Fatalf("upsert operator = %q, want op-9", upserter.operatorID)
	}
	if !reflect.DeepEqual(upserter.granted, want) {
		t.Fatalf("upsert granted = %v, want %v", upserter.granted, want)
	}
	if !reflect.DeepEqual(upserter.tokens, tokens) {
		t.Fatalf("upsert tokens = %+v, want %+v", upserter.tokens, tokens)
	}
	if keys := h.mr.Keys(); len(keys) != 0 {
		t.Fatalf("session keys %v were written; the callback must not mint a session", keys)
	}
}

func TestServiceCompleteCallbackRejectsReusedState(t *testing.T) {
	exchanger := &fakeExchanger{tokens: tiktokclient.TokenSet{Scope: "user.info.basic"}}
	h := newHarness(t, exchanger, &fakeUpserter{id: "acct-1"})
	ctx := context.Background()
	sess := browserSession(t, "op-1", "browser-1")
	state := issueState(t, h.svc, sess.ID)

	if err := h.svc.CompleteCallback(ctx, "op-1", sess.ID, "auth-code-1", state); err != nil {
		t.Fatalf("first CompleteCallback: %v", err)
	}
	err := h.svc.CompleteCallback(ctx, "op-1", sess.ID, "auth-code-1", state)
	wantCode(t, err, apierror.CodeUnauthorized)
}

func TestServiceCompleteCallbackRejectsUnknownState(t *testing.T) {
	exchanger := &fakeExchanger{tokens: tiktokclient.TokenSet{Scope: "user.info.basic"}}
	h := newHarness(t, exchanger, &fakeUpserter{id: "acct-1"})
	sess := browserSession(t, "op-1", "browser-1")

	err := h.svc.CompleteCallback(context.Background(), "op-1", sess.ID, "auth-code-1", "never-issued")
	wantCode(t, err, apierror.CodeUnauthorized)
	if exchanger.calls != 0 {
		t.Fatalf("exchange ran %d times on an unknown state", exchanger.calls)
	}
}

// TestServiceCompleteCallbackRejectsForeignSession proves a state issued to
// one browser session cannot be spent by another session of the same operator.
// In this single operator model every session shares one operator id, so only
// the per browser binding stops an attacker from replaying their own code and
// state pair against the operator's cookie.
func TestServiceCompleteCallbackRejectsForeignSession(t *testing.T) {
	exchanger := &fakeExchanger{tokens: tiktokclient.TokenSet{Scope: "user.info.basic"}}
	upserter := &fakeUpserter{id: "acct-1"}
	h := newHarness(t, exchanger, upserter)
	attacker := browserSession(t, testOperatorID, "browser-attacker")
	victim := browserSession(t, testOperatorID, "browser-victim")
	if attacker.OperatorID != victim.OperatorID {
		t.Fatalf("fixture sessions have different operators %q and %q", attacker.OperatorID, victim.OperatorID)
	}
	state := issueState(t, h.svc, attacker.ID)

	err := h.svc.CompleteCallback(context.Background(), victim.OperatorID, victim.ID, "auth-code-1", state)
	wantCode(t, err, apierror.CodeUnauthorized)
	if exchanger.calls != 0 {
		t.Fatalf("exchange ran %d times on a foreign session", exchanger.calls)
	}
	if upserter.calls != 0 {
		t.Fatalf("upsert ran %d times on a foreign session", upserter.calls)
	}
}

func TestServiceCompleteCallbackKeepsPartialScopes(t *testing.T) {
	exchanger := &fakeExchanger{tokens: tiktokclient.TokenSet{Scope: "user.info.basic"}}
	upserter := &fakeUpserter{id: "acct-1"}
	h := newHarness(t, exchanger, upserter)
	sess := browserSession(t, "op-1", "browser-1")
	state := issueState(t, h.svc, sess.ID)

	if err := h.svc.CompleteCallback(context.Background(), "op-1", sess.ID, "auth-code-1", state); err != nil {
		t.Fatalf("CompleteCallback: %v", err)
	}
	want := []string{"user.info.basic"}
	if !reflect.DeepEqual(upserter.granted, want) {
		t.Fatalf("upsert granted = %v, want only the granted set %v", upserter.granted, want)
	}
}

func TestServiceCompleteCallbackExchangeFailure(t *testing.T) {
	exchanger := &fakeExchanger{err: apierror.New(apierror.CodeTikTokUnavailable, 0, "TikTok is down.")}
	upserter := &fakeUpserter{id: "acct-1"}
	h := newHarness(t, exchanger, upserter)
	sess := browserSession(t, "op-1", "browser-1")
	state := issueState(t, h.svc, sess.ID)

	err := h.svc.CompleteCallback(context.Background(), "op-1", sess.ID, "auth-code-1", state)
	wantCode(t, err, apierror.CodeTikTokUnavailable)
	if upserter.calls != 0 {
		t.Fatalf("upsert ran %d times on exchange failure", upserter.calls)
	}
	if keys := h.mr.Keys(); len(keys) != 0 {
		t.Fatalf("session keys %v were written on exchange failure", keys)
	}
}
