// Package account covers the account HTTP surface.
//
// @file      internal/account/handler_test.go
// @for       Route tests for the account list, show, and delete endpoints.
// @uses      net/http/httptest, encoding/json, testing, gin
// @reason    Pins the envelope, the session guard, the CSRF guard, and the absence of token fields.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-16
package account

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// routeHarness wires the account routes over the fake store.
type routeHarness struct {
	router *gin.Engine
	svc    *Service
	store  *fakeStore
}

// csrfGate answers 403 unless the request carries the test CSRF header.
func csrfGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("X-CSRF-Token") != "csrf-ok" {
			c.AbortWithStatus(403)
			return
		}
		c.Next()
	}
}

// newRouteHarness builds the router with one signed in operator.
func newRouteHarness(t *testing.T) *routeHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc, store := newTestService(t)
	engine := gin.New()
	RegisterRoutes(engine, NewHandler(svc, func(*gin.Context) (string, bool) { return "operator-a", true }, csrfGate()))
	return &routeHarness{router: engine, svc: svc, store: store}
}

// do runs one request through the account router.
func (h *routeHarness) do(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader("")))
	return rec
}

// decodedItem pulls one list member out of a recorded response.
func decodedItem(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	if len(envelope.Data) != 1 {
		t.Fatalf("want one item, got %s", rec.Body.String())
	}
	return envelope.Data[0]
}

// TestListAccountsOmitsTokenMaterial proves the listing never puts credential
// bytes on the wire, since the response is cached by browsers and proxies.
func TestListAccountsOmitsTokenMaterial(t *testing.T) {
	h := newRouteHarness(t)
	id := seedOwned(t, h.svc, "operator-a", "open-a")
	seedOwned(t, h.svc, "operator-b", "open-b")

	rec := h.do(t, "GET", "/accounts")
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	item := decodedItem(t, rec)
	if item["id"] != id {
		t.Fatalf("want account %s, got %v", id, item["id"])
	}
	for _, forbidden := range []string{"access_token", "refresh_token", "access_token_enc", "token_key_id"} {
		if _, present := item[forbidden]; present {
			t.Errorf("response carries %s", forbidden)
		}
	}
	if strings.Contains(rec.Body.String(), "plaintext-access") {
		t.Error("response carries access token material")
	}
}

// TestShowAccountAnswersNotFoundForAForeignAccount proves the show path hides
// another operator's account instead of confirming the id exists.
func TestShowAccountAnswersNotFoundForAForeignAccount(t *testing.T) {
	h := newRouteHarness(t)
	foreign := seedOwned(t, h.svc, "operator-b", "open-b")

	rec := h.do(t, "GET", "/accounts/"+foreign)
	if rec.Code != 404 {
		t.Fatalf("want 404 for a foreign account, got %d: %s", rec.Code, rec.Body.String())
	}
	var problem struct {
		Code string `json:"code"`
		Data any    `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	if problem.Code != "NOT_FOUND" {
		t.Fatalf("want NOT_FOUND, got %q", problem.Code)
	}
	if problem.Data != nil {
		t.Fatal("the not found body carries account data")
	}
}

// TestDeleteAccountRequiresTheCSRFHeader proves the destructive route is not
// reachable from a cross site form post.
func TestDeleteAccountRequiresTheCSRFHeader(t *testing.T) {
	h := newRouteHarness(t)
	id := seedOwned(t, h.svc, "operator-a", "open-a")

	rec := h.do(t, "DELETE", "/accounts/"+id)
	if rec.Code != 403 {
		t.Fatalf("want 403 without the CSRF header, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestDeleteAccountClearsTheAccount proves the delete clears credentials and
// answers the 204 the console waits for.
func TestDeleteAccountClearsTheAccount(t *testing.T) {
	h := newRouteHarness(t)
	id := seedOwned(t, h.svc, "operator-a", "open-a")

	req := httptest.NewRequest("DELETE", "/accounts/"+id, nil)
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(h.store.clearCalls) != 1 {
		t.Fatalf("want one credential clear, got %v", h.store.clearCalls)
	}
}

// TestListAccountsWithoutASessionAnswers401 proves the guard runs before any
// operator data is read.
func TestListAccountsWithoutASessionAnswers401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc, _ := newTestService(t)
	engine := gin.New()
	RegisterRoutes(engine, NewHandler(svc, func(*gin.Context) (string, bool) { return "", false }, csrfGate()))

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest("GET", "/accounts", nil))
	if rec.Code != 401 {
		t.Fatalf("want 401 without a session, got %d: %s", rec.Code, rec.Body.String())
	}
}
