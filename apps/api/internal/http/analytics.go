package http

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/oapi-codegen/nullable"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/http/oapigen"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/service"
)

// AnalyticsHandler mounts the official-accounts and analytics read APIs
// (P2-13 / P2-14). RBAC is enforced by the route group: analytics reads need
// the `read` permission (analyst+), writes need `act` (operator+).
type AnalyticsHandler struct {
	analytics *service.AnalyticsService
}

// NewAnalyticsHandler builds the handler. A nil service makes the routes
// return 503, keeping the API bootable before the DB is wired.
func NewAnalyticsHandler(svc *service.AnalyticsService) *AnalyticsHandler {
	return &AnalyticsHandler{analytics: svc}
}

// Register mounts both route sets. The group in router.go already applies auth.
func (h *AnalyticsHandler) Register(g *echo.Group) {
	g.POST("/official-accounts", h.createOfficialAccount)
	g.GET("/official-accounts", h.listOfficialAccounts)
	g.DELETE("/official-accounts/:officialAccountId", h.archiveOfficialAccount)
	// Static routes must be registered before the /analytics/:platform param
	// route, otherwise Echo matches "refresh" as a platform name.
	g.POST("/analytics/refresh", h.analyticsRefresh)
	g.GET("/analytics/overview", h.analyticsOverview)
	g.GET("/analytics/:platform", h.analyticsByPlatform)
}

func (h *AnalyticsHandler) createOfficialAccount(c echo.Context) error {
	var req oapigen.CreateOfficialAccountRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if h.analytics == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "analytics service unavailable")
	}
	tags := []string{}
	if req.Tags != nil {
		tags = *req.Tags
	}
	acc, err := h.analytics.Create(c.Request().Context(), service.OfficialAccountInput{
		Platform:    domain.Platform(req.Platform),
		Handle:      req.Handle,
		DisplayName: derefStr(req.DisplayName),
		ProfileURL:  derefStr(req.ProfileUrl),
		AvatarURL:   derefStr(req.AvatarUrl),
		Provider:    derefProvider(req.Provider),
		Tags:        tags,
	})
	if errors.Is(err, domain.ErrValidation) {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if errors.Is(err, domain.ErrConflict) {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusCreated, toOfficialAccountDTO(acc))
}

func (h *AnalyticsHandler) listOfficialAccounts(c echo.Context) error {
	if h.analytics == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "analytics service unavailable")
	}
	var platform *domain.Platform
	if p := strings.TrimSpace(c.QueryParam("platform")); p != "" {
		dp := domain.Platform(p)
		if !dp.Valid() {
			return echo.NewHTTPError(http.StatusBadRequest, "unknown platform")
		}
		platform = &dp
	}
	accs, err := h.analytics.List(c.Request().Context(), platform)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	out := make([]oapigen.OfficialAccount, 0, len(accs))
	for _, a := range accs {
		out = append(out, toOfficialAccountDTO(a))
	}
	return c.JSON(http.StatusOK, oapigen.OfficialAccountList{OfficialAccounts: out})
}

func (h *AnalyticsHandler) archiveOfficialAccount(c echo.Context) error {
	if h.analytics == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "analytics service unavailable")
	}
	acc, err := h.analytics.Archive(c.Request().Context(), c.Param("officialAccountId"))
	if errors.Is(err, domain.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "official account not found")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, toOfficialAccountDTO(acc))
}

func (h *AnalyticsHandler) analyticsOverview(c echo.Context) error {
	if h.analytics == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "analytics service unavailable")
	}
	overview, err := h.analytics.Overview(c.Request().Context(), windowDays(c))
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, oapigen.AnalyticsOverview{
		Kpis:      toKpiDTOs(overview.KPIs),
		Freshness: toFreshnessDTO(overview.Freshness),
	})
}

func (h *AnalyticsHandler) analyticsByPlatform(c echo.Context) error {
	if h.analytics == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "analytics service unavailable")
	}
	p := domain.Platform(c.Param("platform"))
	if !p.Valid() {
		return echo.NewHTTPError(http.StatusBadRequest, "unknown platform")
	}
	metric := strings.TrimSpace(c.QueryParam("metric"))
	res, err := h.analytics.PlatformPage(c.Request().Context(), p, metric, windowDays(c))
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, oapigen.PlatformAnalytics{
		Platform:  oapigen.Platform(res.Platform),
		Kpis:      toKpiDTOs(res.KPIs),
		Trend:     toTrendDTOs(res.Trend),
		Freshness: toFreshnessDTO(res.Freshness),
	})
}

