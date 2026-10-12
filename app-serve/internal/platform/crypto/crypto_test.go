// Package crypto_test verifies AES-256-GCM round trips and HMAC checks.
//
// @file      internal/platform/crypto/crypto_test.go
// @for       Table tests for Keyring encrypt and HMAC verification.
// @uses      testing, internal/platform/crypto
// @reason    Proves token encryption behavior before it is implemented.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     util
// @stability stable
// @since     2026-10-12
package crypto_test

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/crypto"
)

// testKey returns a fixed 32 byte key for deterministic cases.
func testKey() []byte {
	return []byte("0123456789abcdef0123456789abcdef")
}

// otherKey returns a different fixed 32 byte key for wrong-key cases.
func otherKey() []byte {
	return []byte("fedcba9876543210fedcba9876543210")
}

// mustRing builds a single-key ring or fails the test.
func mustRing(t *testing.T, id string, key []byte) *crypto.Keyring {
	t.Helper()
	ring, err := crypto.NewKeyring(map[string][]byte{id: key})
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	return ring
}

func TestKeyringRoundTrip(t *testing.T) {
	big := make([]byte, 64*1024)
	if _, err := rand.Read(big); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	cases := []struct {
		name      string
		plaintext []byte
		wantErr   bool
	}{
		// Empty input has nothing to protect, so it is rejected.
		{name: "empty fails", plaintext: []byte{}, wantErr: true},
		// One block exercises the smallest realistic token payload.
		{name: "16 bytes", plaintext: []byte("0123456789abcdef")},
		// 64KB exercises the webhook body size limit from 002 section 5.
		{name: "64KB random", plaintext: big},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ring := mustRing(t, "k1", testKey())
			ctx := context.Background()
			sealed, keyID, err := ring.Encrypt(ctx, tc.plaintext, "k1")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			if keyID != "k1" {
				t.Fatalf("keyID = %q, want k1", keyID)
			}
			open, gotID, err := ring.Decrypt(ctx, sealed)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if gotID != "k1" {
				t.Fatalf("decrypted keyID = %q, want k1", gotID)
			}
			if string(open) != string(tc.plaintext) {
				t.Fatal("decrypted bytes differ from plaintext")
			}
		})
	}
}

func TestKeyringDecryptWrongKey(t *testing.T) {
	a := mustRing(t, "k1", testKey())
	b := mustRing(t, "k1", otherKey())
	ctx := context.Background()
	sealed, _, err := a.Encrypt(ctx, []byte("token material"), "k1")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, _, err := b.Decrypt(ctx, sealed); err == nil {
		t.Fatal("expected error decrypting with the wrong key, got nil")
	}
}

func TestVerifyHMACSHA256(t *testing.T) {
	key := []byte("webhook-secret")
	body := []byte(`{"event":"post.publish.complete","publish_id":"abc"}`)
	sign := func(k, msg []byte) string {
		mac := hmac.New(sha256.New, k)
		mac.Write(msg)
		return hex.EncodeToString(mac.Sum(nil))
	}
	signed := []byte("1633174587." + string(body))
	valid := sign(key, signed)
	reordered := []byte(`{"publish_id":"abc","event":"post.publish.complete"}`)
	reorderedSigned := []byte("1633174587." + string(reordered))
	cases := []struct {
		name        string
		key         []byte
		message     []byte
		expectedHex string
		want        bool
	}{
		// Exact t.body vector from 002 section 5 must verify.
		{name: "exact vector", key: key, message: signed, expectedHex: valid, want: true},
		// A trailing newline changes the raw bytes, so it must fail.
		{name: "trailing newline", key: key, message: append(signed, '\n'), expectedHex: valid},
		// Re-ordered JSON keys change the raw bytes, so it must fail.
		{name: "reordered keys", key: key, message: reorderedSigned, expectedHex: valid},
		// Wrong key produces a different MAC, so it must fail.
		{name: "wrong key", key: []byte("other-secret"), message: signed, expectedHex: valid},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := crypto.VerifyHMACSHA256(tc.key, tc.message, tc.expectedHex); got != tc.want {
				t.Fatalf("VerifyHMACSHA256 = %v, want %v", got, tc.want)
			}
		})
	}
}
