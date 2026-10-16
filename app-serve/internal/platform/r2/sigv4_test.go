// Package r2 signs and sends Cloudflare R2 object requests with AWS SigV4.
//
// @file      internal/platform/r2/sigv4_test.go
// @for       SigV4 signing tests, including the AWS test suite vector.
// @uses      net/http, testing, time
// @reason    A signature that is off by one byte fails every upload at runtime.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     util
// @stability stable
// @since     2026-10-16
package r2

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestSignRequestMatchesAWSGetVanillaVector pins the whole signing chain to the
// official AWS SigV4 test suite get-vanilla case.
func TestSignRequestMatchesAWSGetVanillaVector(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	creds := credentials{
		accessKey: "AKIDEXAMPLE",
		secretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		region:    "us-east-1",
	}
	now := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)

	signRequest(req, creds, "service", emptyBodyHash, now)

	canonical, signed := canonicalRequest(req, emptyBodyHash)
	wantCanonical := strings.Join([]string{
		"GET",
		"/",
		"",
		"host:example.amazonaws.com\nx-amz-date:20150830T123600Z\n",
		"host;x-amz-date",
		emptyBodyHash,
	}, "\n")
	if canonical != wantCanonical {
		t.Errorf("canonical request mismatch:\n got %q\nwant %q", canonical, wantCanonical)
	}
	if signed != "host;x-amz-date" {
		t.Errorf("signed headers = %q, want host;x-amz-date", signed)
	}
	if got, want := sha256Hex([]byte(canonical)), "bb579772317eb040ac9ed261061d46c1f17a8133879d6129b6e1c25292927e63"; got != want {
		t.Errorf("canonical hash = %s, want %s", got, want)
	}
	wantAuth := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
		"SignedHeaders=host;x-amz-date, " +
		"Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := req.Header.Get("Authorization"); got != wantAuth {
		t.Errorf("authorization mismatch:\n got %q\nwant %q", got, wantAuth)
	}
}

// TestCanonicalQuerySortsPairs proves query parameters sort by key and value.
func TestCanonicalQuerySortsPairs(t *testing.T) {
	got := canonicalQuery("Param2=value2&Param1=value1")

	if want := "Param1=value1&Param2=value2"; got != want {
		t.Errorf("canonical query = %q, want %q", got, want)
	}
}

// TestCanonicalURIPreservesSlashes proves the path segments stay separable.
func TestCanonicalURIPreservesSlashes(t *testing.T) {
	got := canonicalURI("/bucket/folder/object name.bin")

	if want := "/bucket/folder/object%20name.bin"; got != want {
		t.Errorf("canonical uri = %q, want %q", got, want)
	}
}

// TestCanonicalURIEncodesAnEscapedRequestPathOnce proves a request path that
// already carries an escape is not escaped a second time, which would sign a
// different path than the client sends.
func TestCanonicalURIEncodesAnEscapedRequestPathOnce(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/bucket/obj%20name.bin", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if got, want := canonicalURI(req.URL.Path), "/bucket/obj%20name.bin"; got != want {
		t.Errorf("canonical uri = %q, want %q", got, want)
	}
}
