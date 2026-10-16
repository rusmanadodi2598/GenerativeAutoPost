// Package r2 signs and sends Cloudflare R2 object requests with AWS SigV4.
//
// @file      internal/platform/r2/sigv4.go
// @for       AWS Signature Version 4 over the standard library.
// @uses      crypto/hmac, crypto/sha256, net/http, net/url, sort, time
// @reason    R2 is S3 compatible and SigV4 needs no SDK dependency.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-16
package r2

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	sigAlgorithm  = "AWS4-HMAC-SHA256"
	sigService    = "s3"
	sigTerminator = "aws4_request"
	// emptyBodyHash is SHA-256 of the empty string, the payload hash a bodyless
	// GET, HEAD, or DELETE signs with.
	emptyBodyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// credentials is the static key pair plus the signing region.
type credentials struct {
	accessKey string
	secretKey string
	region    string
}

// signRequest signs req in place and sets its Authorization header. The caller
// has already set any header it wants signed, including x-amz-content-sha256;
// this only adds x-amz-date. service names the AWS service in the credential
// scope, so the AWS test suite vectors can sign for "service" while production
// signs for "s3".
func signRequest(req *http.Request, creds credentials, service, payloadHash string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	scope := strings.Join([]string{dateStamp, creds.region, service, sigTerminator}, "/")
	req.Header.Set("x-amz-date", amzDate)

	canonical, signedHeaders := canonicalRequest(req, payloadHash)
	canonicalHash := sha256Hex([]byte(canonical))
	stringToSign := strings.Join([]string{
		sigAlgorithm, amzDate, scope, canonicalHash,
	}, "\n")
	key := signingKey(creds.secretKey, dateStamp, creds.region, service)
	signature := hex.EncodeToString(hmacSHA256(key, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		sigAlgorithm, creds.accessKey, scope, signedHeaders, signature))
}

// canonicalRequest builds the canonical request and its signed header list.
func canonicalRequest(req *http.Request, payloadHash string) (string, string) {
	headers, signed := canonicalHeaders(req)
	return strings.Join([]string{
		req.Method,
		canonicalURI(req.URL.Path),
		canonicalQuery(req.URL.RawQuery),
		headers,
		signed,
		payloadHash,
	}, "\n"), signed
}

// canonicalHeaders signs the host and every x-amz-* header, sorted by lower
// case name, as SigV4 requires. Other headers stay unsigned.
func canonicalHeaders(req *http.Request) (string, string) {
	values := map[string]string{"host": req.Host}
	if values["host"] == "" {
		values["host"] = req.URL.Host
	}
	for name, list := range req.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-amz-") {
			values[lower] = strings.Join(list, ",")
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte(':')
		b.WriteString(strings.TrimSpace(values[name]))
		b.WriteByte('\n')
	}
	return b.String(), strings.Join(names, ";")
}

// canonicalURI URI-encodes each path segment without touching the separators.
func canonicalURI(path string) string {
	if path == "" {
		path = "/"
	}
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = uriEncode(segment, false)
	}
	out := strings.Join(segments, "/")
	if !strings.HasPrefix(out, "/") {
		out = "/" + out
	}
	return out
}

// canonicalQuery decodes the raw query and re-encodes every pair once, so an
// already escaped value is not escaped a second time, then sorts by key and
// value. A query this client never sends is signed defensively.
func canonicalQuery(raw string) string {
	if raw == "" {
		return ""
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return ""
	}
	type pair struct{ key, value string }
	pairs := make([]pair, 0, len(values))
	for key, list := range values {
		for _, value := range list {
			pairs = append(pairs, pair{uriEncode(key, true), uriEncode(value, true)})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key != pairs[j].key {
			return pairs[i].key < pairs[j].key
		}
		return pairs[i].value < pairs[j].value
	})
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = p.key + "=" + p.value
	}
	return strings.Join(parts, "&")
}

// uriEncode follows the SigV4 rule: unreserved characters pass through and
// everything else becomes an uppercase percent escape. Slashes pass through
// only when the caller allows them.
func uriEncode(value string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// signingKey derives the date, region, service, and terminator HMACs.
func signingKey(secretKey, dateStamp, region, service string) []byte {
	date := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStamp))
	regionKey := hmacSHA256(date, []byte(region))
	serviceKey := hmacSHA256(regionKey, []byte(service))
	return hmacSHA256(serviceKey, []byte(sigTerminator))
}

// hmacSHA256 returns the HMAC of data under key.
func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// sha256Hex returns the lowercase hex SHA-256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
