package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/repository/sqlcgen"
)

// AnalyticsRepo implements port.AnalyticsStore on top of the sqlc query handle.
// It covers the official-account analytics path: monitored accounts, their
// metric time-series, mentions, and the ingest run audit trail. This path is
// fed by a 3rd-party provider and is disjoint from the worker/action path.
type AnalyticsRepo struct {
	q *sqlcgen.Queries
}

// NewAnalyticsRepo binds the repo to a sqlc query handle.
func NewAnalyticsRepo(q *sqlcgen.Queries) *AnalyticsRepo { return &AnalyticsRepo{q: q} }

var _ port.AnalyticsStore = (*AnalyticsRepo)(nil)

// intervalArg converts a Go duration to the pgtype.Interval the window queries
// bind as $n::interval. Zero/negative falls back to 24h.
func intervalArg(d time.Duration) pgtype.Interval {
	if d <= 0 {
		d = 24 * time.Hour
	}
	return pgtype.Interval{Microseconds: int64(d / time.Microsecond), Valid: true}
}

// ---------------------------------------------------------------------------
// official accounts
// ---------------------------------------------------------------------------

func (r *AnalyticsRepo) CreateOfficialAccount(ctx context.Context, a domain.OfficialAccount) (domain.OfficialAccount, error) {
	status := a.Status
	if status == "" {
		status = domain.OfficialAccountActive
	}
	provider := a.Provider
	if provider == "" {
		provider = domain.AnalyticsProviderA
	}
	tags := a.Tags
	if tags == nil {
		tags = []string{}
	}
	row, err := r.q.CreateOfficialAccount(ctx, sqlcgen.CreateOfficialAccountParams{
		Platform:    platformEnum(a.Platform),
		Handle:      a.Handle,
		DisplayName: a.DisplayName,
		ProfileUrl:  a.ProfileURL,
		AvatarUrl:   a.AvatarURL,
		Status:      officialAccountStatusEnum(status),
		Provider:    analyticsProviderEnum(provider),
		ProviderRef: a.ProviderRef,
		Tags:        tags,
	})
	if isUniqueViolation(err) {
		return domain.OfficialAccount{}, fmt.Errorf("%w: %s on %s", domain.ErrConflict, a.Handle, a.Platform)
	}
	if err != nil {
		return domain.OfficialAccount{}, fmt.Errorf("repository.analytics.CreateOfficialAccount: %w", err)
	}
	return toOfficialAccount(row), nil
}

