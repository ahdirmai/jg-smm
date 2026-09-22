package service

import (
	"context"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// fakeRunner is a controllable port.ApifyRunner: it records the calls and
// returns whatever outcome the test dialed in.
type fakeRunner struct {
	runs   []port.ApifyInput
	search []port.ApifySearchInput
	out    port.ApifyOutput
	outErr error
}

var _ port.ApifyRunner = (*fakeRunner)(nil)

func (r *fakeRunner) Run(ctx context.Context, in port.ApifyInput) (port.ApifyOutput, error) {
	r.runs = append(r.runs, in)
	if r.outErr != nil {
		return port.ApifyOutput{}, r.outErr
	}
	return r.out, nil
}

func (r *fakeRunner) RunSearch(ctx context.Context, in port.ApifySearchInput) (port.ApifyOutput, error) {
	r.search = append(r.search, in)
	if r.outErr != nil {
		return port.ApifyOutput{}, r.outErr
	}
	return r.out, nil
}

// fakeProvider is a controllable port.AnalyticsProvider for the ingestor tests.
type fakeProvider struct {
	metrics    domain.AnalyticsSnapshot
	mentions   []domain.AnalyticsMention
	metricsErr error
	healthErr  error
	calls      int
}

var _ port.AnalyticsProvider = (*fakeProvider)(nil)

func (p *fakeProvider) FetchMetrics(ctx context.Context, acc domain.OfficialAccount) (domain.AnalyticsSnapshot, error) {
	p.calls++
	if p.metricsErr != nil {
		return domain.AnalyticsSnapshot{}, p.metricsErr
	}
	s := p.metrics
	s.OfficialAccountID = acc.ID
	s.Platform = acc.Platform
	s.TS = time.Now().UTC()
	return s, nil
}
func (p *fakeProvider) FetchMentions(ctx context.Context, acc domain.OfficialAccount) ([]domain.AnalyticsMention, error) {
	out := make([]domain.AnalyticsMention, len(p.mentions))
	for i, m := range p.mentions {
		m.OfficialAccountID = acc.ID
		m.Platform = acc.Platform
		m.ExternalID = m.ExternalID + "-" + acc.Handle
		out[i] = m
	}
	return out, nil
}
func (p *fakeProvider) Health(ctx context.Context) error { return p.healthErr }

// --- scheduler --------------------------------------------------------------

func TestScrapeSchedulerRunsDueJob(t *testing.T) {
	store := newFakeScrapeStore()
	tgt, _ := store.UpsertTarget(context.Background(), domain.Target{
		Kind:       domain.TargetKindPost,
		Platform:   domain.PlatformInstagram,
		ExternalID: "ig-1",
		URL:        "https://instagram.com/p/1",
	})
	acc := "account-1"
	job, _ := store.CreateScrapeJob(context.Background(), domain.ScrapeJob{
		Type:        domain.JobTypeScrapeMetric,
		TargetID:    tgt.ID,
		AccountID:   &acc,
		ScheduledAt: time.Now().Add(-time.Minute), // due
	})

	runner := &fakeRunner{out: port.ApifyOutput{Status: "SUCCEEDED", RunID: "r1"}}
	s := NewScrapeScheduler(store, runner, ScrapeSchedulerConfig{
		JitterMin: time.Millisecond, JitterMax: 10 * time.Millisecond,
	})

	if err := s.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(runner.runs) != 1 || runner.runs[0].ScrapeJobID != job.ID {
		t.Fatalf("runner should have run the due job once, got %d runs", len(runner.runs))
	}
	got, _ := store.GetScrapeJob(context.Background(), job.ID)
	if got.Status != domain.JobStatusSuccess {
		t.Fatalf("job should be SUCCESS, got %s", got.Status)
	}
}

func TestScrapeSchedulerSkipsFutureJob(t *testing.T) {
	store := newFakeScrapeStore()
	tgt, _ := store.UpsertTarget(context.Background(), domain.Target{
		Kind:       domain.TargetKindPost,
		Platform:   domain.PlatformInstagram,
		ExternalID: "ig-2",
		URL:        "https://instagram.com/p/2",
	})
	acc := "account-2"
	store.CreateScrapeJob(context.Background(), domain.ScrapeJob{
		Type:        domain.JobTypeScrapeMetric,
		TargetID:    tgt.ID,
		AccountID:   &acc,
		ScheduledAt: time.Now().Add(time.Hour), // not due yet (jitter window)
	})

	runner := &fakeRunner{}
	s := NewScrapeScheduler(store, runner, ScrapeSchedulerConfig{
		JitterMin: time.Millisecond, JitterMax: 10 * time.Millisecond,
	})
	if err := s.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("future job must not run, got %d runs", len(runner.runs))
	}
}

