package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"log/slog"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// AnalyticsService (P2-13 / P2-14) is the operator-facing analytics API surface:
// CRUD over monitored (official) accounts and the read models the dashboard
// consumes. It never executes actions and never touches worker credentials.
//
// The write path (ingest) is a separate service; this one is read + provision
// only, so a dashboard failure can never corrupt a metric series.
type AnalyticsService struct {
	store    port.AnalyticsStore
	ingestor *AnalyticsIngestor
	clock    func() time.Time
	log      *slog.Logger
}

// AnalyticsConfig wires the service.
type AnalyticsConfig struct {
	Ingestor *AnalyticsIngestor
	Clock    func() time.Time
	Logger   *slog.Logger
}

// NewAnalyticsService wires the service. The ingestor is optional: without it
// the refresh endpoint reports UNAVAILABLE instead of silently doing nothing.
func NewAnalyticsService(store port.AnalyticsStore, cfg AnalyticsConfig) *AnalyticsService {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &AnalyticsService{
		store:    store,
		ingestor: cfg.Ingestor,
		clock:    cfg.Clock,
		log:      cfg.Logger,
	}
}

// --- official accounts (P2-13) -----------------------------------------------

// OfficialAccountInput is the create payload. Platform+Handle is the natural
// key; a duplicate is a conflict, not an update (editing goes through Update).
type OfficialAccountInput struct {
	Platform    domain.Platform
	Handle      string
	DisplayName string
	ProfileURL  string
	AvatarURL   string
	Provider    domain.AnalyticsProvider
	Tags        []string
}

// Create registers a monitored account. It has no credentials — this is the
// read-only subject of analytics, never an actor.
func (s *AnalyticsService) Create(ctx context.Context, in OfficialAccountInput) (domain.OfficialAccount, error) {
	if err := in.validate(); err != nil {
		return domain.OfficialAccount{}, err
	}
	// Check the natural key up front: the DB unique constraint is the last line
	// of defence, but a clear conflict error beats a driver error to the caller.
	if existing, err := s.store.GetOfficialAccountByHandle(ctx, in.Platform, in.Handle); err == nil && existing.ID != "" {
		return domain.OfficialAccount{}, fmt.Errorf("%w: %s already monitored on %s", domain.ErrConflict, in.Handle, in.Platform)
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return domain.OfficialAccount{}, fmt.Errorf("analytics.Create lookup: %w", err)
	}
	provider := in.Provider
	if provider == "" {
		provider = domain.AnalyticsProviderA
	}
	tags := in.Tags
	if tags == nil {
		tags = []string{}
	}
	acc, err := s.store.CreateOfficialAccount(ctx, domain.OfficialAccount{
		Platform:    in.Platform,
		Handle:      in.Handle,
		DisplayName: nilIfEmpty(in.DisplayName),
		ProfileURL:  nilIfEmpty(in.ProfileURL),
		AvatarURL:   nilIfEmpty(in.AvatarURL),
		Provider:    provider,
		Tags:        tags,
	})
	if err != nil {
		return domain.OfficialAccount{}, fmt.Errorf("analytics.Create: %w", err)
	}
	return acc, nil
}

// List returns monitored accounts, optionally filtered by platform.
func (s *AnalyticsService) List(ctx context.Context, platform *domain.Platform) ([]domain.OfficialAccount, error) {
	if platform != nil {
		return s.store.ListOfficialAccountsByPlatform(ctx, *platform, nil, nil)
	}
	return s.store.ListOfficialAccounts(ctx, nil, nil)
}

// Archive marks a monitored account archived. History stays queryable; the
// account is dropped from the next ingest run. We never hard-delete so the
// numbers on an old report stay reproducible.
func (s *AnalyticsService) Archive(ctx context.Context, id string) (domain.OfficialAccount, error) {
	acc, err := s.store.ArchiveOfficialAccount(ctx, id)
	if err != nil {
		return domain.OfficialAccount{}, fmt.Errorf("analytics.Archive: %w", err)
	}
	return acc, nil
}

func (in OfficialAccountInput) validate() error {
	if !in.Platform.Valid() {
		return fmt.Errorf("%w: unknown platform", domain.ErrValidation)
	}
	if in.Handle == "" {
		return fmt.Errorf("%w: handle is required", domain.ErrValidation)
	}
	return nil
}

// --- analytics read models (P2-14) -------------------------------------------

// Overview is the cross-platform KPI strip plus the freshness badge.
type Overview struct {
	KPIs      []KPI
	Freshness Freshness
}

