package service

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// ReportFilter is the shared shape of every report query (P4-04). A nil field
// means "no filter" — the SQL turns NULL into an unconstrained predicate, so
// the UI's "all" case is a NULL, never an empty string.
type ReportFilter struct {
	From      *time.Time
	To        *time.Time
	Platform  *domain.Platform
	AccountID *string
	Metric    string
}

// ActionRow is one (day, account, action type) cell of the action report.
type ActionRow struct {
	Day        time.Time       `json:"day"`
	AccountID  string          `json:"accountId"`
	Username   string          `json:"username"`
	Platform   domain.Platform `json:"platform"`
	ActionType string          `json:"actionType"`
	Total      int64           `json:"total"`
	Succeeded  int64           `json:"succeeded"`
	Failed     int64           `json:"failed"`
}

// TargetRow is one post with its outcome counts.
type TargetRow struct {
	ID        string          `json:"id"`
	URL       string          `json:"url"`
	Platform  domain.Platform `json:"platform"`
	Total     int64           `json:"total"`
	Succeeded int64           `json:"succeeded"`
	Failed    int64           `json:"failed"`
}

// ReportQuery is the read surface the report builder needs. Implemented by
// ReportRepo; kept here so the service can be tested against a fake.
type ReportQuery interface {
	ActionRollup(ctx context.Context, f ReportFilter) ([]ActionRow, error)
	TargetRollup(ctx context.Context, f ReportFilter) ([]TargetRow, error)
}

// ReportService builds the dashboard reports (P4-04) and streams them out as
// files (P4-05). It is read-only: a report never mutates the queue.
type ReportService struct {
	queries   ReportQuery
	analytics port.AnalyticsStore
	log       *slog.Logger
}

func NewReportService(q ReportQuery, analyticsStore port.AnalyticsStore, logger *slog.Logger) *ReportService {
	if logger == nil {
		logger = slog.Default()
	}
	return &ReportService{queries: q, analytics: analyticsStore, log: logger}
}

// ActionReport returns the daily rollup plus a per-day total series for the chart.
func (s *ReportService) ActionReport(ctx context.Context, f ReportFilter) ([]ActionRow, []domain.TrendPoint, error) {
	rows, err := s.queries.ActionRollup(ctx, f)
	if err != nil {
		return nil, nil, fmt.Errorf("report.actions: %w", err)
	}
	return rows, actionSeries(rows), nil
}

// TargetReport returns the per-post rollup.
func (s *ReportService) TargetReport(ctx context.Context, f ReportFilter) ([]TargetRow, error) {
	rows, err := s.queries.TargetRollup(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("report.targets: %w", err)
	}
	return rows, nil
}

// AnalyticsReport returns one monitored account's metric series.
func (s *ReportService) AnalyticsReport(ctx context.Context, f ReportFilter) (string, []domain.TrendPoint, error) {
	if f.AccountID == nil {
		return "", nil, fmt.Errorf("%w: accountId is required", domain.ErrValidation)
	}
	metric := f.Metric
	if metric == "" {
		metric = string(domain.AnalyticsMetricFollowers)
	}
	accs, err := s.analytics.ListOfficialAccounts(ctx, nil, nil)
	if err != nil {
		return "", nil, fmt.Errorf("report.analytics: %w", err)
	}
	var platform *domain.Platform
	for _, a := range accs {
		if a.ID == *f.AccountID {
			p := a.Platform
			platform = &p
			break
		}
	}
	if platform == nil {
		return "", nil, fmt.Errorf("%w: unknown official account", domain.ErrNotFound)
	}
	window := reportWindow(f)
	series, err := s.analytics.AnalyticsTrendByAccount(ctx, *f.AccountID, metric, window)
	if err != nil {
		return "", nil, fmt.Errorf("report.analytics.series: %w", err)
	}
	return metric, series, nil
}

// actionSeries folds the rollup into one total per day (the chart line).
func actionSeries(rows []ActionRow) []domain.TrendPoint {
	byDay := make(map[time.Time]int64)
	order := make([]time.Time, 0, len(rows))
	for _, r := range rows {
		if _, ok := byDay[r.Day]; !ok {
			order = append(order, r.Day)
		}
		byDay[r.Day] += r.Total
	}
	out := make([]domain.TrendPoint, 0, len(order))
	for _, d := range order {
		out = append(out, domain.TrendPoint{Bucket: d, Value: byDay[d]})
	}
	return out
}

