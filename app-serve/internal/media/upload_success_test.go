// Package media serves upload, lookup, and delete endpoints over one Service.
//
// @file      internal/media/upload_success_test.go
// @for       Coverage for the successful media upload response.
// @uses      crypto/sha256, encoding/hex, encoding/json, net/http, httptest, testing
// @reason    Locks the 201 envelope fields a client reads after upload.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package media

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUploadHandlerAcceptsVideoAndReturnsEnvelope(t *testing.T) {
	store := newFakeStore()
	router := testRouter(t, store, "acct-1", 0)
	payload := "video bytes"

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, uploadRequest(t, "video/mp4", "clip.mp4", payload,
		map[string]string{"kind": "video", "duration_ms": "1200"}))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			ID             string `json:"id"`
			ChecksumSHA256 string `json:"checksum_sha256"`
			Kind           string `json:"kind"`
		} `json:"data"`
		Meta struct {
			RequestID  string `json:"request_id"`
			ServerTime string `json:"server_time"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if body.Data.ID == "" {
		t.Error("id is empty")
	}
	if body.Data.Kind != "video" {
		t.Errorf("kind = %q, want video", body.Data.Kind)
	}
	sum := sha256.Sum256([]byte(payload))
	if want := hex.EncodeToString(sum[:]); body.Data.ChecksumSHA256 != want {
		t.Errorf("checksum = %q, want %q", body.Data.ChecksumSHA256, want)
	}
	if body.Meta.ServerTime == "" {
		t.Error("meta.server_time is empty")
	}
	if got := string(store.payloads[body.Data.ID]); got != payload {
		t.Errorf("stored payload = %q, want %q", got, payload)
	}
}
