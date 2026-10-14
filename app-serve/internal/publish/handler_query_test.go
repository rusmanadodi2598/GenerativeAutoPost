// Package publish covers the lookup, list, chunk, and reconcile HTTP routes.
//
// @file      internal/publish/handler_query_test.go
// @for       HTTP tests for the publish read and upload routes.
// @uses      encoding/json, net/http, httptest, strings, testing, time, gin, internal/apierror
// @reason    Splits the handler suite so each file stays under the line limit.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package publish

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// TestShowHidesForeignAttempt asserts an attempt the operator does not own
// answers 404 instead of confirming the id exists.
func TestShowHidesForeignAttempt(t *testing.T) {
	svc := NewService(Config{
		Poster: &fakePoster{}, Accounts: &fakeAccounts{err: apierror.New(apierror.CodeForbidden, 0, "Not yours.")},
		Creators: &fakeCreators{}, Attempts: &fakeAttempts{},
		Idempotency: newFakeIdempotency(), MediaHost: "media.example",
	})
	reader := &fakeReader{records: []Record{{ID: chunkAttemptID, AccountID: testAccountID}}}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	RegisterRoutes(engine, NewHandler(svc, reader, &fakeReconciler{},
		func(*gin.Context) (string, bool) { return testOperator, true }, HandlerConfig{}))
	req := httptest.NewRequest(http.MethodGet, "/publish/"+chunkAttemptID, nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if code := bodyCode(t, rec); code != string(apierror.CodeNotFound) {
		t.Fatalf("code = %s, body = %s", code, rec.Body)
	}
}

// TestShowReturnsOwnedAttempt asserts the owned attempt renders without an
// upload URL field.
func TestShowReturnsOwnedAttempt(t *testing.T) {
	h := newHandlerHarness(t)
	h.reader.records = []Record{{
		ID: chunkAttemptID, AccountID: testAccountID, PublishID: testPublishID,
		Status: StatusCreated, UploadedBytes: 100,
	}}
	rec := h.do(t, http.MethodGet, "/publish/"+chunkAttemptID, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), testPublishID) {
		t.Fatalf("expected the publish id, body = %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), "upload_url") {
		t.Fatalf("response leaked the upload url: %s", rec.Body)
	}
}

// TestListClampsLimit asserts the default and maximum page sizes reach the
// repository.
func TestListClampsLimit(t *testing.T) {
	h := newHandlerHarness(t)
	rec := h.do(t, http.MethodGet, "/publish", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if h.reader.lastLimit != defaultListLimit {
		t.Fatalf("default limit = %d, want %d", h.reader.lastLimit, defaultListLimit)
	}
	if rec := h.do(t, http.MethodGet, "/publish?limit=500", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if h.reader.lastLimit != maxListLimit {
		t.Fatalf("clamped limit = %d, want %d", h.reader.lastLimit, maxListLimit)
	}
	if rec := h.do(t, http.MethodGet, "/publish?limit=0", "", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
}

// TestListParsesCursor asserts the millisecond cursor reaches the repository
// as a timestamp.
func TestListParsesCursor(t *testing.T) {
	h := newHandlerHarness(t)
	want := time.UnixMilli(1_700_000_000_000).UTC()
	rec := h.do(t, http.MethodGet, "/publish?cursor=1700000000000", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if !h.reader.lastCursor.Equal(want) {
		t.Fatalf("cursor = %s, want %s", h.reader.lastCursor, want)
	}
	if rec := h.do(t, http.MethodGet, "/publish?cursor=nope", "", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
}

// TestPutChunkReturnsProgress asserts a chunk upload returns the progress
// shape and that a mismatched content type answers 422.
func TestPutChunkReturnsProgress(t *testing.T) {
	ch := newChunkHarness()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	RegisterRoutes(engine, NewHandler(ch.svc, &fakeReader{}, &fakeReconciler{},
		func(*gin.Context) (string, bool) { return testOperator, true },
		HandlerConfig{MaxChunkBytes: 1 << 20}))

	payload := strings.Repeat("x", 100)
	req := httptest.NewRequest(http.MethodPut, "/publish/"+chunkAttemptID+"/chunks", strings.NewReader(payload))
	req.Header.Set("Content-Type", chunkMediaType)
	req.Header.Set(chunkIndexHeader, "0")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var envelope struct {
		Data ChunkPutResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decoding envelope: %v", err)
	}
	if envelope.Data.UploadedBytes != 100 || envelope.Data.NextChunkIndex != 1 {
		t.Fatalf("unexpected progress: %+v", envelope.Data)
	}
	if strings.Contains(rec.Body.String(), "upload_url") {
		t.Fatalf("response leaked the upload url: %s", rec.Body)
	}

	bad := httptest.NewRequest(http.MethodPut, "/publish/"+chunkAttemptID+"/chunks", strings.NewReader(payload))
	bad.Header.Set("Content-Type", "application/json")
	bad.Header.Set(chunkIndexHeader, "1")
	badRec := httptest.NewRecorder()
	engine.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", badRec.Code, badRec.Body)
	}
}

// TestReconcileAnswersAccepted asserts the reconcile route answers 202 and
// reaches the reconciler.
func TestReconcileAnswersAccepted(t *testing.T) {
	h := newHandlerHarness(t)
	h.recon.records = []Record{{ID: chunkAttemptID, AccountID: testAccountID, Status: StatusPublishComplete}}
	rec := h.do(t, http.MethodPost, "/publish/"+chunkAttemptID+"/reconcile", "", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if len(h.recon.calls) != 1 || h.recon.calls[0] != chunkAttemptID {
		t.Fatalf("reconcile calls = %v", h.recon.calls)
	}
}