// reportWindow is the (from,to] duration the analytics store consumes. The
// report's default window mirrors the monitoring pages.
func reportWindow(f ReportFilter) time.Duration {
	if f.To != nil {
		if f.From != nil {
			return f.To.Sub(*f.From)
		}
		return time.Until(*f.To)
	}
	return 30 * 24 * time.Hour
}

// --- export (P4-05) ----------------------------------------------------------

// ExportKind selects which report is streamed.
type ExportKind string

const (
	ExportActions   ExportKind = "actions"
	ExportTargets   ExportKind = "targets"
	ExportAnalytics ExportKind = "analytics"
)

// ExportFormat is the file shape.
type ExportFormat string

const (
	ExportCSV  ExportFormat = "csv"
	ExportJSON ExportFormat = "json"
)

func (k ExportKind) Valid() bool {
	switch k {
	case ExportActions, ExportTargets, ExportAnalytics:
		return true
	}
	return false
}

// Export streams the selected report to w. CSV is written row-by-row so a large
// window never buffers in memory; JSON reuses the same rows.
func (s *ReportService) Export(ctx context.Context, kind ExportKind, format ExportFormat, f ReportFilter, w io.Writer) error {
	if !kind.Valid() {
		return fmt.Errorf("%w: unknown report kind", domain.ErrValidation)
	}
	switch format {
	case ExportCSV:
		return s.exportCSV(ctx, kind, f, w)
	case ExportJSON:
		return s.exportJSON(ctx, kind, f, w)
	default:
		return fmt.Errorf("%w: unknown format", domain.ErrValidation)
	}
}

func (s *ReportService) exportCSV(ctx context.Context, kind ExportKind, f ReportFilter, w io.Writer) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	header := csvHeader(kind)
	if err := cw.Write(header); err != nil {
		return err
	}

	switch kind {
	case ExportActions:
		rows, _, err := s.ActionReport(ctx, f)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if err := cw.Write([]string{
				r.Day.Format(time.DateOnly), r.AccountID, r.Username, string(r.Platform),
				r.ActionType, fmt.Sprint(r.Total), fmt.Sprint(r.Succeeded), fmt.Sprint(r.Failed),
			}); err != nil {
				return err
			}
		}
	case ExportTargets:
		rows, err := s.TargetReport(ctx, f)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if err := cw.Write([]string{
				r.ID, r.URL, string(r.Platform),
				fmt.Sprint(r.Total), fmt.Sprint(r.Succeeded), fmt.Sprint(r.Failed),
			}); err != nil {
				return err
			}
		}
	case ExportAnalytics:
		metric, series, err := s.AnalyticsReport(ctx, f)
		if err != nil {
			return err
		}
		for _, p := range series {
			if err := cw.Write([]string{*f.AccountID, metric, p.Bucket.Format(time.RFC3339), fmt.Sprint(p.Value)}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *ReportService) exportJSON(ctx context.Context, kind ExportKind, f ReportFilter, w io.Writer) error {
	enc := json.NewEncoder(w)
	switch kind {
	case ExportActions:
		rows, series, err := s.ActionReport(ctx, f)
		if err != nil {
			return err
		}
		return enc.Encode(map[string]any{"rows": rows, "series": series})
	case ExportTargets:
		rows, err := s.TargetReport(ctx, f)
		if err != nil {
			return err
		}
		return enc.Encode(map[string]any{"rows": rows})
	case ExportAnalytics:
		metric, series, err := s.AnalyticsReport(ctx, f)
		if err != nil {
			return err
		}
		return enc.Encode(map[string]any{"accountId": *f.AccountID, "metric": metric, "series": series})
	}
	return errors.New("unreachable")
}

func csvHeader(kind ExportKind) []string {
	switch kind {
	case ExportActions:
		return []string{"day", "accountId", "username", "platform", "actionType", "total", "succeeded", "failed"}
	case ExportTargets:
		return []string{"id", "url", "platform", "total", "succeeded", "failed"}
	case ExportAnalytics:
		return []string{"accountId", "metric", "bucket", "value"}
	}
	return nil
}
