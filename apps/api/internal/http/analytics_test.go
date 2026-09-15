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

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/http/oapigen"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/service"
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

// newAnalyticsEcho mounts just the analytics routes with no auth middleware so
// the handler contract can be tested directly (RBAC is covered by the group).
func newAnalyticsEcho(h *AnalyticsHandler) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	g := e.Group("/api")
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
