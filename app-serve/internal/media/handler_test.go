// Package media serves upload, lookup, and delete endpoints over one Service.
//
// @file      internal/media/handler_test.go
// @for       Coverage for the media upload handler and its shared test helpers.
// @uses      context, io, log/slog, net/http, httptest, mime/multipart, testing, gin
// @reason    Locks the upload status codes and the envelope shape clients see.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package media

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// fakeStore keeps objects in memory so the tests never touch a real backend.
type fakeStore struct {
	objects     map[string]MediaObject
	payloads    map[string][]byte
	signedURL   string
	deletedIDs  []string
	deleteCalls int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		objects:  map[string]MediaObject{},
		payloads: map[string][]byte{},
	}
}

func (f *fakeStore) Put(_ context.Context, obj MediaObject, r io.Reader) error {
	payload, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.objects[obj.ID] = obj
	f.payloads[obj.ID] = payload
	return nil
}

func (f *fakeStore) Get(_ context.Context, id string) (MediaObject, io.ReadCloser, error) {
	obj, ok := f.objects[id]
	if !ok {
		return MediaObject{}, nil, errObjectMissing
	}
	return obj, io.NopCloser(bytes.NewReader(f.payloads[id])), nil
}

func (f *fakeStore) Delete(_ context.Context, id string) error {
	f.deleteCalls++
	f.deletedIDs = append(f.deletedIDs, id)
	delete(f.objects, id)
	return nil
}

func (f *fakeStore) SignPublicURL(_ context.Context, id string) (string, error) {
	if _, ok := f.objects[id]; !ok {
		return "", errObjectMissing
	}
	return f.signedURL, nil
}

// testRouter wires the real route table over a fake store and a session stub.
func testRouter(t *testing.T, store Store, account string, maxBody int64) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	svc := NewService(store, ServiceConfig{
		Driver:        string(MediaStorageDisk),
		MaxBytes:      1 << 20,
		MaxDurationMs: 60_000,
		AllowedTypes:  []string{"video/mp4", "image/jpeg"},
	}, slog.New(slog.DiscardHandler))
	session := func(*gin.Context) (string, bool) { return account, account != "" }
	RegisterRoutes(router, NewHandler(svc, store, session, maxBody))
	return router
}

// uploadRequest builds a multipart upload body with the given fields.
func uploadRequest(t *testing.T, contentType, fileName, body string, fields map[string]string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if fileName != "" {
		header := make(map[string][]string)
		if contentType != "" {
			header["Content-Disposition"] = []string{
				`form-data; name="file"; filename="` + fileName + `"`}
			header["Content-Type"] = []string{contentType}
		} else {
			header["Content-Disposition"] = []string{
				`form-data; name="file"; filename="` + fileName + `"`}
		}
		part, err := w.CreatePart(header)
		if err != nil {
			t.Fatalf("creating file part: %v", err)
		}
		if _, err := part.Write([]byte(body)); err != nil {
			t.Fatalf("writing file part: %v", err)
		}
	}
	for name, value := range fields {
		if err := w.WriteField(name, value); err != nil {
			t.Fatalf("writing field %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/media/uploads", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

// bodyCode reads the problem code from an error response.
func bodyCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body %q: %v", rec.Body.String(), err)
	}
	return body.Code
}

func TestUploadHandlerRejectsDisallowedContentType(t *testing.T) {
	router := testRouter(t, newFakeStore(), "acct-1", 0)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, uploadRequest(t, "application/x-msdownload", "run.exe", "MZ",
		map[string]string{"kind": "video"}))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if code := bodyCode(t, rec); code != "VALIDATION_FAILED" {
		t.Errorf("code = %q, want VALIDATION_FAILED", code)
	}
}

func TestUploadHandlerRejectsMissingFilePart(t *testing.T) {
	router := testRouter(t, newFakeStore(), "acct-1", 0)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, uploadRequest(t, "", "", "", map[string]string{"kind": "video"}))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if code := bodyCode(t, rec); code != "VALIDATION_FAILED" {
		t.Errorf("code = %q, want VALIDATION_FAILED", code)
	}
}

func TestUploadHandlerRejectsUnknownKind(t *testing.T) {
	router := testRouter(t, newFakeStore(), "acct-1", 0)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, uploadRequest(t, "video/mp4", "clip.mp4", "bytes",
		map[string]string{"kind": "audio"}))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestUploadHandlerRejectsNonNumericDuration(t *testing.T) {
	router := testRouter(t, newFakeStore(), "acct-1", 0)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, uploadRequest(t, "video/mp4", "clip.mp4", "bytes",
		map[string]string{"kind": "video", "duration_ms": "soon"}))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestUploadHandlerRequiresSession(t *testing.T) {
	router := testRouter(t, newFakeStore(), "", 0)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, uploadRequest(t, "video/mp4", "clip.mp4", "bytes",
		map[string]string{"kind": "video"}))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
	if code := bodyCode(t, rec); code != "UNAUTHORIZED" {
		t.Errorf("code = %q, want UNAUTHORIZED", code)
	}
}

func TestUploadHandlerRejectsBodyOverCap(t *testing.T) {
	store := newFakeStore()
	router := testRouter(t, store, "acct-1", 16)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, uploadRequest(t, "video/mp4", "clip.mp4", strings.Repeat("a", 4096),
		map[string]string{"kind": "video"}))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if len(store.objects) != 0 {
		t.Errorf("store holds %d objects, want 0", len(store.objects))
	}
}
