// Package r2 signs and sends Cloudflare R2 object requests with AWS SigV4.
//
// @file      internal/platform/r2/client_test.go
// @for       Signed request tests against an in-process object endpoint.
// @uses      net/http, net/http/httptest, testing, bytes, errors, io
// @reason    Proves streaming, miss mapping, and the boot probe without R2.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     util
// @stability stable
// @since     2026-10-16
package r2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient points a client at the given server with a fixed endpoint.
func newTestClient(t *testing.T, endpoint string) *Client {
	t.Helper()
	client, err := New(Config{
		Endpoint: endpoint, Bucket: "media",
		AccessKey: "AKIDEXAMPLE", SecretKey: "secret", Region: "auto",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

// TestPutObjectStreamsSignedRequest proves the body streams through with the
// caller's size, hash, and a signed Authorization header.
func TestPutObjectStreamsSignedRequest(t *testing.T) {
	var (
		gotPath, gotAuth, gotHash, gotBody string
		gotLength                          int64
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotHash = r.Header.Get("x-amz-content-sha256")
		gotLength = r.ContentLength
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	payload := []byte("streamed bytes")
	err := client.PutObject(context.Background(), "media", "obj-1",
		bytes.NewReader(payload), int64(len(payload)), sha256Hex(payload))
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	if gotPath != "/media/obj-1" {
		t.Errorf("path = %q, want /media/obj-1", gotPath)
	}
	if gotBody != string(payload) {
		t.Errorf("body = %q, want %q", gotBody, payload)
	}
	if gotLength != int64(len(payload)) {
		t.Errorf("content length = %d, want %d", gotLength, len(payload))
	}
	if gotHash != sha256Hex(payload) {
		t.Errorf("content sha256 = %q, want %q", gotHash, sha256Hex(payload))
	}
	if !strings.HasPrefix(gotAuth, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/") {
		t.Errorf("authorization = %q, want an AWS4-HMAC-SHA256 credential header", gotAuth)
	}
	if !strings.Contains(gotAuth, "SignedHeaders=host;x-amz-content-sha256;x-amz-date") {
		t.Errorf("authorization did not sign the payload hash header: %q", gotAuth)
	}
}

// TestGetObjectReturnsBodyAndTypedMiss proves a hit returns the stream and a
// 404 is a typed miss the media layer can recognise.
func TestGetObjectReturnsBodyAndTypedMiss(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/media/there" {
			_, _ = w.Write([]byte("payload"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	body, err := client.GetObject(context.Background(), "media", "there")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := body.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}
	if string(data) != "payload" {
		t.Errorf("body = %q, want payload", data)
	}

	_, err = client.GetObject(context.Background(), "media", "absent")
	var miss notFoundError
	if !errors.As(err, &miss) {
		t.Fatalf("absent get error = %v, want a typed not-found", err)
	}
}

// TestDeleteObjectMissIsTyped proves a delete of an absent key is a miss, not a
// storage fault.
func TestDeleteObjectMissIsTyped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	err := client.DeleteObject(context.Background(), "media", "absent")

	var miss notFoundError
	if !errors.As(err, &miss) {
		t.Fatalf("delete error = %v, want a typed not-found", err)
	}
}

// TestCheckProvesCredentials proves a 2xx HeadBucket passes and a 403 fails.
func TestCheckProvesCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead && r.URL.Path == "/media" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	if err := client.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}

	denied := newTestClient(t, server.URL)
	denied.bucket = "other"
	if err := denied.Check(context.Background()); err == nil {
		t.Fatal("Check passed for a forbidden bucket")
	}
}

// TestPutObjectRejectsMissingSigningInput proves the client refuses to send a
// request it cannot sign correctly.
func TestPutObjectRejectsMissingSigningInput(t *testing.T) {
	client := newTestClient(t, "https://example.invalid")

	if err := client.PutObject(context.Background(), "media", "obj", strings.NewReader("x"), 0, "hash"); err == nil {
		t.Error("PutObject accepted a zero size")
	}
	if err := client.PutObject(context.Background(), "media", "obj", strings.NewReader("x"), 1, ""); err == nil {
		t.Error("PutObject accepted an empty content sha256")
	}
}

// TestNewRejectsBadEndpoint proves a misconfigured endpoint fails at build.
func TestNewRejectsBadEndpoint(t *testing.T) {
	if _, err := New(Config{Endpoint: "not-a-url"}); err == nil {
		t.Error("New accepted a relative endpoint")
	}
	if _, err := New(Config{Endpoint: "ftp://example.test"}); err == nil {
		t.Error("New accepted a non-http scheme")
	}
}
