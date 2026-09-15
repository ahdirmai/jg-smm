package http

import (
	"context"
	"sync"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// Local in-memory port.AnalyticsStore for the handler tests. The service
// package has its own; this one keeps the http tests free of a cross-package
// test helper (and the import cycle that would create).
type httpAnalyticsStore struct {
	mu        sync.Mutex
	accounts  map[string]domain.OfficialAccount
	snapshots []domain.AnalyticsSnapshot
	runs      []domain.AnalyticsIngestRun
}

func newFakeAnalyticsStore() *httpAnalyticsStore {
	return &httpAnalyticsStore{accounts: map[string]domain.OfficialAccount{}}
}

var _ port.AnalyticsStore = (*httpAnalyticsStore)(nil)

func (f *httpAnalyticsStore) CreateOfficialAccount(ctx context.Context, a domain.OfficialAccount) (domain.OfficialAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a.Status == "" {
		a.Status = domain.OfficialAccountActive
	}
	if a.Provider == "" {
		a.Provider = domain.AnalyticsProviderA
	}
	a.ID = "oa-" + string(a.Platform) + "-" + a.Handle
	a.CreatedAt = time.Now()
	f.accounts[a.ID] = a
	return a, nil
}
func (f *httpAnalyticsStore) GetOfficialAccount(ctx context.Context, id string) (domain.OfficialAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.accounts[id]; ok {
		return a, nil
	}
	return domain.OfficialAccount{}, domain.ErrNotFound
}
func (f *httpAnalyticsStore) GetOfficialAccountByHandle(ctx context.Context, p domain.Platform, handle string) (domain.OfficialAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.accounts {
		if a.Platform == p && a.Handle == handle {
			return a, nil
		}
	}
	return domain.OfficialAccount{}, domain.ErrNotFound
}

func (f *httpAnalyticsStore) ListOfficialAccounts(ctx context.Context, limit, offset *int) ([]domain.OfficialAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.OfficialAccount, 0, len(f.accounts))
	for _, a := range f.accounts {
		out = append(out, a)
	}
	return out, nil
}
func (f *httpAnalyticsStore) ListOfficialAccountsByPlatform(ctx context.Context, p domain.Platform, limit, offset *int) ([]domain.OfficialAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.OfficialAccount, 0)
	for _, a := range f.accounts {
		if a.Platform == p {
			out = append(out, a)
		}
	}
	return out, nil
}
func (f *httpAnalyticsStore) CountOfficialAccountsByPlatform(ctx context.Context, p domain.Platform) (int, error) {
	c, _ := f.ListOfficialAccountsByPlatform(context.Background(), p, nil, nil)
	return len(c), nil
}
func (f *httpAnalyticsStore) UpdateOfficialAccount(ctx context.Context, a domain.OfficialAccount) (domain.OfficialAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accounts[a.ID] = a
	return a, nil
}
func (f *httpAnalyticsStore) TouchOfficialAccountFetched(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.accounts[id]; ok {
		now := time.Now()
		a.LastFetchedAt = &now
		f.accounts[id] = a
	}
	return nil
}

