// Package insight covers the insight HTTP surface.
//
// @file      internal/insight/handler_test.go
// @for       Route tests for list, query, video, stats, refresh, and profile.
// @uses      net/http/httptest, encoding/json, testing, gin
// @reason    Pins the envelope shape, the session guard, and the absent key rule.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package insight

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// harness wires the insight routes over fakes.
type harness struct {
	router *gin.Engine
	svc    *Service
	tiktok *fakeTikTok
	store  *fakeStore
}

// newHarness builds the router with one signed in operator.
func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWithSession(t, func(*gin.Context) (string, bool) { return testOperator, true })
}

// newHarnessWithSession builds the router with the given session resolver.
func newHarnessWithSession(t *testing.T, resolve SessionAccount) *harness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &harness{tiktok: &fakeTikTok{}, store: &fakeStore{}}
	h.svc = NewService(Config{
		Videos:   h.tiktok,
		Accounts: &fakeAccounts{access: "access-token"},
		Store:    h.store,
		Now:      func() time.Time { return time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC) },
	})
	engine := gin.New()
	RegisterRoutes(engine, NewHandler(h.svc, resolve))
	h.router = engine
	return h
}

// do runs one request through the router.
func (h *harness) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

// decodeData pulls the envelope data member out of a recorded response.
func decodeData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	return envelope.Data
}

// errorCode pulls the error code out of a recorded problem response.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	return problem.Code
}

// errorDetail pulls the error detail out of a recorded problem response.
func errorDetail(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var problem struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	return problem.Detail
}

func TestListRouteReturnsEnvelope(t *testing.T) {
	h := newHarness(t)
	h.tiktok.page = tiktokclient.VideoPage{
		Videos: []tiktokclient.Video{{ID: testVideo, LikeCount: count(3)}},
		Cursor: 42, HasMore: true,
	}
	rec := h.do(t, "GET", "/videos?account_id="+testAccount+"&max_count=5", "")
	if rec.Code != 200 {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	data := decodeData(t, rec)
	if data["cursor"] != "42" || data["has_more"] != true {
		t.Fatalf("data = %+v", data)
	}
	videos, ok := data["videos"].([]any)
	if !ok || len(videos) != 1 {
		t.Fatalf("videos = %+v", data["videos"])
	}
}

func TestListRouteWithoutSessionAnswers401(t *testing.T) {
	h := newHarnessWithSession(t, func(*gin.Context) (string, bool) { return "", false })
	rec := h.do(t, "GET", "/videos?account_id="+testAccount, "")
	if rec.Code != 401 || errorCode(t, rec) != string(apierror.CodeUnauthorized) {
		t.Fatalf("status = %d code = %s", rec.Code, errorCode(t, rec))
	}
}

func TestQueryRouteRejectsMoreThan20IDs(t *testing.T) {
	h := newHarness(t)
	ids := make([]string, 21)
	for i := range ids {
		ids[i] = testVideo
	}
	body := `{"account_id":"` + testAccount + `","video_ids":` + joinIDs(ids) + `}`
	rec := h.do(t, "POST", "/videos/query", body)
	if rec.Code != 422 || errorCode(t, rec) != string(apierror.CodeValidationFailed) {
		t.Fatalf("status = %d code = %s", rec.Code, errorCode(t, rec))
	}
}

func TestQueryRouteRejectsEmptyIDList(t *testing.T) {
	h := newHarness(t)
	rec := h.do(t, "POST", "/videos/query", `{"account_id":"`+testAccount+`","video_ids":[]}`)
	if rec.Code != 422 {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestQueryRouteRejectsMalformedBody(t *testing.T) {
	h := newHarness(t)
	rec := h.do(t, "POST", "/videos/query", `{"account_id":`)
	if rec.Code != 422 {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestQueryRouteOmitsAbsentCountKey(t *testing.T) {
	h := newHarness(t)
	h.tiktok.videos = []tiktokclient.Video{{ID: testVideo, ViewCount: count(7)}}
	body := `{"account_id":"` + testAccount + `","video_ids":["` + testVideo + `"]}`
	rec := h.do(t, "POST", "/videos/query", body)
	if rec.Code != 200 {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "like_count") {
		t.Fatalf("body carries like_count: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "view_count") {
		t.Fatalf("body misses view_count: %s", rec.Body.String())
	}
}

func TestVideoRouteAnswers404WhenUpstreamOmitsTheID(t *testing.T) {
	h := newHarness(t)
	h.tiktok.videos = []tiktokclient.Video{{ID: "other"}}
	rec := h.do(t, "GET", "/videos/"+testVideo+"?account_id="+testAccount, "")
	if rec.Code != 404 || errorCode(t, rec) != string(apierror.CodeNotFound) {
		t.Fatalf("status = %d code = %s", rec.Code, errorCode(t, rec))
	}
}

func TestRefreshRouteAppendsEveryTime(t *testing.T) {
	h := newHarness(t)
	h.tiktok.videos = []tiktokclient.Video{{ID: testVideo, ViewCount: count(11)}}
	path := "/videos/" + testVideo + "/stats/refresh?account_id=" + testAccount
	h.do(t, "POST", path, "")
	h.do(t, "POST", path, "")
	if h.store.writes != 2 || len(h.store.stats) != 2 {
		t.Fatalf("writes = %d rows = %d", h.store.writes, len(h.store.stats))
	}
}

func TestProfileRouteMapsCounts(t *testing.T) {
	h := newHarness(t)
	h.tiktok.info = tiktokclient.UserInfo{OpenID: "open-1", DisplayName: "Ada", FollowerCount: int64Ptr(5)}
	h.tiktok.creator = tiktokclient.CreatorInfo{PrivacyLevelOptions: []string{"PUBLIC"}}
	rec := h.do(t, "GET", "/accounts/"+testAccount+"/profile", "")
	if rec.Code != 200 {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	data := decodeData(t, rec)
	if data["follower_count"].(float64) != 5 || data["display_name"] != "Ada" {
		t.Fatalf("data = %+v", data)
	}
	if _, present := data["following_count"]; present {
		t.Fatalf("following_count is present: %+v", data)
	}
}

// int64Ptr returns a pointer to a count value for the route fixtures below.
func int64Ptr(value int64) *int64 { return &value }

func TestProfileRouteRejectsMalformedAccountID(t *testing.T) {
	h := newHarness(t)
	rec := h.do(t, "GET", "/accounts/not-a-uuid/profile", "")
	if rec.Code == 200 {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

// joinIDs renders the id slice as a JSON array for the query body fixtures.
func joinIDs(ids []string) string {
	return "[" + `"` + strings.Join(ids, `","`) + `"]`
}
