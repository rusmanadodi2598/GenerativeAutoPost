// Package auth_test drives the CSRF and success envelope route cases.
//
// @file      internal/auth/handler_csrf_test.go
// @for       The CSRF 403 cases plus the refresh and revoke reply shapes.
// @uses      testing, net/http/httptest, encoding/json, strings, time, internal/apierror
// @reason    The guard must fail closed at the HTTP layer, not just unit level.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// csrfCase names a protected route the CSRF layer must guard at the HTTP
// layer. The body is bound only after the CSRF layer passes, so a rejected
// request never reaches the handler.
type csrfCase struct {
	name string
	path string
}

func csrfCases() []csrfCase {
	return []csrfCase{
		{"logout", "/auth/logout"},
		{"refresh", "/auth/tiktok/refresh"},
	}
}

// csrfRequest builds a request that carries a valid session cookie but the
// supplied CSRF header value, so only the CSRF layer decides the outcome.
func (h *httpHarness) csrfRequest(t *testing.T, path, headerToken string) *http.Request {
	t.Helper()
	sess := h.createSession(t)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"account_id":"acct-1"}`))
	req.Header.Set("Content-Type", "application/json")
	if headerToken != "" {
		req.Header.Set("X-CSRF-Token", headerToken)
	}
	req.AddCookie(&http.Cookie{Name: constCookie, Value: sess.ID})
	return req
}

func TestRouterCSRFMissingHeaderIsForbidden(t *testing.T) {
	for _, tc := range csrfCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := newHTTPHarness(t)
			rec := h.serve(h.csrfRequest(t, tc.path, ""))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
			if got := problemCode(t, rec); got != string(apierror.CodeForbidden) {
				t.Fatalf("code = %q, want %q", got, apierror.CodeForbidden)
			}
		})
	}
}

func TestRouterCSRFWrongTokenIsForbidden(t *testing.T) {
	for _, tc := range csrfCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := newHTTPHarness(t)
			rec := h.serve(h.csrfRequest(t, tc.path, "csrf-wrong"))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
			if got := problemCode(t, rec); got != string(apierror.CodeForbidden) {
				t.Fatalf("code = %q, want %q", got, apierror.CodeForbidden)
			}
		})
	}
}

// successBody is the typed view of the success envelope the route tests read.
type successBody struct {
	Data struct {
		AccountID string `json:"account_id"`
	} `json:"data"`
	Meta struct {
		RequestID  string `json:"request_id"`
		ServerTime string `json:"server_time"`
	} `json:"meta"`
}

func TestRouterRefreshReturnsEnvelope(t *testing.T) {
	h := newHTTPHarness(t)
	rec := h.serve(h.authed(t, http.MethodPost, "/auth/tiktok/refresh", `{"account_id":"acct-1"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body successBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding envelope %q: %v", rec.Body.String(), err)
	}
	if body.Data.AccountID != "acct-1" {
		t.Fatalf("data.account_id = %q, want acct-1", body.Data.AccountID)
	}
	if body.Meta.RequestID == "" {
		t.Fatal("meta.request_id is empty")
	}
	if _, err := time.Parse(time.RFC3339, body.Meta.ServerTime); err != nil {
		t.Fatalf("meta.server_time %q is not RFC 3339: %v", body.Meta.ServerTime, err)
	}
}

func TestRouterRevokeReturnsNoContent(t *testing.T) {
	h := newHTTPHarness(t)
	rec := h.serve(h.authed(t, http.MethodPost, "/auth/tiktok/revoke", `{"account_id":"acct-1"}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", rec.Body.String())
	}
}
