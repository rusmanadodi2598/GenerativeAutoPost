// Package webhook holds the shared helpers for the intake handler tests.
//
// @file      internal/webhook/handler_support_test.go
// @for       Signing, handler wiring, and delivery helpers for handler tests.
// @uses      bytes, context, crypto/hmac, crypto/sha256, encoding/hex, errors, log/slog, net/http, net/http/httptest, strconv, testing, time, gin
// @reason    Keeps the handler test file focused on behavior.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// signHandler builds the TikTok-Signature header for a raw body.
func signHandler(secret string, stamp int64, body []byte) string {
	payload := append([]byte(strconv.FormatInt(stamp, 10)+"."), body...)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "t=" + strconv.FormatInt(stamp, 10) + ",s=" + hex.EncodeToString(mac.Sum(nil))
}

// intakeSetup wires a handler whose dispatcher drives the fakes. The
// Dispatcher type takes the concrete *Store, so the same package seam
// used in the dispatcher tests swaps in the memory fake here.
func intakeSetup(t *testing.T, secret string, store *fakeStore, statuses *fakeStatuses) (*Handler, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	d := NewDispatcher(nil, statuses, DispatcherConfig{OutboxAggregate: "webhook_event"}, logger)
	d.store = store
	h := NewHandler(nil, d, HandlerConfig{Secret: secret}, logger)
	return h, &logs
}

// postDelivery runs Receive with the given header and body on a recorder.
func postDelivery(h *Handler, header string, body []byte) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/tiktok", bytes.NewReader(body))
	req.Header.Set("TikTok-Signature", header)
	req.RemoteAddr = "203.0.113.7:4200"
	c.Request = req
	h.Receive(c)
	return rec
}

// completeBody returns the wire body for a post.publish.complete delivery.
func completeBody() []byte {
	return []byte(`{"client_key":"awx123","event":"post.publish.complete","create_time":1728664800,"user_openid":"openid-9","content":"{\"publish_id\":\"pub-1\",\"post_id\":\"post-7\"}"}`)
}

// newIntake wires an intake handler over fresh fakes and a captured log.
func newIntake(t *testing.T, secret string) (*Handler, *fakeStore, *fakeStatuses, *bytes.Buffer) {
	t.Helper()
	store := &fakeStore{stored: true}
	statuses := &fakeStatuses{}
	h, logs := intakeSetup(t, secret, store, statuses)
	return h, store, statuses, logs
}

// signedDelivery builds a header and body pair stamped now.
func signedDelivery(secret string, body []byte) (string, []byte) {
	stamp := time.Now().UTC().Unix()
	return signHandler(secret, stamp, body), body
}

// failingStore returns a database error so the 500 path can be exercised.
type failingStore struct {
	fakeStore
}

func (f *failingStore) InsertIfNew(_ context.Context, ev Event) (bool, error) {
	f.inserted = append(f.inserted, ev)
	return false, errors.New("webhook: insert failed")
}
