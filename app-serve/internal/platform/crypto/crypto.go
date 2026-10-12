// Package crypto encrypts token material with AES-256-GCM.
//
// @file      internal/platform/crypto/crypto.go
// @for       Keyring encryption with key id prefix and HMAC checks.
// @uses      crypto/aes, crypto/cipher, crypto/hmac, crypto/rand
// @reason    Protects stored TikTok tokens and verifies webhook bodies.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     util
// @stability stable
// @since     2026-10-12
package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Keyring maps key ids to AES-256 keys so rotation keeps old rows readable.
type Keyring struct {
	keys map[string][]byte
}

// NewKeyring validates that every key is 32 bytes for AES-256.
func NewKeyring(keys map[string][]byte) (*Keyring, error) {
	if len(keys) == 0 {
		return nil, errors.New("crypto: keyring needs at least one key")
	}
	for id, k := range keys {
		if id == "" {
			return nil, errors.New("crypto: key id must not be empty")
		}
		if len(k) != 32 {
			return nil, fmt.Errorf("crypto: key %q must be 32 bytes, got %d", id, len(k))
		}
	}
	dup := make(map[string][]byte, len(keys))
	for id, k := range keys {
		cp := make([]byte, len(k))
		copy(cp, k)
		dup[id] = cp
	}
	return &Keyring{keys: dup}, nil
}

// buildGCM returns an AEAD cipher for one 32 byte key.
func buildGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new GCM: %w", err)
	}
	return gcm, nil
}

// Encrypt seals plaintext under keyID with a random nonce per call.
func (r *Keyring) Encrypt(ctx context.Context, plaintext []byte, keyID string) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", fmt.Errorf("crypto: encrypt: %w", err)
	}
	if len(plaintext) == 0 {
		return nil, "", errors.New("crypto: plaintext must not be empty")
	}
	key, ok := r.keys[keyID]
	if !ok {
		return nil, "", fmt.Errorf("crypto: unknown key id %q", keyID)
	}
	gcm, err := buildGCM(key)
	if err != nil {
		return nil, "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, "", fmt.Errorf("crypto: nonce: %w", err)
	}
	prefix := keyID + ":"
	sealed := gcm.Seal(nil, nonce, plaintext, []byte(prefix))
	out := make([]byte, 0, len(prefix)+len(nonce)+len(sealed))
	out = append(out, []byte(prefix)...)
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out, keyID, nil
}

// Decrypt reads the key id prefix, then opens the ciphertext.
func (r *Keyring) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", fmt.Errorf("crypto: decrypt: %w", err)
	}
	at := -1
	for i, b := range ciphertext {
		if b == ':' {
			at = i
			break
		}
	}
	if at <= 0 {
		return nil, "", errors.New("crypto: ciphertext missing key id prefix")
	}
	keyID := string(ciphertext[:at])
	key, ok := r.keys[keyID]
	if !ok {
		return nil, "", fmt.Errorf("crypto: unknown key id %q", keyID)
	}
	gcm, err := buildGCM(key)
	if err != nil {
		return nil, "", err
	}
	rest := ciphertext[at+1:]
	if len(rest) < gcm.NonceSize() {
		return nil, "", errors.New("crypto: ciphertext too short")
	}
	nonce := rest[:gcm.NonceSize()]
	sealed := rest[gcm.NonceSize():]
	open, err := gcm.Open(nil, nonce, sealed, []byte(keyID+":"))
	if err != nil {
		return nil, "", fmt.Errorf("crypto: open: %w", err)
	}
	return open, keyID, nil
}

// VerifyHMACSHA256 compares a hex MAC in constant time.
func VerifyHMACSHA256(key, message []byte, expectedHex string) bool {
	want, err := hex.DecodeString(strings.TrimSpace(expectedHex))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(message)
	return hmac.Equal(mac.Sum(nil), want)
}
