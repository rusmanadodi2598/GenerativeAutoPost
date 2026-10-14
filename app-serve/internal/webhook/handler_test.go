// Package webhook proves the intake handler authenticates and dispatches.
//
// @file      internal/webhook/handler_test.go
// @for       Handler tests over fakes for store and status updater.
// @uses      bytes, net/http, strings, testing, time
// @reason    Intake rejects must stay 401 and accepted rows must persist.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package webhook

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestHandler_GivenValidSignature_ThenAcceptedAndStored proves the happy path
// writes one event row and calls the status updater once.
func TestHandler_GivenValidSignature_ThenAcceptedAndStored(t *testing.T) {
	const secret = "tiktok-webhook-secret"
	body := completeBody()
	store := &fakeStore{stored: true}
	statuses := &fakeStatuses{}
	h, _ := intakeSetup(t, secret, store, statuses)
	stamp := time.Now().UTC().Unix()

	rec := postDelivery(h, signHandler(secret, stamp, body), body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("expected one stored event, got %d", len(store.inserted))
	}
	if len(statuses.calls) != 1 || !strings.HasPrefix(statuses.calls[0], "complete:pub-1:post-7") {
		t.Fatalf("expected one complete call, got %v", statuses.calls)
	}
	if len(store.outbox) != 1 {
		t.Fatalf("expected one outbox row, got %d", len(store.outbox))
	}
}

// TestHandler_GivenBadSignatures_ThenUnauthorizedAndNothingStored covers the
// invalid, stale, oversized, and mutated deliveries in one table.
func TestHandler_GivenBadSignatures_ThenUnauthorizedAndNothingStored(t *testing.T) {
	const secret = "tiktok-webhook-secret"
	body := completeBody()
	stamp := time.Now().UTC().Unix()
	good := signHandler(secret, stamp, body)

	mutated := []byte(`{"event":"post.publish.complete","client_key":"awx123","create_time":1728664800,"user_openid":"openid-9","content":"{\"publish_id\":\"pub-1\",\"post_id\":\"post-7\"}"}`)
	reserialized := []byte(`{"client_key":"awx123", "event":"post.publish.complete","create_time":1728664800,"user_openid":"openid-9","content":"{\"publish_id\":\"pub-1\",\"post_id\":\"post-7\"}"}`)
	stale := stamp - int64((5*time.Minute + time.Second).Seconds())

	huge := []byte(`{"client_key":"awx123","event":"post.publish.complete","padding":"`)
	huge = append(huge, bytes.Repeat([]byte("x"), MaxBodyBytes+8)...)
	huge = append(huge, []byte(`"}`)...)

	cases := []struct {
		name   string
		header string
		body   []byte
	}{
		{name: "invalid signature", header: signHandler("other-secret", stamp, body), body: body},
		{name: "missing header", header: "", body: body},
		{name: "stale timestamp", header: signHandler(secret, stale, body), body: body},
		{name: "reordered keys", header: good, body: mutated},
		{name: "reserialized body", header: good, body: reserialized},
		{name: "oversized body", header: good, body: huge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, store, statuses, _ := newIntake(t, secret)
			rec := postDelivery(h, tc.header, tc.body)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", rec.Code)
			}
			if len(store.inserted) != 0 {
				t.Fatalf("expected no stored event, got %d", len(store.inserted))
			}
			if len(statuses.calls) != 0 {
				t.Fatalf("expected no updater calls, got %v", statuses.calls)
			}
		})
	}
}

// TestHandler_GivenMalformedEnvelope_ThenUnauthorized keeps parsing failures
// indistinguishable from signature failures on the wire.
func TestHandler_GivenMalformedEnvelope_ThenUnauthorized(t *testing.T) {
	const secret = "tiktok-webhook-secret"
	body := []byte(`{not-json`)
	store := &fakeStore{stored: true}
	statuses := &fakeStatuses{}
	h, _ := intakeSetup(t, secret, store, statuses)
	stamp := time.Now().UTC().Unix()

	rec := postDelivery(h, signHandler(secret, stamp, body), body)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if len(store.inserted) != 0 {
		t.Fatalf("expected no stored event, got %d", len(store.inserted))
	}
}

