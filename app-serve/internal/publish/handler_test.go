// Package publish exercises the publish HTTP handlers over in memory fakes.
//
// @file      internal/publish/handler_test.go
// @for       HTTP tests for the init, chunk, lookup, list, and reconcile routes.
// @uses      encoding/json, net/http, httptest, strings, testing, gin, internal/apierror
// @reason    Covers the publish HTTP contract end to end over the router.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package publish

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// TestInitRoutesAnswerAccepted asserts each init route returns 202 with a
// publish id and without an upload URL.
func TestInitRoutesAnswerAccepted(t *testing.T) {
	h := newHandlerHarness(t)
	for _, tc := range []struct{ name, path, body string }{
		{"videos", "/publish/videos", videoBody},
		{"photos", "/publish/photos", photoBody},
		{"drafts", "/publish/drafts", draftBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := h.do(t, http.MethodPost, tc.path, tc.body,
				map[string]string{idempotencyHeader: "key-" + tc.name})
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
			}
			var envelope struct {
				Data InitResult `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decoding envelope: %v", err)
			}
			if envelope.Data.PublishID == "" || envelope.Data.ID == "" {
				t.Fatalf("expected attempt identity, got %+v", envelope.Data)
			}
			if strings.Contains(rec.Body.String(), "upload_url") {
				t.Fatalf("response leaked the upload url: %s", rec.Body)
			}
		})
	}
}

// TestInitThreadsIdempotencyKey asserts the header reaches the store claimer.
func TestInitThreadsIdempotencyKey(t *testing.T) {
	h := newHandlerHarness(t)
	rec := h.do(t, http.MethodPost, "/publish/videos", videoBody,
		map[string]string{idempotencyHeader: "key-1"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if _, ok := h.idempot.hashes["key-1"]; !ok {
		t.Fatalf("expected the idempotency key to be claimed, claimed = %v", h.idempot.hashes)
	}
}

// TestInitRejectsInvalidBody asserts a missing account id and a mistyped
// field both answer 422 with the validation code.
func TestInitRejectsInvalidBody(t *testing.T) {
	h := newHandlerHarness(t)
	missing := `{"media_id":"` + testMediaID + `","source":"FILE_UPLOAD"}`
	mistyped := `{"account_id":42,"media_id":"` + testMediaID +
		`","source":"FILE_UPLOAD","post_info":{"title":"x","privacy_level":"PUBLIC"}}`
	for name, body := range map[string]string{"missing account": missing, "mistyped field": mistyped} {
		t.Run(name, func(t *testing.T) {
			rec := h.do(t, http.MethodPost, "/publish/videos", body, nil)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
			}
			if code := bodyCode(t, rec); code != string(apierror.CodeValidationFailed) {
				t.Fatalf("code = %s, body = %s", code, rec.Body)
			}
		})
	}
}

// TestRoutesRejectMissingSession asserts every route answers 401 without a
// session, which covers the per route guard.
func TestRoutesRejectMissingSession(t *testing.T) {
	h := newHandlerHarnessWithSession(t, func(*gin.Context) (string, bool) { return "", false })
	attemptID := "6f1b7a4e-1c2d-4f3a-9b8c-0d1e2f3a4b5c"
	for _, tc := range []struct{ name, method, path string }{
		{"videos", http.MethodPost, "/publish/videos"},
		{"photos", http.MethodPost, "/publish/photos"},
		{"drafts", http.MethodPost, "/publish/drafts"},
		{"chunks", http.MethodPut, "/publish/" + attemptID + "/chunks"},
		{"show", http.MethodGet, "/publish/" + attemptID},
		{"list", http.MethodGet, "/publish"},
		{"reconcile", http.MethodPost, "/publish/" + attemptID + "/reconcile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := h.do(t, tc.method, tc.path, videoBody, nil)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
			}
			if code := bodyCode(t, rec); code != string(apierror.CodeUnauthorized) {
				t.Fatalf("code = %s, body = %s", code, rec.Body)
			}
		})
	}
}

// bodyCode returns the wire error code of a problem response.
func bodyCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding problem body: %v", err)
	}
	return body.Code
}

const videoBody = `{"account_id":"` + testAccountID + `","media_id":"` + testMediaID +
	`","source":"FILE_UPLOAD","post_info":{"title":"hello","privacy_level":"PUBLIC"}}`

const photoBody = `{"account_id":"` + testAccountID + `","media_id":"` + testMediaID +
	`","post_mode":"MEDIA_UPLOAD","post_info":{"title":"hello"},"photo_urls":["https://media.example/a.jpg"]}`

const draftBody = `{"account_id":"` + testAccountID + `","media_id":"` + testMediaID +
	`","source":"FILE_UPLOAD"}`
