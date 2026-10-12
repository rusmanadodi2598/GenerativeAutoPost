// Package apierror_test pins the RFC 9457 error shape and redaction.
//
// @file      internal/apierror/apierror_test.go
// @for       Table tests for Write output and secret redaction.
// @uses      testing, net/http/httptest, internal/apierror
// @reason    Proves error contract behavior before the writer exists.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package apierror_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// decode runs Write and parses the JSON body plus headers.
func decode(t *testing.T, dev bool, err *apierror.Error, path string) (map[string]any, http.Header) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	(&apierror.Writer{Development: dev}).Write(w, r, err)
	res := w.Result()
	defer func() {
		_ = res.Body.Close()
	}()
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body, res.Header
}

// sixMembers asserts the required RFC 9457 members are present.
func sixMembers(t *testing.T, body map[string]any) {
	t.Helper()
	for _, k := range []string{"type", "title", "status", "detail", "instance", "code"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("member %q missing in %v", k, body)
		}
	}
}

func TestWriteShape(t *testing.T) {
	err := apierror.New(apierror.CodeValidationFailed, 422, "bad input")
	body, hdr := decode(t, false, err, "/api/v1/publish/videos")
	sixMembers(t, body)
	if ct := hdr.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	if _, ok := body["log_id"]; ok {
		t.Fatalf("log_id must be omitted when empty, got %v", body)
	}
	if _, ok := body["debug"]; ok {
		t.Fatalf("debug must be absent in production, got %v", body)
	}
}

func TestWriteDebugGating(t *testing.T) {
	err := apierror.New(apierror.CodeValidationFailed, 422, "bad input")
	devBody, _ := decode(t, true, err, "/api/v1/publish/videos")
	if _, ok := devBody["debug"]; !ok {
		t.Fatal("debug must be present in development")
	}
	prodBody, _ := decode(t, false, err, "/api/v1/publish/videos")
	if _, ok := prodBody["debug"]; ok {
		t.Fatal("debug must be absent in production")
	}
}

func TestWriteRedaction(t *testing.T) {
	secret := strings.Repeat("x", 4) + "-fixture-secret"
	for _, dev := range []bool{true, false} {
		err := apierror.New(apierror.CodeInternal, 500, "call failed with client_secret="+secret)
		body, _ := decode(t, dev, err, "/api/v1/publish/videos")
		raw, _ := json.Marshal(body)
		if strings.Contains(string(raw), secret) {
			t.Fatalf("secret leaked in dev=%v body: %s", dev, raw)
		}
	}
}

func TestValidationFailedGolden(t *testing.T) {
	if os.Getenv("GENAUTO_UPDATE_GOLDEN") == "1" {
		t.Skip("golden regeneration run")
	}
	raw, err := os.ReadFile("testdata/validation_failed.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var want map[string]any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	errObj := apierror.New(apierror.CodeValidationFailed, 422, want["detail"].(string))
	body, _ := decode(t, false, errObj, "/api/v1/publish/videos")
	for _, k := range []string{"type", "title", "status", "code", "instance"} {
		if body[k] != want[k] {
			t.Fatalf("member %q = %v, want %v", k, body[k], want[k])
		}
	}
}
