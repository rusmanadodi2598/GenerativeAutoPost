// Package tiktokclient_test provides a scripted TikTok server for client tests.
//
// @file      internal/tiktokclient/faketiktok_test.go
// @for       In process fake TikTok server with scripted responses and a request log.
// @uses      net/http/httptest, net/url, sync, sync/atomic, io, testing
// @reason    Lets the client tests drive retries and wire errors without network access.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package tiktokclient

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// fakeResponse is one scripted HTTP reply from the fake server.
type fakeResponse struct {
	status  int
	body    string
	headers map[string]string
}

// recordedRequest captures what the client sent on one hit.
type recordedRequest struct {
	method  string
	path    string
	query   url.Values
	headers http.Header
	body    string
}

// fakeTikTok is an httptest backed stand in for the TikTok open API.
type fakeTikTok struct {
	server   *httptest.Server
	hitsN    atomic.Int64
	mu       sync.Mutex
	handlers map[string]fakeResponse
	queues   map[string][]fakeResponse
	log      []recordedRequest
}

// newFakeTikTok starts the fake server and closes it on test cleanup.
func newFakeTikTok(t *testing.T) *fakeTikTok {
	t.Helper()
	f := &fakeTikTok{
		handlers: map[string]fakeResponse{},
		queues:   map[string][]fakeResponse{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

// URL returns the base URL tests point the client at.
func (f *fakeTikTok) URL() string { return f.server.URL }

// hits reports how many requests the server has handled.
func (f *fakeTikTok) hits() int64 { return f.hitsN.Load() }

// handle fixes the response for one path.
func (f *fakeTikTok) handle(path string, r fakeResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[path] = r
}

// queue serves responses in order, repeating the last one when exhausted.
func (f *fakeTikTok) queue(path string, responses ...fakeResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queues[path] = append([]fakeResponse{}, responses...)
}

// requests returns a copy of the request log in arrival order.
func (f *fakeTikTok) requests() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedRequest, len(f.log))
	copy(out, f.log)
	return out
}

// lastRequest returns the most recent request and fails when none was seen.
func (f *fakeTikTok) lastRequest(t *testing.T) recordedRequest {
	t.Helper()
	reqs := f.requests()
	if len(reqs) == 0 {
		t.Fatal("fake received no requests")
	}
	return reqs[len(reqs)-1]
}

// serve records the request, then writes the scripted reply.
func (f *fakeTikTok) serve(w http.ResponseWriter, r *http.Request) {
	f.hitsN.Add(1)
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.log = append(f.log, recordedRequest{
		method:  r.Method,
		path:    r.URL.Path,
		query:   r.URL.Query(),
		headers: r.Header.Clone(),
		body:    string(raw),
	})
	resp, ok := f.next(r.URL.Path)
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	for key, value := range resp.headers {
		w.Header().Set(key, value)
	}
	status := resp.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(resp.body))
}

// next picks the queued response, then the fixed handler, else no match.
func (f *fakeTikTok) next(path string) (fakeResponse, bool) {
	if q := f.queues[path]; len(q) > 0 {
		resp := q[0]
		if len(q) > 1 {
			f.queues[path] = q[1:]
		}
		return resp, true
	}
	if resp, ok := f.handlers[path]; ok {
		return resp, true
	}
	return fakeResponse{}, false
}

// successEnvelope wraps raw JSON in the data member of a success body.
func successEnvelope(data string) fakeResponse {
	return fakeResponse{status: http.StatusOK, body: `{"data":` + data + `}`}
}

// wireErrorResponse builds a TikTok error body with the given status and log id.
func wireErrorResponse(status int, code, message, logID string) fakeResponse {
	return fakeResponse{
		status: status,
		body:   fmt.Sprintf(`{"error":{"code":%q,"message":%q,"log_id":%q}}`, code, message, logID),
	}
}

// malformedResponse builds a body that is not valid JSON.
func malformedResponse(status int) fakeResponse {
	return fakeResponse{status: status, body: `{"data":`}
}

// newTestClient points a client at the fake server.
func newTestClient(t *testing.T, f *fakeTikTok) *Client {
	t.Helper()
	return New(Config{
		ClientKey:    "test-key",
		ClientSecret: "test-secret",
		RedirectURI:  "https://example.test/callback",
		BaseURL:      f.URL(),
	})
}

// Token endpoint paths the fake routes.
const (
	tokenPath  = "/v2/oauth/token/"
	revokePath = "/v2/oauth/revoke/"
	userPath   = "/v2/user/info/"
)

// wantError asserts a typed client error code and optional upstream log id.
func wantError(t *testing.T, err error, code apierror.Code, logID string) {
	t.Helper()
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not *apierror.Error", err)
	}
	if apiErr.Code != code {
		t.Fatalf("code = %s, want %s", apiErr.Code, code)
	}
	if logID != "" && apiErr.LogID != logID {
		t.Fatalf("log id = %q, want %q", apiErr.LogID, logID)
	}
}

// wantHits fails unless the fake handled exactly want requests.
func wantHits(t *testing.T, f *fakeTikTok, want int64) {
	t.Helper()
	if got := f.hits(); got != want {
		t.Fatalf("server hits = %d, want %d", got, want)
	}
}

// parseForm parses the form body of the most recent request.
func parseForm(t *testing.T, f *fakeTikTok) url.Values {
	t.Helper()
	form, err := url.ParseQuery(f.lastRequest(t).body)
	if err != nil {
		t.Fatalf("parse form: %v", err)
	}
	return form
}

// assertForm checks the wanted fields are present with exact values.
func assertForm(t *testing.T, form url.Values, want map[string]string) {
	t.Helper()
	for key, value := range want {
		if got := form.Get(key); got != value {
			t.Fatalf("form %s = %q, want %q", key, got, value)
		}
	}
}
