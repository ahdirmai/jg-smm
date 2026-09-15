package service

import (
	"context"
	"sync"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// fakeAnalyticsStore is an in-memory port.AnalyticsStore for the ingestor and
// the analytics API tests.
type fakeAnalyticsStore struct {
	mu        sync.Mutex
	accounts  map[string]domain.OfficialAccount
	snapshots []domain.AnalyticsSnapshot
	mentions  []domain.AnalyticsMention
	runs      []domain.AnalyticsIngestRun
}

func newFakeAnalyticsStore() *fakeAnalyticsStore {
	return &fakeAnalyticsStore{accounts: map[string]domain.OfficialAccount{}}
}

var _ port.AnalyticsStore = (*fakeAnalyticsStore)(nil)

func (f *fakeAnalyticsStore) CreateOfficialAccount(ctx context.Context, a domain.OfficialAccount) (domain.OfficialAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Mirror the DDL default: a new account is ACTIVE until changed.
	if a.Status == "" {
		a.Status = domain.OfficialAccountActive
	}
	if a.Provider == "" {
		a.Provider = domain.AnalyticsProviderA
	}
	a.ID = fakeID("oa", string(a.Platform)+"|"+a.Handle)
	a.CreatedAt = time.Now()
	f.accounts[a.ID] = a
	return a, nil
}
func (f *fakeAnalyticsStore) GetOfficialAccount(ctx context.Context, id string) (domain.OfficialAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.accounts[id]; ok {
		return a, nil
	}
	return domain.OfficialAccount{}, domain.ErrNotFound
}
func (f *fakeAnalyticsStore) GetOfficialAccountByHandle(ctx context.Context, p domain.Platform, handle string) (domain.OfficialAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.accounts {
		if a.Platform == p && a.Handle == handle {
			return a, nil
		}
	}
	return domain.OfficialAccount{}, domain.ErrNotFound
}

func (f *fakeAnalyticsStore) ListOfficialAccounts(ctx context.Context, limit, offset *int) ([]domain.OfficialAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.OfficialAccount, 0, len(f.accounts))
	for _, a := range f.accounts {
		out = append(out, a)
	}
	return out, nil
}
func (f *fakeAnalyticsStore) ListOfficialAccountsByPlatform(ctx context.Context, p domain.Platform, limit, offset *int) ([]domain.OfficialAccount, error) {
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
func (f *fakeAnalyticsStore) CountOfficialAccountsByPlatform(ctx context.Context, p domain.Platform) (int, error) {
	c, _ := f.ListOfficialAccountsByPlatform(context.Background(), p, nil, nil)
	return len(c), nil
}
func (f *fakeAnalyticsStore) UpdateOfficialAccount(ctx context.Context, a domain.OfficialAccount) (domain.OfficialAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accounts[a.ID] = a
	return a, nil
}
func (f *fakeAnalyticsStore) TouchOfficialAccountFetched(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.accounts[id]; ok {
		now := time.Now()
		a.LastFetchedAt = &now
		f.accounts[id] = a
	}
	return nil
}

func (f *fakeAnalyticsStore) ArchiveOfficialAccount(ctx context.Context, id string) (domain.OfficialAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.accounts[id]; ok {
		a.Status = domain.OfficialAccountArchived
		f.accounts[id] = a
		return a, nil
	}
	return domain.OfficialAccount{}, domain.ErrNotFound
}
func (f *fakeAnalyticsStore) ListOfficialAccountsForIngest(ctx context.Context, provider domain.AnalyticsProvider, limit int) ([]domain.OfficialAccount, error) {
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
func (f *fakeAnalyticsStore) UpsertAnalyticsSnapshot(ctx context.Context, s domain.AnalyticsSnapshot) (domain.AnalyticsSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshots = append(f.snapshots, s)
	return s, nil
}
func (f *fakeAnalyticsStore) ListAnalyticsSnapshots(ctx context.Context, accountID string, from, to time.Time) ([]domain.AnalyticsSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.AnalyticsSnapshot, 0)
	for _, s := range f.snapshots {
		if s.OfficialAccountID == accountID && !s.TS.Before(from) && s.TS.Before(to) {
			out = append(out, s)
		}
	}
	return out, nil
}
func (f *fakeAnalyticsStore) AnalyticsOverview(ctx context.Context, p domain.Platform, window time.Duration) ([]domain.AnalyticsSnapshot, error) {
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
func (f *fakeAnalyticsStore) AnalyticsTrendByPlatform(ctx context.Context, p domain.Platform, metric string, window time.Duration) ([]domain.TrendPoint, error) {
	return []domain.TrendPoint{{Bucket: time.Now(), Value: 100}}, nil
}
func (f *fakeAnalyticsStore) AnalyticsTrendByAccount(ctx context.Context, accountID string, metric string, window time.Duration) ([]domain.TrendPoint, error) {
	return []domain.TrendPoint{{Bucket: time.Now(), Value: 50}}, nil
}
func (f *fakeAnalyticsStore) UpsertAnalyticsMention(ctx context.Context, m domain.AnalyticsMention) (domain.AnalyticsMention, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, ex := range f.mentions {
		if ex.Platform == m.Platform && ex.ExternalID == m.ExternalID {
			f.mentions[i] = m
			return m, nil
		}
	}
	f.mentions = append(f.mentions, m)
	return m, nil
}
func (f *fakeAnalyticsStore) ListAnalyticsMentionsByAccount(ctx context.Context, accountID string, limit, offset *int) ([]domain.AnalyticsMention, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.AnalyticsMention, 0)
	for _, m := range f.mentions {
		if m.OfficialAccountID == accountID {
			out = append(out, m)
		}
	}
	return out, nil
}
func (f *fakeAnalyticsStore) ListAnalyticsMentionsByPlatform(ctx context.Context, p domain.Platform, limit, offset *int) ([]domain.AnalyticsMention, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.AnalyticsMention, 0)
	for _, m := range f.mentions {
		if m.Platform == p {
			out = append(out, m)
		}
	}
	return out, nil
}
func (f *fakeAnalyticsStore) CreateAnalyticsIngestRun(ctx context.Context, provider domain.AnalyticsProvider, scope string) (domain.AnalyticsIngestRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	run := domain.AnalyticsIngestRun{
		ID:        fakeID("run", string(provider)+"|"+scope+"|"+time.Now().Format(time.RFC3339Nano)),
		Provider:  provider,
		Scope:     scope,
		Status:    domain.IngestStatusRunning,
		StartedAt: time.Now(),
	}
	f.runs = append(f.runs, run)
	return run, nil
}
func (f *fakeAnalyticsStore) UpdateAnalyticsIngestRun(ctx context.Context, run domain.AnalyticsIngestRun) (domain.AnalyticsIngestRun, error) {
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
func (f *fakeAnalyticsStore) ListAnalyticsIngestRuns(ctx context.Context, limit, offset *int) ([]domain.AnalyticsIngestRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.AnalyticsIngestRun, len(f.runs))
	copy(out, f.runs)
	return out, nil
}
func (f *fakeAnalyticsStore) GetLatestAnalyticsIngestRun(ctx context.Context) (domain.AnalyticsIngestRun, error) {
	runs, _ := f.ListAnalyticsIngestRuns(context.Background(), nil, nil)
	if len(runs) == 0 {
		return domain.AnalyticsIngestRun{}, domain.ErrNotFound
	}
	return runs[len(runs)-1], nil
}

func seedOfficial(f *fakeAnalyticsStore, handle string) domain.OfficialAccount {
	a, _ := f.CreateOfficialAccount(context.Background(), domain.OfficialAccount{
		Platform: domain.PlatformInstagram,
		Handle:   handle,
		Provider: domain.AnalyticsProviderA,
	})
	return a
}
