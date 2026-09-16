package http

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	openapiTypes "github.com/oapi-codegen/runtime/types"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/http/oapigen"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/service"
)

// ReportHandler mounts the report builder + export (P4-04 / P4-05). Reads need
// `read` (the group gate); the export route additionally requires `export`.
type ReportHandler struct {
	reports *service.ReportService
}

func NewReportHandler(svc *service.ReportService) *ReportHandler {
	return &ReportHandler{reports: svc}
}

func (h *ReportHandler) Register(g *echo.Group) {
	g.GET("/reports/actions", h.reportActions)
	g.GET("/reports/targets", h.reportTargets)
	g.GET("/reports/analytics", h.reportAnalytics)
	// The file export is a distinct permission (analyst+), so it gets its own
	// gate rather than riding the group's `act`.
	g.GET("/reports/export", h.reportExport, RequirePermission(domain.PermExport))
}

func (h *ReportHandler) reportActions(c echo.Context) error {
	var params oapigen.ReportActionsParams
	if err := c.Bind(&params); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid query parameters")
	}
	f := filterFrom(params.From, params.To, params.Platform, params.AccountId, nil)
	rows, series, err := h.reports.ActionReport(c.Request().Context(), f)
	if err != nil {
		return err
	}
	out := make([]oapigen.ActionReportRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, oapigen.ActionReportRow{
			Day:        openapiTypes.Date{Time: r.Day},
			AccountId:  r.AccountID,
			Username:   r.Username,
			Platform:   oapigen.Platform(r.Platform),
			ActionType: oapigen.ActionReportRowActionType(r.ActionType),
			Total:      int(r.Total),
			Succeeded:  int(r.Succeeded),
			Failed:     int(r.Failed),
		})
	}
	return c.JSON(http.StatusOK, oapigen.ActionReport{
		Rows:   out,
		Series: toTrendDTOs(series),
	})
}

func (h *ReportHandler) reportTargets(c echo.Context) error {
	var params oapigen.ReportTargetsParams
	if err := c.Bind(&params); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid query parameters")
	}
	f := filterFrom(params.From, params.To, params.Platform, nil, nil)
	rows, err := h.reports.TargetReport(c.Request().Context(), f)
	if err != nil {
		return err
	}
	out := make([]oapigen.TargetReportRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, oapigen.TargetReportRow{
			Id:        r.ID,
			Url:       r.URL,
			Platform:  oapigen.Platform(r.Platform),
			Total:     int(r.Total),
			Succeeded: int(r.Succeeded),
			Failed:    int(r.Failed),
		})
	}
	return c.JSON(http.StatusOK, oapigen.TargetReport{Rows: out})
}

func (h *ReportHandler) reportAnalytics(c echo.Context) error {
	var params oapigen.ReportAnalyticsParams
	if err := c.Bind(&params); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid query parameters")
	}
	metric := string(domain.AnalyticsMetricFollowers)
	if params.Metric != nil {
		metric = *params.Metric
	}
	f := filterFrom(params.From, params.To, nil, &params.AccountId, &metric)
	got, series, err := h.reports.AnalyticsReport(c.Request().Context(), f)
	if err != nil {
		return mapReportErr(err)
	}
	return c.JSON(http.StatusOK, oapigen.AnalyticsReport{
		AccountId: params.AccountId,
		Metric:    got,
		Series:    toTrendDTOs(series),
	})
}

func (h *ReportHandler) reportExport(c echo.Context) error {
	var params oapigen.ReportExportParams
	if err := c.Bind(&params); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid query parameters")
	}
	metric := string(domain.AnalyticsMetricFollowers)
	if params.Metric != nil {
		metric = *params.Metric
	}
	kind := service.ExportKind(params.Kind)
	format := service.ExportCSV
	if params.Format != nil {
		format = service.ExportFormat(*params.Format)
	}
	f := filterFrom(params.From, params.To, params.Platform, params.AccountId, &metric)

	switch format {
	case service.ExportCSV:
		c.Response().Header().Set(echo.HeaderContentType, "text/csv")
		c.Response().Header().Set(echo.HeaderContentDisposition,
			"attachment; filename=report-"+string(kind)+".csv")
	case service.ExportJSON:
		c.Response().Header().Set(echo.HeaderContentType, "application/json")
		c.Response().Header().Set(echo.HeaderContentDisposition,
			"attachment; filename=report-"+string(kind)+".json")
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "unknown format")
	}

	// Streaming: the CSV/JSON writers flush straight into the response, so a
	// 90-day window never materialises as one buffer in memory.
	return h.reports.Export(c.Request().Context(), kind, format, f, c.Response().Writer)
}

func mapReportErr(err error) error {
	switch {
	case errors.Is(err, domain.ErrValidation):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	default:
		return err
	}
}

// filterFrom converts the generated nullable query params into the service
// filter. A nil pointer is the "all" case and stays nil (the SQL treats NULL as
// an unconstrained predicate).
func filterFrom(
	from, to *openapiTypes.Date,
	platform *oapigen.Platform,
	accountID *string,
	metric *string,
) service.ReportFilter {
	var f service.ReportFilter
	if from != nil {
		t := from.Time
		f.From = &t
	}
	if to != nil {
		t := to.Time
		f.To = &t
	}
	if platform != nil {
		p := domain.Platform(*platform)
		f.Platform = &p
	}
	if accountID != nil {
		f.AccountID = accountID
	}
	if metric != nil {
		f.Metric = *metric
	}
	return f
}