func TestScrapeSchedulerRateLimitBackoff(t *testing.T) {
	store := newFakeScrapeStore()
	tgt, _ := store.UpsertTarget(context.Background(), domain.Target{
		Kind:       domain.TargetKindPost,
		Platform:   domain.PlatformInstagram,
		ExternalID: "ig-3",
		URL:        "https://instagram.com/p/3",
	})
	acc := "account-3"
	job, _ := store.CreateScrapeJob(context.Background(), domain.ScrapeJob{
		Type:        domain.JobTypeScrapeMetric,
		TargetID:    tgt.ID,
		AccountID:   &acc,
		ScheduledAt: time.Now().Add(-time.Minute),
	})

	runner := &fakeRunner{out: port.ApifyOutput{Status: "FAILED", Error: "rate limit exceeded"}}
	s := NewScrapeScheduler(store, runner, ScrapeSchedulerConfig{
		JitterMin:   time.Millisecond,
		JitterMax:   10 * time.Millisecond,
		BackoffBase: time.Minute,
		MaxAttempts: 3,
	})

	if err := s.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	got, _ := store.GetScrapeJob(context.Background(), job.ID)
	if got.Status != domain.JobStatusPending {
		t.Fatalf("rate-limited job should be requeued as PENDING, got %s", got.Status)
	}
	if got.Attempts != 1 {
		t.Fatalf("attempt should be 1, got %d", got.Attempts)
	}
	// The reschedule must push scheduled_at into the future, not leave it due.
	if !got.ScheduledAt.After(time.Now()) {
		t.Fatalf("requeued job should be scheduled in the future, got %v", got.ScheduledAt)
	}

	// Exhausting attempts must terminate as FAILED, never loop forever. Each
	// iteration fast-forwards the job's scheduled_at to "now" to simulate the
	// backoff window elapsing.
	for i := 0; i < 5; i++ {
		cur, _ := store.GetScrapeJob(context.Background(), job.ID)
		if cur.Status == domain.JobStatusFailed {
			break
		}
		store.RescheduleScrapeJob(context.Background(), job.ID, time.Now().Add(-time.Minute))
		if err := s.tick(context.Background()); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	got, _ = store.GetScrapeJob(context.Background(), job.ID)
	if got.Status != domain.JobStatusFailed {
		t.Fatalf("job should eventually FAILED after max attempts, got %s", got.Status)
	}
}

// --- ingestor ---------------------------------------------------------------

func TestScrapeIngestorIdempotent(t *testing.T) {
	store := newFakeScrapeStore()
	storage := newFakeStorage()
	ing := NewScrapeIngestor(store, storage, nil)

	runID := "apify-run-1"
	body := []byte(`{"platform":"instagram","externalId":"ig-p-1","authorHandle":"brand","authorId":"1","text":"hi","metrics":{"likes":5,"views":10}}`)
	key := "apify/" + runID + "/0.json"
	storage.Put(context.Background(), key, body)
	store.CreateRawPayload(context.Background(), domain.RawPayload{
		ApifyRunID: runID, S3Key: key, Bytes: int64(len(body)),
	})

	res, err := ing.IngestRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if res.PostsUpserted != 1 || res.SnapshotsWritten != 1 {
		t.Fatalf("first ingest counts wrong: %+v", res)
	}

	// Re-running the same run must refresh the same post (idempotent) but writes
	// another metric sample, because the hypertable is append-only history.
	res2, err := ing.IngestRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("ingest again: %v", err)
	}
	if res2.PostsUpserted != 1 {
		t.Fatalf("second ingest changed post count: %+v", res2)
	}
	// Still exactly one post row (idempotent upsert).
	posts, _ := store.ListPosts(context.Background(), nil, nil)
	if len(posts) != 1 {
		t.Fatalf("expected 1 post after re-ingest, got %d", len(posts))
	}
}

func TestScrapeIngestorSkipsMalformed(t *testing.T) {
	store := newFakeScrapeStore()
	storage := newFakeStorage()
	ing := NewScrapeIngestor(store, storage, nil)

	runID := "apify-run-2"
	good := []byte(`{"platform":"instagram","externalId":"ig-p-2","authorHandle":"brand","authorId":"2"}`)
	bad := []byte(`{not-json`)
	goodKey := "apify/" + runID + "/0.json"
	badKey := "apify/" + runID + "/1.json"
	storage.Put(context.Background(), goodKey, good)
	storage.Put(context.Background(), badKey, bad)
	store.CreateRawPayload(context.Background(), domain.RawPayload{ApifyRunID: runID, S3Key: goodKey, Bytes: int64(len(good))})
	store.CreateRawPayload(context.Background(), domain.RawPayload{ApifyRunID: runID, S3Key: badKey, Bytes: int64(len(bad))})

	res, err := ing.IngestRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if res.PostsUpserted != 1 {
		t.Fatalf("malformed item should be skipped, got %d posts", res.PostsUpserted)
	}
}

// --- analytics ingestor -------------------------------------------------------

// perAccountProvider fails for one specific handle so a run ends PARTIAL.
type perAccountProvider struct {
	failHandle string
	inner      *fakeProvider
}

var _ port.AnalyticsProvider = (*perAccountProvider)(nil)

