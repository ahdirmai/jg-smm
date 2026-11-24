package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/oapi-codegen/nullable"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/http/oapigen"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
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

// Register mounts both route sets. The group in router.go applies auth + the
// `read` floor; mutating routes add `act` (P5-04).
func (h *AnalyticsHandler) Register(g *echo.Group) {
	g.POST("/official-accounts", h.createOfficialAccount, RequirePermission(domain.PermAct))
	g.GET("/official-accounts", h.listOfficialAccounts)
	g.DELETE("/official-accounts/:officialAccountId", h.archiveOfficialAccount, RequirePermission(domain.PermAct))
	// Static routes must be registered before the /analytics/:platform param
	// route, otherwise Echo matches "refresh" as a platform name.
	g.POST("/analytics/refresh", h.analyticsRefresh, RequirePermission(domain.PermAct))
	g.GET("/analytics/overview", h.analyticsOverview)
	g.GET("/analytics/:platform", h.analyticsByPlatform)
	g.GET("/analytics/:platform/content", h.analyticsContentByPlatform)
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
		ProviderRef: derefStr(req.ProviderRef),
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
		Breakdown: toBreakdownDTOs(res.Breakdown),
	})
}

func (h *AnalyticsHandler) analyticsContentByPlatform(c echo.Context) error {
	if h.analytics == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "analytics service unavailable")
	}
	p := domain.Platform(c.Param("platform"))
	if !p.Valid() {
		return echo.NewHTTPError(http.StatusBadRequest, "unknown platform")
	}
	limit := 50
	if v := strings.TrimSpace(c.QueryParam("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "limit must be a positive integer")
		}
		limit = n
	}
	rows, err := h.analytics.Content(c.Request().Context(), p, c.QueryParam("accountId"), windowDays(c), limit)
	if err != nil {
		if errors.Is(err, domain.ErrValidation) {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, oapigen.ContentMetricsResponse{
		Platform: oapigen.Platform(p),
		Items:    toContentMetricDTOs(rows),
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
		AvatarUrl:     nullableFromPtr(a.AvatarURL),
		DisplayName:   nullableFromPtr(a.DisplayName),
		ProfileUrl:    nullableFromPtr(a.ProfileURL),
		LastFetchedAt: nullableFromPtr(a.LastFetchedAt),
	}
}

func toKpiDTOs(kpis []service.KPI) []oapigen.AnalyticsKpi {
	out := make([]oapigen.AnalyticsKpi, 0, len(kpis))
	for _, k := range kpis {
		out = append(out, oapigen.AnalyticsKpi{
			OfficialAccountId: k.OfficialAccountID,
			Handle:            &k.Handle,
			Metric:            string(k.Metric),
			Value:             nullableFromPtr(k.Value),
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
		LastRunStatus:    nullableFromPtr(f.LastRunStatus),
		LastRunAt:        nullableFromPtr(f.LastRunAt),
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
		FinishedAt:  nullableFromPtr(r.FinishedAt),
		AccountsOk:  ptr(r.AccountsOk),
		AccountsErr: ptr(r.AccountsErr),
		ErrorClass:  nullableFromPtr(r.ErrorClass),
		Error:       nullableFromPtr(r.Error),
	}
}

// --- helpers ------------------------------------------------------------------

// nullableFromPtr maps a pointer to the oapi-codegen Nullable wrapper the
// generated models use for optional fields, so a nil pointer round-trips to
// JSON null instead of being omitted or coerced to a zero value. One generic
// rule, applied to every nullable DTO field, keeps the four scalar shapes from
// drifting apart.
func nullableFromPtr[T any](p *T) nullable.Nullable[T] {
	if p == nil {
		return nullable.NewNullNullable[T]()
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

func toContentMetricDTOs(rows []service.ContentMetric) []oapigen.ContentMetric {
	out := make([]oapigen.ContentMetric, 0, len(rows))
	for _, r := range rows {
		out = append(out, oapigen.ContentMetric{
			Id:                r.ID,
			AccountHandle:     r.AccountHandle,
			ExternalId:        r.ExternalID,
			ContentType:       nullableFromPtr(r.ContentType),
			Caption:           nullableFromPtr(r.Caption),
			Permalink:         nullableFromPtr(r.Permalink),
			PublishedAt:       nullableFromPtr(r.PublishedAt),
			Reach:             nullableFromPtr(r.Reach),
			Views:             nullableFromPtr(r.Views),
			Interactions:      nullableFromPtr(r.Interactions),
			Likes:             nullableFromPtr(r.Likes),
			Comments:          nullableFromPtr(r.Comments),
			Saves:             nullableFromPtr(r.Saves),
			Shares:            nullableFromPtr(r.Shares),
			EngagementRatePct: nullableFromPtr(r.EngagementRatePct),
		})
	}
	return out
}

func toBreakdownDTOs(rows []service.EngagementBreakdown) []oapigen.EngagementBreakdown {
	out := make([]oapigen.EngagementBreakdown, 0, len(rows))
	for _, r := range rows {
		out = append(out, oapigen.EngagementBreakdown{
			OfficialAccountId: r.OfficialAccountID,
			Handle:            r.Handle,
			Likes:             nullableFromPtr(r.Likes),
			Comments:          nullableFromPtr(r.Comments),
			Saves:             nullableFromPtr(r.Saves),
			Shares:            nullableFromPtr(r.Shares),
			AccountsEngaged:   nullableFromPtr(r.AccountsEngaged),
			EngagementRatePct: nullableFromPtr(r.EngagementRatePct),
		})
	}
	return out
}
