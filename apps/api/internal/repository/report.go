package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/repository/sqlcgen"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/service"
)

// ReportRepo implements service.ReportQuery on top of the sqlc handle.
type ReportRepo struct {
	q *sqlcgen.Queries
}

func NewReportRepo(q *sqlcgen.Queries) *ReportRepo { return &ReportRepo{q: q} }

var _ service.ReportQuery = (*ReportRepo)(nil)

func (r *ReportRepo) ActionRollup(ctx context.Context, f service.ReportFilter) ([]service.ActionRow, error) {
	rows, err := r.q.ActionRollupDaily(ctx, sqlcgen.ActionRollupDailyParams{
		Column1: reportDate(f.From),
		Column2: reportDate(f.To),
		Column3: reportPlatform(f.Platform),
		Column4: reportUUID(f.AccountID),
	})
	if err != nil {
		return nil, err
	}
	out := make([]service.ActionRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, service.ActionRow{
			Day:        reportDateValue(row.Day),
			AccountID:  uuidString(row.AccountID),
			Username:   row.Username,
			Platform:   platformDomain(row.Platform),
			ActionType: string(row.Type),
			Total:      row.Total,
			Succeeded:  row.Succeeded,
			Failed:     row.Failed,
		})
	}
	return out, nil
}

func (r *ReportRepo) TargetRollup(ctx context.Context, f service.ReportFilter) ([]service.TargetRow, error) {
	rows, err := r.q.ActionTargetRollup(ctx, sqlcgen.ActionTargetRollupParams{
		Column1: reportDate(f.From),
		Column2: reportDate(f.To),
		Column3: reportPlatform(f.Platform),
	})
	if err != nil {
		return nil, err
	}
	out := make([]service.TargetRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, service.TargetRow{
			ID:        uuidString(row.ID),
			URL:       row.Url,
			Platform:  platformDomain(row.Platform),
			Total:     row.Total,
			Succeeded: row.Succeeded,
			Failed:    row.Failed,
		})
	}
	return out, nil
}

func reportDate(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{Valid: false}
	}
	return pgtype.Date{Time: *t, Valid: true}
}

func reportDateValue(d pgtype.Date) time.Time {
	if !d.Valid {
		return time.Time{}
	}
	return d.Time
}

func reportPlatform(p *domain.Platform) string {
	if p == nil || !p.Valid() {
		return ""
	}
	return string(*p)
}

func reportUUID(s *string) pgtype.UUID {
	if s == nil {
		return pgtype.UUID{Valid: false}
	}
	var u pgtype.UUID
	if err := u.Scan(*s); err != nil {
		return pgtype.UUID{Valid: false}
	}
	return u
}
