// Package googleoidc_test pins the Google authorize and token exchange behavior.
//
// @file      internal/platform/googleoidc/exchange_test.go
// @for       Accept and error cases for the authorize URL and the code exchange.
// @uses      testing, net/http, net/http/httptest, net/url, strings
// @reason    Proves the consent link carries state and nonce and the code buys an id token.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-17
package googleoidc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAuthorizeURL_CarriesStateAndNonce(t *testing.T) {
	client := New(Config{
		ClientID:    "client-1",
		RedirectURI: "https://app.example/auth/google/callback",
		AuthURL:     "https://accounts.example/o/oauth2/v2/auth",
	})

	parsed, err := url.Parse(client.AuthorizeURL("state-1", "nonce-1"))
	if err != nil {
		t.Fatalf("AuthorizeURL() returned an unparsable url: %v", err)
	}
	if parsed.Host != "accounts.example" || parsed.Path != "/o/oauth2/v2/auth" {
		t.Errorf("AuthorizeURL() endpoint = %s%s, want the configured auth endpoint", parsed.Host, parsed.Path)
	}
	want := map[string]string{
		"client_id":     "client-1",
		"redirect_uri":  "https://app.example/auth/google/callback",
		"response_type": "code",
		"scope":         "openid email",
		"state":         "state-1",
		"nonce":         "nonce-1",
	}
	query := parsed.Query()
	for key, value := range want {
		if query.Get(key) != value {
			t.Errorf("AuthorizeURL() query %q = %q, want %q", key, query.Get(key), value)
		}
	}
}

func TestExchangeCode_PostsGrantAndReturnsIDToken(t *testing.T) {
	forms := make(chan url.Values, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		forms <- r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id_token":"raw-id-token","access_token":"ignored"}`))
	}))
	t.Cleanup(srv.Close)

	client := New(Config{
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		RedirectURI:  "https://app.example/auth/google/callback",
		TokenURL:     srv.URL,
	})

	token, err := client.ExchangeCode(context.Background(), "the-code")
	if err != nil {
		t.Fatalf("ExchangeCode() error = %v", err)
	}
	if token != "raw-id-token" {
		t.Errorf("ExchangeCode() = %q, want %q", token, "raw-id-token")
	}
	wantForm := map[string]string{
		"code":          "the-code",
		"client_id":     "client-1",
		"client_secret": "secret-1",
		"redirect_uri":  "https://app.example/auth/google/callback",
		"grant_type":    "authorization_code",
	}
	form := <-forms
	for key, value := range wantForm {
		if form.Get(key) != value {
			t.Errorf("ExchangeCode() form %q = %q, want %q", key, form.Get(key), value)
		}
	}
}

func TestExchangeCode_ErrorsOnProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	t.Cleanup(srv.Close)

	client := New(Config{ClientID: "client-1", ClientSecret: "secret-1", TokenURL: srv.URL})

	_, err := client.ExchangeCode(context.Background(), "the-code")
	if err == nil {
		t.Fatal("ExchangeCode() error = nil, want an error for a provider error response")
	}
	if strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("ExchangeCode() error = %q, must not echo the response body", err)
	}
}

func TestExchangeCode_ErrorsOnMissingIDToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"ignored"}`))
	}))
	t.Cleanup(srv.Close)

	client := New(Config{ClientID: "client-1", ClientSecret: "secret-1", TokenURL: srv.URL})

	if _, err := client.ExchangeCode(context.Background(), "the-code"); err == nil {
		t.Fatal("ExchangeCode() error = nil, want an error when the response carries no id token")
	}
}