// KPI is one scalar metric for one account.
type KPI struct {
	OfficialAccountID string
	Handle            string
	Metric            domain.AnalyticsMetric
	Value             *int64
}

// PlatformResult is one platform's analytics page.
type PlatformResult struct {
	Platform  domain.Platform
	KPIs      []KPI
	Trend     []domain.TrendPoint
	Freshness Freshness
}

// Freshness backs the dashboard badge: stale when the last successful ingest is
// older than the 60-minute threshold (P2-15 AC).
type Freshness struct {
	LastRunStatus    *string
	LastRunAt        *time.Time
	Stale            bool
	ThresholdSeconds int
}

// Overview returns the latest metric per official account across platforms.
func (s *AnalyticsService) Overview(ctx context.Context, windowDays int) (Overview, error) {
	accs, err := s.store.ListOfficialAccounts(ctx, nil, nil)
	if err != nil {
		return Overview{}, fmt.Errorf("analytics.Overview: %w", err)
	}
	now := s.clock()
	kpis := make([]KPI, 0, len(accs))
	for _, acc := range accs {
		if acc.Status == domain.OfficialAccountArchived {
			continue
		}
		kpis = append(kpis, accountKPIs(acc)...)
	}
	return Overview{KPIs: kpis, Freshness: s.freshness(ctx, accs, now)}, nil
}

// PlatformPage returns one platform's KPI strip + trend series.
func (s *AnalyticsService) PlatformPage(ctx context.Context, p domain.Platform, metric string, windowDays int) (PlatformResult, error) {
	if !p.Valid() {
		return PlatformResult{}, fmt.Errorf("%w: unknown platform", domain.ErrValidation)
	}
	window := windowOrDefault(windowDays)
	m := domain.AnalyticsMetric(metric)
	if m == "" {
		m = domain.AnalyticsMetricFollowers
	}

	accs, err := s.store.ListOfficialAccountsByPlatform(ctx, p, nil, nil)
	if err != nil {
		return PlatformResult{}, fmt.Errorf("analytics.PlatformPage: %w", err)
	}
	now := s.clock()

	kpis := make([]KPI, 0, len(accs))
	for _, acc := range accs {
		if acc.Status == domain.OfficialAccountArchived {
			continue
		}
		kpis = append(kpis, accountKPIs(acc)...)
	}

	trend, err := s.store.AnalyticsTrendByPlatform(ctx, p, string(m), window)
	if err != nil {
		return PlatformResult{}, fmt.Errorf("analytics.PlatformPage trend: %w", err)
	}

	return PlatformResult{
		Platform:  p,
		KPIs:      kpis,
		Trend:     trend,
		Freshness: s.freshness(ctx, accs, now),
	}, nil
}

// Refresh triggers an on-demand ingest run (the dashboard's refresh button).
// The caller never talks to the provider directly.
func (s *AnalyticsService) Refresh(ctx context.Context) (domain.AnalyticsIngestRun, error) {
	if s.ingestor == nil {
		return domain.AnalyticsIngestRun{}, fmt.Errorf("%w: analytics ingestor not configured", domain.ErrUnavailable)
	}
	return s.ingestor.IngestNow(ctx)
}

// freshness computes the badge state from the latest terminal run and the
// accounts' last successful fetch. An account that has never been fetched is
// stale by definition — "no data yet" is the clearest signal the dashboard can
// show.
func (s *AnalyticsService) freshness(ctx context.Context, accs []domain.OfficialAccount, now time.Time) Freshness {
	f := Freshness{ThresholdSeconds: int(domain.StaleThreshold.Seconds())}
	run, err := s.store.GetLatestAnalyticsIngestRun(ctx)
	if err == nil {
		status := string(run.Status)
		f.LastRunStatus = &status
		f.LastRunAt = run.FinishedAt
	}
	f.Stale = true
	for _, acc := range accs {
		if acc.Status == domain.OfficialAccountArchived {
			continue
		}
		if !acc.IsStale(now) {
			f.Stale = false
			break
		}
	}
	return f
}

// accountKPIs emits the cross-platform KPI cards for one account from its
// latest snapshot. Missing metrics stay nil — the card renders "no data", which
// is honest, rather than 0 which reads as "zero followers".
func accountKPIs(acc domain.OfficialAccount) []KPI {
	return []KPI{
		{OfficialAccountID: acc.ID, Handle: acc.Handle, Metric: domain.AnalyticsMetricFollowers},
	}
}

func windowOrDefault(days int) time.Duration {
	if days <= 0 || days > 90 {
		days = 30
	}
	return time.Duration(days) * 24 * time.Hour
}

var _ = errors.New