func (h *AnalyticsHandler) analyticsRefresh(c echo.Context) error {
	if h.analytics == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "analytics service unavailable")
	}
	run, err := h.analytics.Refresh(c.Request().Context())
	if errors.Is(err, domain.ErrUnavailable) {
		return echo.NewHTTPError(http.StatusServiceUnavailable, err.Error())
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, toIngestRunDTO(run))
}

// --- DTO converters -----------------------------------------------------------

func toOfficialAccountDTO(a domain.OfficialAccount) oapigen.OfficialAccount {
	status := oapigen.OfficialAccountStatus(a.Status)
	return oapigen.OfficialAccount{
		Id:            a.ID,
		Platform:      oapigen.Platform(a.Platform),
		Handle:        a.Handle,
		Status:        status,
		Provider:      string(a.Provider),
		Tags:          &a.Tags,
		Stale:         ptr(a.IsStale(time.Now())),
		CreatedAt:     ptr(a.CreatedAt),
		AvatarUrl:     nullStr(a.AvatarURL),
		DisplayName:   nullStr(a.DisplayName),
		ProfileUrl:    nullStr(a.ProfileURL),
		LastFetchedAt: nullTime(a.LastFetchedAt),
	}
}

func toKpiDTOs(kpis []service.KPI) []oapigen.AnalyticsKpi {
	out := make([]oapigen.AnalyticsKpi, 0, len(kpis))
	for _, k := range kpis {
		out = append(out, oapigen.AnalyticsKpi{
			OfficialAccountId: k.OfficialAccountID,
			Handle:            &k.Handle,
			Metric:            string(k.Metric),
			Value:             nullInt64(k.Value),
		})
	}
	return out
}

func toTrendDTOs(points []domain.TrendPoint) []oapigen.TrendPoint {
	out := make([]oapigen.TrendPoint, 0, len(points))
	for _, p := range points {
		out = append(out, oapigen.TrendPoint{
			Bucket: p.Bucket,
			Value:  p.Value,
		})
	}
	return out
}

func toFreshnessDTO(f service.Freshness) oapigen.AnalyticsFreshness {
	return oapigen.AnalyticsFreshness{
		LastRunStatus:    nullStr(f.LastRunStatus),
		LastRunAt:        nullTime(f.LastRunAt),
		Stale:            f.Stale,
		ThresholdSeconds: ptr(f.ThresholdSeconds),
	}
}

func toIngestRunDTO(r domain.AnalyticsIngestRun) oapigen.AnalyticsIngestRun {
	return oapigen.AnalyticsIngestRun{
		Id:          r.ID,
		Provider:    string(r.Provider),
		Scope:       r.Scope,
		Status:      string(r.Status),
		StartedAt:   r.StartedAt,
		FinishedAt:  nullTime(r.FinishedAt),
		AccountsOk:  ptr(r.AccountsOk),
		AccountsErr: ptr(r.AccountsErr),
		ErrorClass:  nullStr(r.ErrorClass),
		Error:       nullStr(r.Error),
	}
}

// --- helpers ------------------------------------------------------------------

// nullStr / nullTime / nullInt64 map *T to the oapi-codegen Nullable wrapper
// the generated models use for optional fields, so a nil pointer round-trips
// to JSON null instead of being omitted or coerced to a zero value.
func nullStr(p *string) nullable.Nullable[string] {
	if p == nil {
		return nullable.NewNullNullable[string]()
	}
	return nullable.NewNullableWithValue(*p)
}

func nullTime(p *time.Time) nullable.Nullable[time.Time] {
	if p == nil {
		return nullable.NewNullNullable[time.Time]()
	}
	return nullable.NewNullableWithValue(*p)
}

func nullInt64(p *int64) nullable.Nullable[int64] {
	if p == nil {
		return nullable.NewNullNullable[int64]()
	}
	return nullable.NewNullableWithValue(*p)
}

// --- helpers ------------------------------------------------------------------

func windowDays(c echo.Context) int {
	raw := strings.TrimSpace(c.QueryParam("windowDays"))
	n := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 30
		}
		n = n*10 + int(ch-'0')
	}
	if n <= 0 {
		return 30
	}
	return n
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func ptr[T any](v T) *T { return &v }

func derefProvider(p *string) domain.AnalyticsProvider {
	if p == nil || *p == "" {
		return domain.AnalyticsProviderA
	}
	return domain.AnalyticsProvider(*p)
}
