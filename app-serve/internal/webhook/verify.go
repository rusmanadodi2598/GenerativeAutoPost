// Package webhook verifies the TikTok-Signature header over a raw body.
//
// @file      internal/webhook/verify.go
// @for       Signature header parsing, timestamp tolerance, MAC check.
// @uses      strconv, strings, time, internal/apierror, internal/platform/crypto
// @reason    Authenticates inbound webhooks before any payload is trusted.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package webhook

import (
	"strconv"
	"strings"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/crypto"
)

// MaxBodyBytes caps the body read before verification runs.
const MaxBodyBytes = 64 << 10

// signatureParts holds the t and s members of TikTok-Signature.
type signatureParts struct {
	timestamp int64
	signature string
}

// invalidSignature is the single client safe failure for every reject
// path so nothing about the body, secret, or expected MAC leaks.
func invalidSignature() error {
	return apierror.New(apierror.CodeUnauthorized, 0, "The webhook signature is invalid.")
}

// parseSignature splits "t=<unix>,s=<hex>" and rejects anything else.
func parseSignature(header string) (signatureParts, bool) {
	var parts signatureParts
	var haveT, haveS bool
	for _, element := range strings.Split(header, ",") {
		key, value, found := strings.Cut(element, "=")
		if !found || value == "" {
			return parts, false
		}
		switch key {
		case "t":
			seconds, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return parts, false
			}
			parts.timestamp, haveT = seconds, true
		case "s":
			parts.signature, haveS = value, true
		}
	}
	return parts, haveT && haveS
}

// Verify parses the TikTok-Signature header and checks it over the
// exact raw body bytes. Every failure returns CodeUnauthorized. An empty
// secret refuses every delivery: HMAC with a zero length key is a real key,
// so a misconfigured deploy must not answer 200 to forged signatures.
func Verify(secret string, header string, rawBody []byte, now time.Time, tolerance time.Duration) error {
	if secret == "" {
		return invalidSignature()
	}
	parts, ok := parseSignature(header)
	if !ok {
		return invalidSignature()
	}
	drift := now.Sub(time.Unix(parts.timestamp, 0))
	if drift < 0 {
		drift = -drift
	}
	if drift > tolerance {
		return invalidSignature()
	}
	payload := make([]byte, 0, 21+len(rawBody))
	payload = append(payload, strconv.FormatInt(parts.timestamp, 10)...)
	payload = append(payload, '.')
	payload = append(payload, rawBody...)
	if !crypto.VerifyHMACSHA256([]byte(secret), payload, parts.signature) {
		return invalidSignature()
	}
	return nil
}
