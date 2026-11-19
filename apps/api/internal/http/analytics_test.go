package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/http/oapigen"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
)

// The handler tests use the service package's in-memory analytics store so the
// DTO mapping and the freshness badge are verified without a database.
var _ = domain.AnalyticsProviderA

// DTO aliases to keep the table below readable.
type (
	oapigenOfficialAccount     = oapigen.OfficialAccount
	oapigenOfficialAccountList = oapigen.OfficialAccountList
	oapigenPlatformAnalytics   = oapigen.PlatformAnalytics
	oapigenAnalyticsIngestRun  = oapigen.AnalyticsIngestRun
)

// newAnalyticsEcho mounts just the analytics routes with the claims an
// authenticated operator would carry, so the handler contract can be tested
// directly. RBAC itself is covered by the group tests (TestContainerRBAC);
// mutating routes still pass through the real RequirePermission(PermAct)
// middleware, so this does not silently bypass it.
func newAnalyticsEcho(h *AnalyticsHandler) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	g := e.Group("/api", func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(ctxKeyClaims, port.Claims{UserID: "u1", Role: domain.RoleOperator})
			return next(c)
		}
	})
	h.Register(g)
	return e
}

func TestAnalyticsHandlerCreateAndList(t *testing.T) {
	store := newFakeAnalyticsStore()
	svc := service.NewAnalyticsService(store, service.AnalyticsConfig{Clock: time.Now})
	h := NewAnalyticsHandler(svc)
	e := newAnalyticsEcho(h)

	body := `{"platform":"instagram","handle":"brand","displayName":"Brand","tags":["client"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/official-accounts", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d, body %s", rec.Code, rec.Body.String())
	}
	var got oapigenOfficialAccount
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if got.Handle != "brand" || got.Platform != "instagram" {
		t.Fatalf("create payload wrong: %+v", got)
	}
	if string(got.Status) != "active" {
		t.Fatalf("default status should be active: %s", got.Status)
	}
	if got.Stale == nil || !*got.Stale {
		t.Fatal("never-fetched account must be stale")
	}

	// List returns the created account.
	req = httptest.NewRequest(http.MethodGet, "/api/official-accounts", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	var list oapigenOfficialAccountList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.OfficialAccounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(list.OfficialAccounts))
	}

	// Duplicate (platform, handle) is a conflict.
	req = httptest.NewRequest(http.MethodPost, "/api/official-accounts", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate should 409, got %d", rec.Code)
	}
}

func TestAnalyticsHandlerCreateValidation(t *testing.T) {
	store := newFakeAnalyticsStore()
	svc := service.NewAnalyticsService(store, service.AnalyticsConfig{Clock: time.Now})
	e := newAnalyticsEcho(NewAnalyticsHandler(svc))

	cases := []struct{ name, body string }{
		{"missing handle", `{"platform":"instagram"}`},
		{"unknown platform", `{"platform":"myspace","handle":"brand"}`},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, "/api/official-accounts", strings.NewReader(tc.body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d (%s)", tc.name, rec.Code, rec.Body.String())
		}
	}
}

func TestAnalyticsHandlerArchive(t *testing.T) {
	store := newFakeAnalyticsStore()
	acc := seedOfficial(store, "brand-archive")
	svc := service.NewAnalyticsService(store, service.AnalyticsConfig{Clock: time.Now})
	e := newAnalyticsEcho(NewAnalyticsHandler(svc))

	req := httptest.NewRequest(http.MethodDelete, "/api/official-accounts/"+acc.ID, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive: %d", rec.Code)
	}
	var got oapigenOfficialAccount
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(got.Status) != "archived" {
		t.Fatalf("status should be archived: %s", got.Status)
	}

	// Unknown id is a 404.
	req = httptest.NewRequest(http.MethodDelete, "/api/official-accounts/does-not-exist", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing account should 404, got %d", rec.Code)
	}
}

func TestAnalyticsHandlerPlatformPage(t *testing.T) {
	store := newFakeAnalyticsStore()
	acc := seedOfficial(store, "brand-trend")
	// Seed a snapshot so the trend series has something to chart.
	store.UpsertAnalyticsSnapshot(context.Background(), domain.AnalyticsSnapshot{
		OfficialAccountID: acc.ID,
		Platform:          acc.Platform,
		TS:                time.Now().UTC(),
		Followers:         ptr(int64(1500)),
		Provider:          domain.AnalyticsProviderA,
	})

	svc := service.NewAnalyticsService(store, service.AnalyticsConfig{Clock: time.Now})
	e := newAnalyticsEcho(NewAnalyticsHandler(svc))

	req := httptest.NewRequest(http.MethodGet, "/api/analytics/instagram?metric=followers&windowDays=30", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("platform page: %d %s", rec.Code, rec.Body.String())
	}
	var got oapigenPlatformAnalytics
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Platform != "instagram" {
		t.Fatalf("platform wrong: %s", got.Platform)
	}
	if len(got.Trend) == 0 {
		t.Fatal("trend should not be empty")
	}

	// Unknown platform is a 400.
	req = httptest.NewRequest(http.MethodGet, "/api/analytics/myspace", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown platform should 400, got %d", rec.Code)
	}
}

func TestAnalyticsHandlerRefresh(t *testing.T) {
	store := newFakeAnalyticsStore()
	seedOfficial(store, "brand-refresh")
	ingestor := service.NewAnalyticsIngestor(store, &httpProvider{}, service.AnalyticsIngestorConfig{})
	svc := service.NewAnalyticsService(store, service.AnalyticsConfig{
		Ingestor: ingestor,
		Clock:    time.Now,
	})
	e := newAnalyticsEcho(NewAnalyticsHandler(svc))

	req := httptest.NewRequest(http.MethodPost, "/api/analytics/refresh", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh: %d %s", rec.Code, rec.Body.String())
	}
	var got oapigenAnalyticsIngestRun
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "success" {
		t.Fatalf("empty-provider run should be success, got %s", got.Status)
	}
}

func TestAnalyticsHandlerUnavailableWithoutService(t *testing.T) {
	e := newAnalyticsEcho(NewAnalyticsHandler(nil))
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/official-accounts"},
		{http.MethodPost, "/api/analytics/refresh"},
		{http.MethodGet, "/api/analytics/overview"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: expected 503, got %d", tc.method, tc.path, rec.Code)
		}
	}
}

// A breakdown part the provider never reported must arrive as JSON null, not 0.
// The dashboard renders null as "—"; a 0 would assert "nobody saved anything"
// about an account whose saves are simply not visible.
func TestAnalyticsHandlerBreakdownKeepsAbsentPartsNull(t *testing.T) {
	store := newFakeAnalyticsStore()
	acc := seedOfficial(store, "brand-trend")
	store.UpsertAnalyticsSnapshot(context.Background(), domain.AnalyticsSnapshot{
		OfficialAccountID: acc.ID,
		Platform:          acc.Platform,
		TS:                time.Now().UTC(),
		Reach:             ptr(int64(100)),
		Engagements:       ptr(int64(10)),
		// Instagram reports likes/comments here but says nothing about saves.
		Metrics:  json.RawMessage(`{"instagram":{"likes":7,"comments":3,"shares":0}}`),
		Provider: domain.AnalyticsProviderA,
	})

	svc := service.NewAnalyticsService(store, service.AnalyticsConfig{Clock: time.Now})
	e := newAnalyticsEcho(NewAnalyticsHandler(svc))
	req := httptest.NewRequest(http.MethodGet, "/api/analytics/instagram?windowDays=30", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("platform page: %d %s", rec.Code, rec.Body.String())
	}

	var raw struct {
		Breakdown []struct {
			Handle            string           `json:"handle"`
			Likes             *json.Number     `json:"likes"`
			Comments          *json.Number     `json:"comments"`
			Saves             *json.Number     `json:"saves"`
			Shares            *json.Number     `json:"shares"`
			EngagementRatePct *json.RawMessage `json:"engagementRatePct"`
		} `json:"breakdown"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw.Breakdown) != 1 {
		t.Fatalf("want 1 breakdown row, got %d", len(raw.Breakdown))
	}
	b := raw.Breakdown[0]
	if b.Likes == nil || b.Likes.String() != "7" {
		t.Errorf("likes: want 7, got %v", b.Likes)
	}
	if b.Comments == nil || b.Comments.String() != "3" {
		t.Errorf("comments: want 3, got %v", b.Comments)
	}
	// A reported zero is a measurement and must survive as 0, not become null.
	if b.Shares == nil || b.Shares.String() != "0" {
		t.Errorf("shares: want a reported 0, got %v", b.Shares)
	}
	// An unreported part must be null. This is the whole point of the type.
	if b.Saves != nil {
		t.Errorf("saves: want null for an unreported part, got %v", *b.Saves)
	}
	if b.EngagementRatePct == nil || string(*b.EngagementRatePct) == "null" {
		t.Errorf("engagement rate: want 10%%, got %s", string(*b.EngagementRatePct))
	}

	// A nil pointer must produce a present JSON key with null, not an omitted
	// key. Decoding through *json.Number cannot tell those apart.
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode raw envelope: %v", err)
	}
	var rawBreakdown []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["breakdown"], &rawBreakdown); err != nil {
		t.Fatalf("decode raw breakdown: %v", err)
	}
	for _, key := range []string{"saves", "shares", "likes", "comments", "engagementRatePct"} {
		if _, ok := rawBreakdown[0][key]; !ok {
			t.Errorf("breakdown key %q is missing from JSON", key)
		}
	}
	if string(rawBreakdown[0]["saves"]) != "null" {
		t.Errorf("saves raw = %s, want null", rawBreakdown[0]["saves"])
	}
	if string(rawBreakdown[0]["shares"]) != "0" {
		t.Errorf("shares raw = %s, want 0", rawBreakdown[0]["shares"])
	}
}