func (f *httpAnalyticsStore) ArchiveOfficialAccount(ctx context.Context, id string) (domain.OfficialAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.accounts[id]; ok {
		a.Status = domain.OfficialAccountArchived
		f.accounts[id] = a
		return a, nil
	}
	return domain.OfficialAccount{}, domain.ErrNotFound
}
func (f *httpAnalyticsStore) ListOfficialAccountsForIngest(ctx context.Context, provider domain.AnalyticsProvider, limit int) ([]domain.OfficialAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.OfficialAccount, 0)
	for _, a := range f.accounts {
		if a.Status == domain.OfficialAccountActive {
			out = append(out, a)
		}
	}
	return out, nil
}
func (f *httpAnalyticsStore) UpsertAnalyticsSnapshot(ctx context.Context, s domain.AnalyticsSnapshot) (domain.AnalyticsSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshots = append(f.snapshots, s)
	return s, nil
}
func (f *httpAnalyticsStore) ListAnalyticsSnapshots(ctx context.Context, accountID string, from, to time.Time) ([]domain.AnalyticsSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.AnalyticsSnapshot, 0)
	for _, s := range f.snapshots {
		if s.OfficialAccountID == accountID {
			out = append(out, s)
		}
	}
	return out, nil
}
func (f *httpAnalyticsStore) AnalyticsOverview(ctx context.Context, p domain.Platform, window time.Duration) ([]domain.AnalyticsSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.AnalyticsSnapshot, 0)
	for _, s := range f.snapshots {
		if s.Platform == p {
			out = append(out, s)
		}
	}
	return out, nil
}
func (f *httpAnalyticsStore) AnalyticsTrendByPlatform(ctx context.Context, p domain.Platform, metric string, window time.Duration) ([]domain.TrendPoint, error) {
	return []domain.TrendPoint{{Bucket: time.Now(), Value: 100}}, nil
}
func (f *httpAnalyticsStore) AnalyticsTrendByAccount(ctx context.Context, accountID string, metric string, window time.Duration) ([]domain.TrendPoint, error) {
	return []domain.TrendPoint{{Bucket: time.Now(), Value: 50}}, nil
}
func (f *httpAnalyticsStore) UpsertAnalyticsMention(ctx context.Context, m domain.AnalyticsMention) (domain.AnalyticsMention, error) {
	return m, nil
}
func (f *httpAnalyticsStore) ListAnalyticsMentionsByAccount(ctx context.Context, accountID string, limit, offset *int) ([]domain.AnalyticsMention, error) {
	return nil, nil
}
func (f *httpAnalyticsStore) ListAnalyticsMentionsByPlatform(ctx context.Context, p domain.Platform, limit, offset *int) ([]domain.AnalyticsMention, error) {
	return nil, nil
}
func (f *httpAnalyticsStore) CreateAnalyticsIngestRun(ctx context.Context, provider domain.AnalyticsProvider, scope string) (domain.AnalyticsIngestRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	run := domain.AnalyticsIngestRun{
		ID:        "run-" + time.Now().Format("150405.000000"),
		Provider:  provider,
		Scope:     scope,
		Status:    domain.IngestStatusRunning,
		StartedAt: time.Now(),
	}
	f.runs = append(f.runs, run)
	return run, nil
}
func (f *httpAnalyticsStore) UpdateAnalyticsIngestRun(ctx context.Context, run domain.AnalyticsIngestRun) (domain.AnalyticsIngestRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, r := range f.runs {
		if r.ID == run.ID {
			now := time.Now()
			run.FinishedAt = &now
			f.runs[i] = run
			return run, nil
		}
	}
	return domain.AnalyticsIngestRun{}, domain.ErrNotFound
}
func (f *httpAnalyticsStore) ListAnalyticsIngestRuns(ctx context.Context, limit, offset *int) ([]domain.AnalyticsIngestRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.AnalyticsIngestRun, len(f.runs))
	copy(out, f.runs)
	return out, nil
}
func (f *httpAnalyticsStore) GetLatestAnalyticsIngestRun(ctx context.Context) (domain.AnalyticsIngestRun, error) {
	runs, _ := f.ListAnalyticsIngestRuns(context.Background(), nil, nil)
	if len(runs) == 0 {
		return domain.AnalyticsIngestRun{}, domain.ErrNotFound
	}
	return runs[len(runs)-1], nil
}

func seedOfficial(f *httpAnalyticsStore, handle string) domain.OfficialAccount {
	a, _ := f.CreateOfficialAccount(context.Background(), domain.OfficialAccount{
		Platform: domain.PlatformInstagram,
		Handle:   handle,
		Provider: domain.AnalyticsProviderA,
	})
	return a
}

// httpProvider is a no-op analytics provider for the handler tests.
type httpProvider struct{}

var _ port.AnalyticsProvider = (*httpProvider)(nil)

func (p *httpProvider) FetchMetrics(ctx context.Context, acc domain.OfficialAccount) (domain.AnalyticsSnapshot, error) {
	return domain.AnalyticsSnapshot{OfficialAccountID: acc.ID, Platform: acc.Platform}, nil
}
func (p *httpProvider) FetchMentions(ctx context.Context, acc domain.OfficialAccount) ([]domain.AnalyticsMention, error) {
	return nil, nil
}
func (p *httpProvider) Health(ctx context.Context) error { return nil }
