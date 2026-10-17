// Package googleoidc_test pins the Google id token verification behavior.
//
// @file      internal/platform/googleoidc/oidc_test.go
// @for       Accept and reject cases for the Google id token verifier.
// @uses      testing, crypto/rsa, crypto/sha256, encoding/base64, net/http/httptest
// @reason    Proves a forged, stale, or mis-scoped token never yields an identity.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-17
package googleoidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// testKey is a signing key pair with the key id the token header names.
type testKey struct {
	kid  string
	priv *rsa.PrivateKey
}

func newTestKey(t *testing.T, kid string) testKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating rsa key: %v", err)
	}
	return testKey{kid: kid, priv: priv}
}

func (k testKey) jwksEntry() map[string]any {
	return map[string]any{
		"kty": "RSA",
		"kid": k.kid,
		"alg": "RS256",
		"use": "sig",
		"n":   base64.RawURLEncoding.EncodeToString(k.priv.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.priv.E)).Bytes()),
	}
}

func validClaims(clientID, nonce string) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss":            "https://accounts.google.com",
		"aud":            clientID,
		"sub":            "operator-123",
		"email":          "operator@example.com",
		"email_verified": true,
		"nonce":          nonce,
		"exp":            now.Add(time.Hour).Unix(),
		"iat":            now.Add(-time.Minute).Unix(),
	}
}

func signToken(t *testing.T, key testKey, claims map[string]any) string {
	t.Helper()
	headerJSON, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": key.kid, "typ": "JWT"})
	payloadJSON, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(payloadJSON)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key.priv, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// newJWKSServer serves key sets in order, holding the final one for later calls.
func newJWKSServer(t *testing.T, sets ...[]map[string]any) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		idx := calls
		if idx >= len(sets) {
			idx = len(sets) - 1
		}
		calls++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": sets[idx]})
	}))
	t.Cleanup(srv.Close)
	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
}

// newTestClient stands up a JWKS server holding one key and points a client at it.
func newTestClient(t *testing.T, key testKey) *Client {
	t.Helper()
	srv, _ := newJWKSServer(t, []map[string]any{key.jwksEntry()})
	return New(Config{ClientID: "client-1", JWKSURL: srv.URL})
}

// mustReject fails the test when a token the client must refuse is accepted.
func mustReject(t *testing.T, client *Client, token, nonce string) {
	t.Helper()
	if _, err := client.VerifyIDToken(context.Background(), token, nonce); err == nil {
		t.Fatal("VerifyIDToken() error = nil, want a rejection")
	}
}

func TestVerifyIDToken_AcceptsAValidToken(t *testing.T) {
	key := newTestKey(t, "key-1")
	client := newTestClient(t, key)
	token := signToken(t, key, validClaims("client-1", "nonce-1"))

	claims, err := client.VerifyIDToken(context.Background(), token, "nonce-1")
	if err != nil {
		t.Fatalf("VerifyIDToken() error = %v", err)
	}
	if claims.Email != "operator@example.com" {
		t.Errorf("Email = %q, want %q", claims.Email, "operator@example.com")
	}
	if claims.Subject != "operator-123" {
		t.Errorf("Subject = %q, want %q", claims.Subject, "operator-123")
	}
	if !claims.EmailVerified {
		t.Error("EmailVerified = false, want true")
	}
}

func TestVerifyIDToken_RejectsBadSignature(t *testing.T) {
	served := newTestKey(t, "key-1")
	forger := newTestKey(t, "key-1")
	client := newTestClient(t, served)
	token := signToken(t, forger, validClaims("client-1", "nonce-1"))

	mustReject(t, client, token, "nonce-1")
}

func TestVerifyIDToken_RejectsWrongAudience(t *testing.T) {
	key := newTestKey(t, "key-1")
	client := newTestClient(t, key)
	token := signToken(t, key, validClaims("another-client", "nonce-1"))

	mustReject(t, client, token, "nonce-1")
}

func TestVerifyIDToken_RejectsWrongIssuer(t *testing.T) {
	key := newTestKey(t, "key-1")
	client := newTestClient(t, key)
	claims := validClaims("client-1", "nonce-1")
	claims["iss"] = "https://evil.example"
	token := signToken(t, key, claims)

	mustReject(t, client, token, "nonce-1")
}

func TestVerifyIDToken_RejectsExpired(t *testing.T) {
	key := newTestKey(t, "key-1")
	client := newTestClient(t, key)
	claims := validClaims("client-1", "nonce-1")
	claims["exp"] = time.Now().Add(-time.Hour).Unix()
	token := signToken(t, key, claims)

	mustReject(t, client, token, "nonce-1")
}

func TestVerifyIDToken_RejectsNonceMismatch(t *testing.T) {
	key := newTestKey(t, "key-1")
	client := newTestClient(t, key)
	token := signToken(t, key, validClaims("client-1", "nonce-1"))

	mustReject(t, client, token, "a-different-nonce")
}

func TestVerifyIDToken_RejectsEmptyNonceArgument(t *testing.T) {
	key := newTestKey(t, "key-1")
	client := newTestClient(t, key)
	token := signToken(t, key, validClaims("client-1", "nonce-1"))

	mustReject(t, client, token, "")
}
