// Package publish fakes the collaborators the publish handlers depend on.
//
// @file      internal/publish/handler_fakes_test.go
// @for       Fakes and the router harness for the publish HTTP tests.
// @uses      context, net/http, httptest, testing, gin, internal/apierror
// @reason    Keeps the handler tests free of Redis, PostgreSQL, and TikTok.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package publish

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// fakeReader serves attempt rows and records the list arguments it receives.
type fakeReader struct {
	records     []Record
	err         error
	lastLimit   int
	lastCursor  time.Time
	lastAccount string
	lastStatus  string
}

// Get returns the first canned row or a 404 shaped error.
func (f *fakeReader) Get(_ context.Context, id string) (Record, error) {
	if f.err != nil {
		return Record{}, f.err
	}
	for _, rec := range f.records {
		if rec.ID == id {
			return rec, nil
		}
	}
	return Record{}, apierror.New(apierror.CodeNotFound, 0, "Publish attempt not found.")
}

// List records the query arguments and returns the canned rows.
func (f *fakeReader) List(_ context.Context, accountID, status string, cursor time.Time, limit int) ([]Record, error) {
	f.lastAccount, f.lastStatus, f.lastCursor, f.lastLimit = accountID, status, cursor, limit
	if f.err != nil {
		return nil, f.err
	}
	return f.records, nil
}

// fakeReconciler records the reconcile calls it receives.
type fakeReconciler struct {
	records []Record
	calls   []string
	err     error
}

// Reconcile returns the first canned row.
func (f *fakeReconciler) Reconcile(_ context.Context, _, attemptID string) (Record, error) {
	f.calls = append(f.calls, attemptID)
	if f.err != nil {
		return Record{}, f.err
	}
	if len(f.records) == 0 {
		return Record{}, nil
	}
	return f.records[0], nil
}

// handlerHarness wires the routes over fakes with one operator session.
type handlerHarness struct {
	router  *gin.Engine
	reader  *fakeReader
	recon   *fakeReconciler
	idempot *fakeIdempotency
	chunks  *fakeChunkStore
}

// newHandlerHarness builds the router with a signed in operator.
func newHandlerHarness(t *testing.T) *handlerHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	return newHandlerHarnessWithSession(t, func(*gin.Context) (string, bool) {
		return testOperator, true
	})
}

// newHandlerHarnessWithSession builds the router with the given session.
func newHandlerHarnessWithSession(t *testing.T, session SessionAccount) *handlerHarness {
	t.Helper()
	svc := NewService(Config{
		Poster:      &fakePoster{uploadURL: "https://upload.example/v4"},
		Accounts:    &fakeAccounts{},
		Creators:    &fakeCreators{options: []string{"SELF_ONLY", "FOLLOWERS", "PUBLIC"}},
		Attempts:    &fakeAttempts{},
		Idempotency: newFakeIdempotency(),
		MediaHost:   "media.example",
	})
	h := &handlerHarness{
		reader:  &fakeReader{},
		recon:   &fakeReconciler{},
		idempot: newFakeIdempotency(),
		chunks:  &fakeChunkStore{},
	}
	svc.idempotency = h.idempot
	svc.WithChunkProxy(ChunkServiceConfig{
		Attempts: h.chunks, Chunks: h.chunks,
		Uploader: &fakeChunkPutter{}, Plans: NewMemoryPlanStore(),
		TTL: time.Hour, Now: func() time.Time { return time.Now().UTC() },
	})
	engine := gin.New()
	RegisterRoutes(engine, NewHandler(svc, h.reader, h.recon, session, HandlerConfig{MaxChunkBytes: 64 << 20}))
	h.router = engine
	return h
}

// do runs one request through the router and returns the recorder.
func (h *handlerHarness) do(t *testing.T, method, path string, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}
