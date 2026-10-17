// Package googleoidc verifies Google id tokens against the published key set.
//
// @file      internal/platform/googleoidc/jwks.go
// @for       Fetch, cache, and decode the Google JWKS into RSA public keys.
// @uses      net/http, encoding/json, encoding/base64, crypto/rsa, math/big
// @reason    The local signature check needs Google's rotating keys without a JWT library.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-17
package googleoidc

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// jwksTTL bounds how long a fetched key set is trusted before a revalidation.
const jwksTTL = time.Hour

// jwksBodyCap caps the fetched key set, a few kilobytes in practice.
const jwksBodyCap = 1 << 20

type jwksDocument struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// keyCache stores the decoded keys and the time they were fetched.
type keyCache struct {
	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

// publicKey returns the key named by kid. A missing kid, or an expired cache,
// triggers one refetch so Google key rotation does not lock the operator out.
func (c *Client) publicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.cache.mu.Lock()
	defer c.cache.mu.Unlock()

	if key, ok := c.cache.keys[kid]; ok && time.Since(c.cache.fetchedAt) < jwksTTL {
		return key, nil
	}
	if err := c.fetchKeys(ctx); err != nil {
		return nil, err
	}
	if key, ok := c.cache.keys[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("googleoidc: signing key %q not found", kid)
}

func (c *Client) fetchKeys(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.JWKSURL, nil)
	if err != nil {
		return fmt.Errorf("googleoidc: building jwks request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("googleoidc: fetching jwks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("googleoidc: jwks returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, jwksBodyCap))
	if err != nil {
		return fmt.Errorf("googleoidc: reading jwks: %w", err)
	}
	var doc jwksDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("googleoidc: decoding jwks: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, entry := range doc.Keys {
		if entry.Kty != "RSA" {
			continue
		}
		key, err := entry.publicKey()
		if err != nil {
			return fmt.Errorf("googleoidc: decoding key %q: %w", entry.Kid, err)
		}
		keys[entry.Kid] = key
	}
	c.cache.keys = keys
	c.cache.fetchedAt = time.Now()
	return nil
}

// publicKey decodes the modulus and exponent of a JWKS entry.
func (k jwkKey) publicKey() (*rsa.PublicKey, error) {
	modulus, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decoding modulus: %w", err)
	}
	exponent, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decoding exponent: %w", err)
	}
	e := new(big.Int).SetBytes(exponent)
	if e.BitLen() > 31 {
		return nil, fmt.Errorf("exponent too large")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: int(e.Int64())}, nil
}
