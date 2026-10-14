// Package webhook table drives the 002 section 5.2 vectors and Review Focus cases.
//
// @file      internal/webhook/verify_test.go
// @for       Known-good vector, negative cases, and byte-exactness proofs.
// @uses      crypto/hmac, crypto/sha256, encoding/hex, strconv, testing, time
// @reason    Locks webhook authentication to the TikTok contract.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// sign builds the expected "t=...,s=..." header for a body.
func sign(t *testing.T, secret string, stamp int64, body []byte) string {
	t.Helper()
	payload := append([]byte(strconv.FormatInt(stamp, 10)+"."), body...)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "t=" + strconv.FormatInt(stamp, 10) + ",s=" + hex.EncodeToString(mac.Sum(nil))
}

// TestVerify_GivenVectors_ThenVerdictMatchesContract is the 002
// section 5.2 vector suite plus the Review Focus mutation cases.
func TestVerify_GivenVectors_ThenVerdictMatchesContract(t *testing.T) {
	const secret = "tiktok-webhook-secret"
	body := []byte(`{"client_key":"awx123","event":"post.publish.complete","create_time":1728664800,"user_openid":"openid-9","content":"{\"publish_id\":\"pub-1\"}"}`)
	stamp := int64(1728664800)
	now := time.Unix(stamp, 0)
	tolerance := 5 * time.Minute

	cases := []struct {
		name    string
		header  string
		body    []byte
		now     time.Time
		wantErr bool
	}{
		{name: "known good vector", header: sign(t, secret, stamp, body), body: body, now: now},
		{name: "missing t", header: "s=abc123", body: body, now: now, wantErr: true},
		{name: "missing s", header: "t=1728664800", body: body, now: now, wantErr: true},
		{name: "malformed header", header: "t1728664800:s", body: body, now: now, wantErr: true},
		{name: "wrong secret", header: sign(t, "other-secret", stamp, body), body: body, now: now, wantErr: true},
		{name: "stale timestamp", header: sign(t, secret, stamp, body), body: body, now: now.Add(tolerance + time.Second), wantErr: true},
		{name: "non hex s", header: "t=1728664800,s=zzzz", body: body, now: now, wantErr: true},
		{name: "trailing newline", header: sign(t, secret, stamp, body), body: append(body, '\n'), now: now, wantErr: true},
		{
			name:    "reordered keys",
			header:  sign(t, secret, stamp, body),
			body:    []byte(`{"event":"post.publish.complete","client_key":"awx123","create_time":1728664800,"user_openid":"openid-9","content":"{\"publish_id\":\"pub-1\"}"}`),
			now:     now,
			wantErr: true,
		},
		{
			name:    "reserialized body",
			header:  sign(t, secret, stamp, body),
			body:    []byte(`{"client_key":"awx123", "event":"post.publish.complete","create_time":1728664800,"user_openid":"openid-9","content":"{\"publish_id\":\"pub-1\"}"}`),
			now:     now,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Verify(secret, tc.header, tc.body, tc.now, tolerance)
			if tc.wantErr && err == nil {
				t.Fatal("expected signature failure, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected verify success, got %v", err)
			}
		})
	}
}

// TestDecodedContent_GivenEnvelope_ThenTypedStructOrBadGateway locks the
// two-step content decode: the content string decodes into the typed
// struct, malformed JSON maps to CodeBadGateway.
func TestDecodedContent_GivenEnvelope_ThenTypedStructOrBadGateway(t *testing.T) {
	env := Envelope{
		ClientKey:  "awx123",
		Event:      EventComplete,
		CreateTime: 1728664800,
		UserOpenID: "openid-9",
		Content:    json.RawMessage(`"{\"publish_id\":\"pub-1\",\"post_id\":\"post-7\"}"`),
	}
	var post PostPublishContent
	if err := decodedContent(env, &post); err != nil {
		t.Fatalf("expected decode success, got %v", err)
	}
	if post.PublishID != "pub-1" || post.PostID != "post-7" {
		t.Fatalf("unexpected content %+v", post)
	}

	auth := Envelope{Event: EventAuthRemoved, Content: json.RawMessage(`"{\"reason\":4}"`)}
	var removed AuthorizationRemovedContent
	if err := decodedContent(auth, &removed); err != nil {
		t.Fatalf("expected decode success, got %v", err)
	}
	if removed.Reason != 4 {
		t.Fatalf("unexpected reason %+v", removed)
	}

	bad := Envelope{Event: EventComplete, Content: json.RawMessage(`"{broken"`)}
	err := decodedContent(bad, &post)
	if err == nil {
		t.Fatal("expected decode failure, got nil")
	}
	apiErr, ok := err.(*apierror.Error)
	if !ok || apiErr.Code != apierror.CodeBadGateway {
		t.Fatalf("expected CodeBadGateway, got %v", err)
	}
}

// TestVerify_GivenToleranceEdge_ThenBoundaryHolds verifies the exact
// edge: inside tolerance passes, one second past fails.
func TestVerify_GivenToleranceEdge_ThenBoundaryHolds(t *testing.T) {
	const secret = "tiktok-webhook-secret"
	body := []byte(`{"event":"post.publish.complete"}`)
	stamp := int64(1728664800)
	header := sign(t, secret, stamp, body)
	tolerance := 5 * time.Minute
	if err := Verify(secret, header, body, time.Unix(stamp, 0).Add(tolerance), tolerance); err != nil {
		t.Fatalf("expected success at edge, got %v", err)
	}
	if err := Verify(secret, header, body, time.Unix(stamp, 0).Add(tolerance+time.Second), tolerance); err == nil {
		t.Fatal("expected failure one second past edge, got nil")
	}
}