func TestAnalyticsContentRejectsBadLimit(t *testing.T) {
	store := newFakeAnalyticsStore()
	seedOfficial(store, "brand-trend")
	svc := service.NewAnalyticsService(store, service.AnalyticsConfig{Clock: time.Now})
	e := newAnalyticsEcho(NewAnalyticsHandler(svc))

	for _, q := range []string{"limit=0", "limit=-3", "limit=abc"} {
		req := httptest.NewRequest(http.MethodGet, "/api/analytics/instagram/content?"+q, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", q, rec.Code)
		}
	}
}

// The content endpoint must not be shadowed by the /analytics/:platform route:
// "content" is a path segment, not a platform name, and matching it as one
// would answer every content request with an unknown-platform 400.
func TestAnalyticsContentRouteNotShadowedByPlatformParam(t *testing.T) {
	store := newFakeAnalyticsStore()
	acc := seedOfficial(store, "brand-trend")
	store.UpsertContentMetric(context.Background(), domain.ContentMetric{
		OfficialAccountID: acc.ID,
		Platform:          acc.Platform,
		ExternalID:        "17900000000000001",
		Provider:          domain.AnalyticsProviderA,
		Reach:             ptr(int64(500)),
	})

	svc := service.NewAnalyticsService(store, service.AnalyticsConfig{Clock: time.Now})
	e := newAnalyticsEcho(NewAnalyticsHandler(svc))
	req := httptest.NewRequest(http.MethodGet, "/api/analytics/instagram/content?windowDays=30", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("content route: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Platform string `json:"platform"`
		Items    []struct {
			ExternalID    string       `json:"externalId"`
			AccountHandle string       `json:"accountHandle"`
			Reach         *json.Number `json:"reach"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Platform != "instagram" {
		t.Fatalf("platform wrong: %s", got.Platform)
	}
	if len(got.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(got.Items))
	}
	if got.Items[0].Reach == nil || got.Items[0].Reach.String() != "500" {
		t.Errorf("reach: want 500, got %v", got.Items[0].Reach)
	}
	if got.Items[0].AccountHandle != "brand-trend" {
		t.Errorf("account handle: want brand-trend, got %q", got.Items[0].AccountHandle)
	}
}