func (r *AnalyticsRepo) GetOfficialAccount(ctx context.Context, id string) (domain.OfficialAccount, error) {
	row, err := r.q.GetOfficialAccountByID(ctx, uuidValue(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OfficialAccount{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.OfficialAccount{}, fmt.Errorf("repository.analytics.GetOfficialAccount: %w", err)
	}
	return toOfficialAccount(row), nil
}

func (r *AnalyticsRepo) ListOfficialAccounts(ctx context.Context, limit, offset *int) ([]domain.OfficialAccount, error) {
	l, o := ptrPage(limit, offset)
	rows, err := r.q.ListOfficialAccounts(ctx, sqlcgen.ListOfficialAccountsParams{Limit: l, Offset: o})
	if err != nil {
		return nil, fmt.Errorf("repository.analytics.ListOfficialAccounts: %w", err)
	}
	out := make([]domain.OfficialAccount, 0, len(rows))
	for _, row := range rows {
		out = append(out, toOfficialAccount(row))
	}
	return out, nil
}

func (r *AnalyticsRepo) ListOfficialAccountsByPlatform(ctx context.Context, p domain.Platform, limit, offset *int) ([]domain.OfficialAccount, error) {
	l, o := ptrPage(limit, offset)
	rows, err := r.q.ListOfficialAccountsByPlatform(ctx, sqlcgen.ListOfficialAccountsByPlatformParams{
		Platform: platformEnum(p),
		Limit:    l,
		Offset:   o,
	})
	if err != nil {
		return nil, fmt.Errorf("repository.analytics.ListOfficialAccountsByPlatform: %w", err)
	}
	out := make([]domain.OfficialAccount, 0, len(rows))
	for _, row := range rows {
		out = append(out, toOfficialAccount(row))
	}
	return out, nil
}

func (r *AnalyticsRepo) CountOfficialAccountsByPlatform(ctx context.Context, p domain.Platform) (int, error) {
	n, err := r.q.CountOfficialAccountsByPlatform(ctx, platformEnum(p))
	if err != nil {
		return 0, fmt.Errorf("repository.analytics.CountOfficialAccountsByPlatform: %w", err)
	}
	return int(n), nil
}

func (r *AnalyticsRepo) UpdateOfficialAccount(ctx context.Context, a domain.OfficialAccount) (domain.OfficialAccount, error) {
	tags := a.Tags
	if tags == nil {
		tags = []string{}
	}
	row, err := r.q.UpdateOfficialAccount(ctx, sqlcgen.UpdateOfficialAccountParams{
		ID:          uuidValue(a.ID),
		Platform:    platformEnum(a.Platform),
		Handle:      a.Handle,
		DisplayName: a.DisplayName,
		ProfileUrl:  a.ProfileURL,
		AvatarUrl:   a.AvatarURL,
		Status:      officialAccountStatusEnum(a.Status),
		Provider:    analyticsProviderEnum(a.Provider),
		ProviderRef: a.ProviderRef,
		Tags:        tags,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OfficialAccount{}, domain.ErrNotFound
	}
	if isUniqueViolation(err) {
		return domain.OfficialAccount{}, fmt.Errorf("%w: %s on %s", domain.ErrConflict, a.Handle, a.Platform)
	}
	if err != nil {
		return domain.OfficialAccount{}, fmt.Errorf("repository.analytics.UpdateOfficialAccount: %w", err)
	}
	return toOfficialAccount(row), nil
}

// ArchiveOfficialAccount flips status to ARCHIVED. The row and its history stay
// queryable; an archived account is simply excluded from the next ingest run.
func (r *AnalyticsRepo) ArchiveOfficialAccount(ctx context.Context, id string) (domain.OfficialAccount, error) {
	row, err := r.q.ArchiveOfficialAccount(ctx, uuidValue(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OfficialAccount{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.OfficialAccount{}, fmt.Errorf("repository.analytics.ArchiveOfficialAccount: %w", err)
	}
	return toOfficialAccount(row), nil
}

// ListOfficialAccountsForIngest returns ACTIVE accounts for one provider, least
// recently fetched first so a slow account never starves behind fresh ones.
func (r *AnalyticsRepo) ListOfficialAccountsForIngest(ctx context.Context, provider domain.AnalyticsProvider, limit int) ([]domain.OfficialAccount, error) {
	rows, err := r.q.ListOfficialAccountsForIngest(ctx, sqlcgen.ListOfficialAccountsForIngestParams{
		Provider: analyticsProviderEnum(provider),
		Limit:    clampLimit(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("repository.analytics.ListOfficialAccountsForIngest: %w", err)
	}
	out := make([]domain.OfficialAccount, 0, len(rows))
	for _, row := range rows {
		out = append(out, toOfficialAccount(row))
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// snapshots
// ---------------------------------------------------------------------------

// UpsertAnalyticsSnapshot is the idempotent write for the ingest path: the same
// (account, ts, provider) refreshes in place, so a retried run never duplicates
// a point in the series.
func (r *AnalyticsRepo) UpsertAnalyticsSnapshot(ctx context.Context, s domain.AnalyticsSnapshot) (domain.AnalyticsSnapshot, error) {
	ts := s.TS
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	provider := s.Provider
	if provider == "" {
		provider = domain.AnalyticsProviderA
	}
	row, err := r.q.UpsertAnalyticsSnapshot(ctx, sqlcgen.UpsertAnalyticsSnapshotParams{
		OfficialAccountID: uuidValue(s.OfficialAccountID),
		Platform:          platformEnum(s.Platform),
		Ts:                tsPtr(&ts),
		Followers:         s.Followers,
		Reach:             s.Reach,
		Views:             s.Views,
		Mentions:          s.Mentions,
		Engagements:       s.Engagements,
		ProfileViews:      s.ProfileViews,
		Metrics:           jsonbBytes(s.Metrics),
		Provider:          analyticsProviderEnum(provider),
		ProviderRunID:     s.ProviderRunID,
	})
	if err != nil {
		return domain.AnalyticsSnapshot{}, fmt.Errorf("repository.analytics.UpsertAnalyticsSnapshot: %w", err)
	}
	return toAnalyticsSnapshot(row), nil
}

func (r *AnalyticsRepo) ListAnalyticsSnapshots(ctx context.Context, accountID string, from, to time.Time) ([]domain.AnalyticsSnapshot, error) {
	rows, err := r.q.ListAnalyticsSnapshotsByAccount(ctx, sqlcgen.ListAnalyticsSnapshotsByAccountParams{
		OfficialAccountID: uuidValue(accountID),
		Ts:                tsPtr(&from),
		Ts_2:              tsPtr(&to),
	})
	if err != nil {
		return nil, fmt.Errorf("repository.analytics.ListAnalyticsSnapshots: %w", err)
	}
	out := make([]domain.AnalyticsSnapshot, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAnalyticsSnapshot(row))
	}
	return out, nil
}

// AnalyticsOverview returns the latest value of each scalar metric per account
// on a platform over the trailing window. Aggregate columns decode as
// interface{} because they are nullable.
func (r *AnalyticsRepo) AnalyticsOverview(ctx context.Context, p domain.Platform, window time.Duration) ([]domain.AnalyticsSnapshot, error) {
	rows, err := r.q.AnalyticsOverviewByPlatform(ctx, sqlcgen.AnalyticsOverviewByPlatformParams{
		Platform: platformEnum(p),
		Column2:  intervalArg(window),
	})
	if err != nil {
		return nil, fmt.Errorf("repository.analytics.AnalyticsOverview: %w", err)
	}
	out := make([]domain.AnalyticsSnapshot, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.AnalyticsSnapshot{
			ID:                "",
			OfficialAccountID: uuidString(row.OfficialAccountID),
			Platform:          p,
			TS:                derefTimeOrZero(aggTime(row.Ts)),
			Followers:         aggInt64(row.Followers),
			Reach:             aggInt64(row.Reach),
			Views:             aggInt64(row.Views),
			Mentions:          aggInt64(row.Mentions),
			Engagements:       aggInt64(row.Engagements),
			ProfileViews:      aggInt64(row.ProfileViews),
			Provider:          domain.AnalyticsProvider(""),
			FetchedAt:         time.Time{},
		})
	}
	return out, nil
}

// AnalyticsTrendByPlatform returns a daily gap-filled series of one metric
// summed across every account on a platform.
func (r *AnalyticsRepo) AnalyticsTrendByPlatform(ctx context.Context, p domain.Platform, metric string, window time.Duration) ([]domain.TrendPoint, error) {
	rows, err := r.q.AnalyticsTrendByPlatform(ctx, sqlcgen.AnalyticsTrendByPlatformParams{
		Platform: platformEnum(p),
		Column2:  string(metric),
		Column3:  intervalArg(window),
	})
	if err != nil {
		return nil, fmt.Errorf("repository.analytics.AnalyticsTrendByPlatform: %w", err)
	}
	out := make([]domain.TrendPoint, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.TrendPoint{
			Bucket: derefTimeOrZero(aggTime(row.Bucket)),
			Value:  row.Value,
		})
	}
	return out, nil
}

// AnalyticsTrendByAccount is the per-account drill-down of the same series.
func (r *AnalyticsRepo) AnalyticsTrendByAccount(ctx context.Context, accountID string, metric string, window time.Duration) ([]domain.TrendPoint, error) {
	rows, err := r.q.AnalyticsTrendByAccount(ctx, sqlcgen.AnalyticsTrendByAccountParams{
		OfficialAccountID: uuidValue(accountID),
		Column2:           string(metric),
		Column3:           intervalArg(window),
	})
	if err != nil {
		return nil, fmt.Errorf("repository.analytics.AnalyticsTrendByAccount: %w", err)
	}
	out := make([]domain.TrendPoint, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.TrendPoint{
			Bucket: derefTimeOrZero(aggTime(row.Bucket)),
			Value:  row.Value,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// mentions
// ---------------------------------------------------------------------------

func (r *AnalyticsRepo) UpsertAnalyticsMention(ctx context.Context, m domain.AnalyticsMention) (domain.AnalyticsMention, error) {
	if m.FetchedAt.IsZero() {
		m.FetchedAt = time.Now().UTC()
	}
	row, err := r.q.UpsertAnalyticsMention(ctx, sqlcgen.UpsertAnalyticsMentionParams{
		OfficialAccountID: uuidValue(m.OfficialAccountID),
		Platform:          platformEnum(m.Platform),
		ExternalID:        m.ExternalID,
		AuthorHandle:      m.AuthorHandle,
		Text:              m.Text,
		Url:               m.URL,
		PostedAt:          tsPtr(&m.PostedAt),
		Sentiment:         m.Sentiment,
	})
	if err != nil {
		return domain.AnalyticsMention{}, fmt.Errorf("repository.analytics.UpsertAnalyticsMention: %w", err)
	}
	return toAnalyticsMention(row), nil
}

func (r *AnalyticsRepo) ListAnalyticsMentionsByAccount(ctx context.Context, accountID string, limit, offset *int) ([]domain.AnalyticsMention, error) {
	l, o := ptrPage(limit, offset)
	rows, err := r.q.ListAnalyticsMentionsByAccount(ctx, sqlcgen.ListAnalyticsMentionsByAccountParams{
		OfficialAccountID: uuidValue(accountID),
		Limit:             l,
		Offset:            o,
	})
	if err != nil {
		return nil, fmt.Errorf("repository.analytics.ListAnalyticsMentionsByAccount: %w", err)
	}
	out := make([]domain.AnalyticsMention, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAnalyticsMention(row))
	}
	return out, nil
}

func (r *AnalyticsRepo) ListAnalyticsMentionsByPlatform(ctx context.Context, p domain.Platform, limit, offset *int) ([]domain.AnalyticsMention, error) {
	l, o := ptrPage(limit, offset)
	rows, err := r.q.ListAnalyticsMentionsByPlatform(ctx, sqlcgen.ListAnalyticsMentionsByPlatformParams{
		Platform: platformEnum(p),
		Limit:    l,
		Offset:   o,
	})
	if err != nil {
		return nil, fmt.Errorf("repository.analytics.ListAnalyticsMentionsByPlatform: %w", err)
	}
	out := make([]domain.AnalyticsMention, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAnalyticsMention(row))
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// ingest run audit trail
// ---------------------------------------------------------------------------

// CreateAnalyticsIngestRun opens a run in RUNNING state; the closer fills in
// the outcome so a crashed ingestor leaves a RUNNING row the operator can see.
func (r *AnalyticsRepo) CreateAnalyticsIngestRun(ctx context.Context, provider domain.AnalyticsProvider, scope string) (domain.AnalyticsIngestRun, error) {
	row, err := r.q.CreateAnalyticsIngestRun(ctx, sqlcgen.CreateAnalyticsIngestRunParams{
		Provider: analyticsProviderEnum(provider),
		Scope:    scope,
	})
	if err != nil {
		return domain.AnalyticsIngestRun{}, fmt.Errorf("repository.analytics.CreateAnalyticsIngestRun: %w", err)
	}
	return toAnalyticsIngestRun(row), nil
}

func (r *AnalyticsRepo) UpdateAnalyticsIngestRun(ctx context.Context, run domain.AnalyticsIngestRun) (domain.AnalyticsIngestRun, error) {
	row, err := r.q.UpdateAnalyticsIngestRun(ctx, sqlcgen.UpdateAnalyticsIngestRunParams{
		ID:          uuidValue(run.ID),
		Status:      ingestStatusEnum(run.Status),
		AccountsOk:  int32(run.AccountsOk),
		AccountsErr: int32(run.AccountsErr),
		ErrorClass:  run.ErrorClass,
		Error:       run.Error,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AnalyticsIngestRun{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.AnalyticsIngestRun{}, fmt.Errorf("repository.analytics.UpdateAnalyticsIngestRun: %w", err)
	}
	return toAnalyticsIngestRun(row), nil
}

func (r *AnalyticsRepo) ListAnalyticsIngestRuns(ctx context.Context, limit, offset *int) ([]domain.AnalyticsIngestRun, error) {
	l, o := ptrPage(limit, offset)
	rows, err := r.q.ListAnalyticsIngestRuns(ctx, sqlcgen.ListAnalyticsIngestRunsParams{Limit: l, Offset: o})
	if err != nil {
		return nil, fmt.Errorf("repository.analytics.ListAnalyticsIngestRuns: %w", err)
	}
	out := make([]domain.AnalyticsIngestRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAnalyticsIngestRun(row))
	}
	return out, nil
}

// GetLatestAnalyticsIngestRun backs the freshness badge: the most recent
// terminal run tells the operator whether the view is current or stale.
func (r *AnalyticsRepo) GetLatestAnalyticsIngestRun(ctx context.Context) (domain.AnalyticsIngestRun, error) {
	row, err := r.q.GetLatestAnalyticsIngestRun(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AnalyticsIngestRun{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.AnalyticsIngestRun{}, fmt.Errorf("repository.analytics.GetLatestAnalyticsIngestRun: %w", err)
	}
	return toAnalyticsIngestRun(row), nil
}
