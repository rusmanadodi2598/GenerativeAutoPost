// Package webhook proves the intake answers typed dispatch errors correctly.
//
// @file      internal/webhook/handler_errors_test.go
// @for       Handler tests for malformed content, apply failure, and log hygiene.
// @uses      bytes, errors, net/http, strings, testing, time
// @reason    A flattened 500 keeps TikTok retrying a delivery that can never succeed.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-14
package webhook

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestHandler_GivenMalformedContent_ThenBadGateway proves a permanently
// malformed payload keeps its typed 502 instead of being flattened to a 500,
// which would have TikTok retry a delivery that can never succeed.
func TestHandler_GivenMalformedContent_ThenBadGateway(t *testing.T) {
	const secret = "tiktok-webhook-secret"
	body := []byte(`{"client_key":"awx123","event":"post.publish.complete","create_time":1728664800,"user_openid":"openid-9","content":"not-json"}`)
	h, _, statuses, _ := newIntake(t, secret)
	header, _ := signedDelivery(secret, body)

	rec := postDelivery(h, header, body)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"BAD_GATEWAY"`) {
		t.Fatalf("expected a BAD_GATEWAY body code, got %s", rec.Body.String())
	}
	if len(statuses.calls) != 0 {
		t.Fatalf("expected no updater calls, got %v", statuses.calls)
	}
}

// TestHandler_GivenApplyFailure_ThenStoreErrorAndNothingStamped proves a failed
// transition answers 500 and leaves no outbox row behind.
func TestHandler_GivenApplyFailure_ThenStoreErrorAndNothingStamped(t *testing.T) {
	const secret = "tiktok-webhook-secret"
	body := completeBody()
	h, store, statuses, _ := newIntake(t, secret)
	statuses.applyErr = errors.New("publish row missing")
	header, _ := signedDelivery(secret, body)

	rec := postDelivery(h, header, body)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if len(store.outbox) != 0 {
		t.Fatalf("expected no outbox row, got %d", len(store.outbox))
	}
	if len(store.deleted) != 1 {
		t.Fatalf("expected the stored row removed, got %v", store.deleted)
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