// TestHandler_GivenDuplicate_ThenAcceptedOnceUpdaterCalledOnce proves the
// second delivery answers 200 with no additional state change.
func TestHandler_GivenDuplicate_ThenAcceptedOnceUpdaterCalledOnce(t *testing.T) {
	const secret = "tiktok-webhook-secret"
	body := completeBody()
	store := &fakeStore{stored: true}
	statuses := &fakeStatuses{}
	h, _ := intakeSetup(t, secret, store, statuses)
	header, _ := signedDelivery(secret, body)

	first := postDelivery(h, header, body)
	store.stored = false
	second := postDelivery(h, header, body)

	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("expected 200 twice, got %d and %d", first.Code, second.Code)
	}
	if len(statuses.calls) != 1 {
		t.Fatalf("expected updater called once, got %v", statuses.calls)
	}
}

// TestHandler_GivenUnknownEvent_ThenAcceptedWithoutUpdaterCall proves unknown
// types acknowledge 200 and never touch publish state.
func TestHandler_GivenUnknownEvent_ThenAcceptedWithoutUpdaterCall(t *testing.T) {
	const secret = "tiktok-webhook-secret"
	body := []byte(`{"client_key":"awx123","event":"video.upload.failed","create_time":1728664800,"user_openid":"openid-9","content":"{}"}`)
	header, _ := signedDelivery(secret, body)
	h, _, statuses, _ := newIntake(t, secret)

	rec := postDelivery(h, header, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if len(statuses.calls) != 0 {
		t.Fatalf("expected no updater calls, got %v", statuses.calls)
	}
}

// TestHandler_GivenStoreFailure_ThenInternalError proves a persistence failure
// answers 500 so TikTok retries instead of losing the delivery.
func TestHandler_GivenStoreFailure_ThenInternalError(t *testing.T) {
	const secret = "tiktok-webhook-secret"
	body := completeBody()
	backing := &fakeStore{}
	statuses := &fakeStatuses{}
	h, _ := intakeSetup(t, secret, backing, statuses)
	h.dispatch.store = &failingStore{fakeStore: *backing}
	header, _ := signedDelivery(secret, body)

	rec := postDelivery(h, header, body)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if len(statuses.calls) != 0 {
		t.Fatalf("expected no updater calls, got %v", statuses.calls)
	}
}

// TestHandler_GivenReject_ThenLogsCarryNoBody captures the slog output for each
// reject path and asserts the raw body never appears in it.
func TestHandler_GivenReject_ThenLogsCarryNoBody(t *testing.T) {
	const secret = "tiktok-webhook-secret"
	body := completeBody()
	marker := "pub-1-unique-marker"
	marked := bytes.Replace(body, []byte("pub-1"), []byte(marker), 1)
	broken := []byte(`{not-json`)
	badHeader, _ := signedDelivery(secret, broken)

	cases := []struct {
		name   string
		header string
		body   []byte
	}{
		{name: "bad signature", header: signHandler("other-secret", time.Now().UTC().Unix(), marked), body: marked},
		{name: "bad envelope", header: badHeader, body: broken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _, logs := newIntake(t, secret)
			rec := postDelivery(h, tc.header, tc.body)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", rec.Code)
			}
			if strings.Contains(logs.String(), marker) {
				t.Fatalf("failure log contains the body: %q", logs.String())
			}
			if strings.Contains(logs.String(), "not-json") {
				t.Fatalf("failure log contains the body: %q", logs.String())
			}
			if !strings.Contains(logs.String(), "203.0.113.7") {
				t.Fatalf("failure log is missing the source IP: %q", logs.String())
			}
		})
	}
}