func (p *perAccountProvider) FetchMetrics(ctx context.Context, acc domain.OfficialAccount) (domain.AnalyticsSnapshot, error) {
	if acc.Handle == p.failHandle {
		return domain.AnalyticsSnapshot{}, context.DeadlineExceeded
	}
	return p.inner.FetchMetrics(ctx, acc)
}
func (p *perAccountProvider) FetchMentions(ctx context.Context, acc domain.OfficialAccount) ([]domain.AnalyticsMention, error) {
	return p.inner.FetchMentions(ctx, acc)
}
func (p *perAccountProvider) Health(ctx context.Context) error { return p.inner.Health(ctx) }

func TestAnalyticsIngestorPartialRun(t *testing.T) {
	store := newFakeAnalyticsStore()
	acc1 := seedOfficial(store, "brand-a")
	acc2 := seedOfficial(store, "brand-b")

	prov := &perAccountProvider{
		failHandle: acc2.Handle,
		inner: &fakeProvider{
			metrics:  domain.AnalyticsSnapshot{Provider: domain.AnalyticsProviderA},
			mentions: []domain.AnalyticsMention{{ExternalID: "m1", Text: "hi", URL: "https://ig/1"}},
		},
	}
	ing := NewAnalyticsIngestor(store, prov, AnalyticsIngestorConfig{})

	run, err := ing.IngestNow(context.Background())
	if err != nil {
		t.Fatalf("ingest now: %v", err)
	}
	if run.Status != domain.IngestStatusPartial {
		t.Fatalf("one failing account should make the run PARTIAL, got %s", run.Status)
	}
	if run.AccountsOk != 1 || run.AccountsErr != 1 {
		t.Fatalf("counts wrong: ok=%d err=%d", run.AccountsOk, run.AccountsErr)
	}
	if run.ErrorClass == nil || *run.ErrorClass != "TRANSIENT" {
		t.Fatalf("error class should be TRANSIENT, got %+v", run.ErrorClass)
	}
	if run.FinishedAt == nil {
		t.Fatal("finished_at must be stamped")
	}

	// The healthy account got a snapshot; the failing one did not.
	snaps, _ := store.ListAnalyticsSnapshots(context.Background(), acc1.ID, time.Time{}, time.Now().Add(time.Hour))
	if len(snaps) != 1 {
		t.Fatalf("healthy account should have 1 snapshot, got %d", len(snaps))
	}
	snaps, _ = store.ListAnalyticsSnapshots(context.Background(), acc2.ID, time.Time{}, time.Now().Add(time.Hour))
	if len(snaps) != 0 {
		t.Fatalf("failing account should have 0 snapshots, got %d", len(snaps))
	}

	// The latest-run lookup must return this run (freshness badge path).
	latest, err := store.GetLatestAnalyticsIngestRun(context.Background())
	if err != nil || latest.ID != run.ID {
		t.Fatalf("latest run wrong: %+v err=%v", latest, err)
	}
}

func TestAnalyticsIngestorMarksFetchedOnlyOnSuccess(t *testing.T) {
	store := newFakeAnalyticsStore()
	acc := seedOfficial(store, "brand-fresh")

	prov := &perAccountProvider{
		failHandle: acc.Handle,
		inner:      &fakeProvider{metrics: domain.AnalyticsSnapshot{Provider: domain.AnalyticsProviderA}},
	}
	ing := NewAnalyticsIngestor(store, prov, AnalyticsIngestorConfig{})

	if _, err := ing.IngestNow(context.Background()); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	// A failed pull must NOT stamp last_fetched_at, otherwise the freshness
	// badge would claim data is present when it is not.
	got, _ := store.GetOfficialAccount(context.Background(), acc.ID)
	if got.LastFetchedAt != nil {
		t.Fatal("failed account must not be marked fetched")
	}

	// Flip the account to healthy and re-run: now it must be stamped.
	prov.failHandle = ""
	if _, err := ing.IngestNow(context.Background()); err != nil {
		t.Fatalf("ingest again: %v", err)
	}
	got, _ = store.GetOfficialAccount(context.Background(), acc.ID)
	if got.LastFetchedAt == nil {
		t.Fatal("successful account must be marked fetched")
	}
}

func TestAnalyticsIngestorEmptyFleetIsSuccess(t *testing.T) {
	store := newFakeAnalyticsStore()
	ing := NewAnalyticsIngestor(store, &fakeProvider{}, AnalyticsIngestorConfig{})

	run, err := ing.IngestNow(context.Background())
	if err != nil {
		t.Fatalf("ingest now: %v", err)
	}
	if run.Status != domain.IngestStatusSuccess {
		t.Fatalf("empty fleet should be SUCCESS not FAILED, got %s", run.Status)
	}
}

func TestAnalyticsIngestorUnconfiguredProviderFails(t *testing.T) {
	store := newFakeAnalyticsStore()
	ing := NewAnalyticsIngestor(store, nil, AnalyticsIngestorConfig{})

	run, err := ing.IngestNow(context.Background())
	if err != nil {
		t.Fatalf("ingest now: %v", err)
	}
	if run.Status != domain.IngestStatusFailed {
		t.Fatalf("missing provider should FAILED, got %s", run.Status)
	}
}
