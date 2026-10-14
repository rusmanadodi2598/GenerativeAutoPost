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
		disposition := `form-data; name="file"; filename="` + fileName + `"`
		header := map[string][]string{"Content-Disposition": {disposition}}
		if contentType != "" {
			header["Content-Type"] = []string{contentType}
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
	rec := uploadCase(t, newFakeStore(), "acct-1", 0, "application/x-msdownload", "run.exe",
		"audio")
	assertStatus(t, rec, http.StatusUnprocessableEntity)
	assertCode(t, rec, "VALIDATION_FAILED")
}

// uploadCase runs one upload through a fresh router and returns the recorder.
func uploadCase(t *testing.T, store Store, account string, maxBody int64,
	contentType, fileName, kind string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	testRouter(t, store, account, maxBody).ServeHTTP(rec,
		uploadRequest(t, contentType, fileName, "bytes", map[string]string{"kind": kind}))
	return rec
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
}

func assertCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if code := bodyCode(t, rec); code != want {
		t.Errorf("code = %q, want %s", code, want)
	}
}

func TestUploadHandlerRejectsMissingFilePart(t *testing.T) {
	rec := uploadCase(t, newFakeStore(), "acct-1", 0, "", "", "video")
	assertStatus(t, rec, http.StatusUnprocessableEntity)
	assertCode(t, rec, "VALIDATION_FAILED")
}

func TestUploadHandlerRejectsUnknownKind(t *testing.T) {
	rec := uploadCase(t, newFakeStore(), "acct-1", 0, "video/mp4", "clip.mp4", "audio")
	assertStatus(t, rec, http.StatusUnprocessableEntity)
}

func TestUploadHandlerRejectsNonNumericDuration(t *testing.T) {
	router := testRouter(t, newFakeStore(), "acct-1", 0)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, uploadRequest(t, "video/mp4", "clip.mp4", "bytes",
		map[string]string{"kind": "video", "duration_ms": "soon"}))

	assertStatus(t, rec, http.StatusUnprocessableEntity)
}

func TestUploadHandlerRequiresSession(t *testing.T) {
	rec := uploadCase(t, newFakeStore(), "", 0, "video/mp4", "clip.mp4", "video")
	assertStatus(t, rec, http.StatusUnauthorized)
	assertCode(t, rec, "UNAUTHORIZED")
}

func seedObject(t *testing.T, store *fakeStore) string {
	t.Helper()
	obj := MediaObject{ID: "media-1", Kind: MediaKindVideo, ContentType: "video/mp4", ByteSize: 4}
	if err := store.Put(context.Background(), obj, strings.NewReader("bytes")); err != nil {
		t.Fatalf("seeding store: %v", err)
	}
	return obj.ID
}

// requireSessionCase runs one guarded route with no session and asserts 401.
func requireSessionCase(t *testing.T, method, id string, store *fakeStore) {
	t.Helper()
	rec := httptest.NewRecorder()
	testRouter(t, store, "", 0).ServeHTTP(rec, httptest.NewRequest(method, "/media/"+id, nil))
	assertStatus(t, rec, http.StatusUnauthorized)
	assertCode(t, rec, "UNAUTHORIZED")
}

func TestShowHandlerRequiresSession(t *testing.T) {
	store := newFakeStore()
	requireSessionCase(t, http.MethodGet, seedObject(t, store), store)
}

func TestDeleteHandlerRequiresSession(t *testing.T) {
	store := newFakeStore()
	id := seedObject(t, store)

	requireSessionCase(t, http.MethodDelete, id, store)

	if store.deleteCalls != 0 {
		t.Errorf("deleteCalls = %d, want 0", store.deleteCalls)
	}
	if _, ok := store.objects[id]; !ok {
		t.Errorf("object %q was deleted, want it kept", id)
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
