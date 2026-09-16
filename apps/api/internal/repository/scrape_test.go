package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/repository/sqlcgen"
)

// P2 repository integration tests. Same contract as repo_test.go: real
// Postgres, skipped unless SMM_TEST_DB=1. The whole P2 surface is asserted here
// against the DDL that actually runs in production (no fakes), because the
// interesting behaviour lives in the SQL: idempotent upserts, the FIFO claim,
// hypertable round trips and the unique constraints.

// newP2TestRepos builds the scrape + analytics repos over a real pool.
// p2Suffix makes every fixture id unique per test so repeated `go test`
// invocations against the same shared database do not collide on the unique
// constraints (post/platform, official account handle, ...). It is stable
// within one test (derived from the test name) so a lookup can re-find the row
// it just inserted.
func p2Suffix(t *testing.T) string {
	h := sha256.Sum256([]byte(t.Name()))
	return hex.EncodeToString(h[:])[:10]
}

func newP2TestRepos(t *testing.T) (*ScrapeRepo, *AnalyticsRepo) {
	t.Helper()
	if os.Getenv("SMM_TEST_DB") != "1" {
		t.Skip("set SMM_TEST_DB=1 to run repository integration tests")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://smm:smm@localhost:24543/smm?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	q := sqlcgen.New(pool)
	// The P2 tables hold pipeline data only. Clearing them makes every test
	// deterministic against a shared dev database, so a unique constraint never
	// fires on a row left behind by a previous run.
	if _, err := pool.Exec(context.Background(), `
		TRUNCATE TABLE
		  analytics_ingest_run, analytics_mention, analytics_snapshot,
		  official_account,
		  raw_payload, apify_run, scrape_job, target, comment, post
		CASCADE`); err != nil {
		t.Fatalf("truncate p2 tables: %v", err)
	}
	return NewScrapeRepo(q), NewAnalyticsRepo(q)
}

// p2SeedAccount inserts an account row the scrape jobs can reference, and
// returns its id. FKs from scrape_job -> account are nullable but the scheduler
// tests want a real account so the claim path is exercised end to end.
func p2SeedAccount(t *testing.T, pool *pgxpool.Pool, username string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := pool.QueryRow(ctx,
		`INSERT INTO account (platform, username, password_enc, auth_status, status)
		 VALUES ('INSTAGRAM', $1, decode('00', 'hex'), 'AUTHENTICATING', 'PENDING')
		 ON CONFLICT (platform, username) DO UPDATE SET username = EXCLUDED.username
		 RETURNING id::text`, username).Scan(&id)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	return id
}

// --- scrape: target + content ------------------------------------------------

func TestScrapeTargetRoundTrip(t *testing.T) {
	scrape, _ := newP2TestRepos(t)
	ctx := context.Background()

	tgt, err := scrape.UpsertTarget(ctx, domain.Target{
		Kind:       domain.TargetKindPost,
		Platform:   domain.PlatformInstagram,
		ExternalID: "ig-post-target-" + p2Suffix(t),
		URL:        "https://instagram.com/p/abc",
		Meta:       json.RawMessage(`{"source":"manual"}`),
	})
	if err != nil {
		t.Fatalf("upsert target: %v", err)
	}
	if tgt.ID == "" || tgt.PostID != nil || tgt.CommentID != nil {
		t.Fatalf("new target should have no entity ref, got %+v", tgt)
	}

	// Idempotent: a second upsert returns the SAME row (same id) with refreshed
	// meta, never a duplicate.
	tgt2, err := scrape.UpsertTarget(ctx, domain.Target{
		Kind:       domain.TargetKindPost,
		Platform:   domain.PlatformInstagram,
		ExternalID: "ig-post-target-" + p2Suffix(t),
		URL:        "https://instagram.com/p/abc",
		Meta:       json.RawMessage(`{"source":"rescrape"}`),
	})
	if err != nil {
		t.Fatalf("upsert target again: %v", err)
	}
	if tgt2.ID != tgt.ID {
		t.Fatalf("upsert changed target id: %s -> %s", tgt.ID, tgt2.ID)
	}

	got, err := scrape.GetTargetByExternalID(ctx, domain.PlatformInstagram, "ig-post-target-"+p2Suffix(t))
	if err != nil {
		t.Fatalf("get by external id: %v", err)
	}
	if got.URL != "https://instagram.com/p/abc" {
		t.Fatalf("url mismatch: %s", got.URL)
	}

	// Linking the target to a post is what makes it actionable.
	post, err := scrape.UpsertPost(ctx, domain.Post{
		Platform:     domain.PlatformInstagram,
		ExternalID:   "ig-post-target-" + p2Suffix(t),
		AuthorHandle: "brand",
		AuthorID:     "123",
		Text:         ptr("hello"),
		Metrics:      json.RawMessage(`{"likes":5}`),
	})
	if err != nil {
		t.Fatalf("upsert post: %v", err)
	}
	if err := scrape.LinkTargetPost(ctx, tgt.ID, post.ID); err != nil {
		t.Fatalf("link target post: %v", err)
	}
	got, err = scrape.GetTarget(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("get target after link: %v", err)
	}
	if got.PostID == nil || *got.PostID != post.ID {
		t.Fatalf("target not linked to post: %+v", got)
	}
}

func TestScrapePostUpsertIdempotent(t *testing.T) {
	scrape, _ := newP2TestRepos(t)
	ctx := context.Background()

	p1, err := scrape.UpsertPost(ctx, domain.Post{
		Platform:     domain.PlatformThreads,
		ExternalID:   "threads-post-" + p2Suffix(t),
		AuthorHandle: "brand",
		AuthorID:     "456",
		Metrics:      json.RawMessage(`{"likes":1}`),
	})
	if err != nil {
		t.Fatalf("upsert post: %v", err)
	}
	p2, err := scrape.UpsertPost(ctx, domain.Post{
		Platform:     domain.PlatformThreads,
		ExternalID:   "threads-post-" + p2Suffix(t),
		AuthorHandle: "brand",
		AuthorID:     "456",
		Metrics:      json.RawMessage(`{"likes":99}`),
	})
	if err != nil {
		t.Fatalf("upsert post again: %v", err)
	}
	if p1.ID != p2.ID {
		t.Fatalf("re-scrape created a duplicate post: %s != %s", p1.ID, p2.ID)
	}
	got, err := scrape.GetPostByExternalID(ctx, domain.PlatformThreads, "threads-post-"+p2Suffix(t))
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if string(got.Metrics) != `{"likes": 99}` {
		t.Fatalf("metrics not refreshed: %s", got.Metrics)
	}

	// Top-N by a jsonb metric: the refresh above must make this post the top one.
	top, err := scrape.ListTopPosts(ctx, domain.PlatformThreads, "likes", 10)
	if err != nil {
		t.Fatalf("list top posts: %v", err)
	}
	if len(top) == 0 || top[0].ID != p1.ID {
		t.Fatalf("top posts wrong: %d rows, first=%+v", len(top), top)
	}
}

func TestScrapeCommentThread(t *testing.T) {
	scrape, _ := newP2TestRepos(t)
	ctx := context.Background()

	post, err := scrape.UpsertPost(ctx, domain.Post{
		Platform:     domain.PlatformInstagram,
		ExternalID:   "ig-post-comments-" + p2Suffix(t),
		AuthorHandle: "brand",
		AuthorID:     "1",
	})
	if err != nil {
		t.Fatalf("upsert post: %v", err)
	}
	parent, err := scrape.UpsertComment(ctx, domain.Comment{
		PostID:       post.ID,
		Platform:     domain.PlatformInstagram,
		ExternalID:   "ig-c-1",
		AuthorHandle: "user",
		Text:         "top level",
	})
	if err != nil {
		t.Fatalf("upsert comment: %v", err)
	}
	child, err := scrape.UpsertComment(ctx, domain.Comment{
		PostID:       post.ID,
		Platform:     domain.PlatformInstagram,
		ExternalID:   "ig-c-2",
		AuthorHandle: "user",
		Text:         "reply",
		ParentID:     &parent.ID,
	})
	if err != nil {
		t.Fatalf("upsert reply: %v", err)
	}
	if child.ParentID == nil || *child.ParentID != parent.ID {
		t.Fatalf("reply parent not set: %+v", child)
	}
	n, err := scrape.CountCommentsByPost(ctx, post.ID)
	if err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 comments, got %d", n)
	}
}

// --- scrape: FIFO claim + completion -----------------------------------------

func TestScrapeJobFIFOClaim(t *testing.T) {
	scrape, _ := newP2TestRepos(t)
	ctx := context.Background()

	pool := testPool(t)
	accountID := p2SeedAccount(t, pool, "fifo-account-"+p2Suffix(t))

	tgt, err := scrape.UpsertTarget(ctx, domain.Target{
		Kind:       domain.TargetKindPost,
		Platform:   domain.PlatformInstagram,
		ExternalID: "ig-fifo-target-" + p2Suffix(t),
		URL:        "https://instagram.com/p/fifo",
	})
	if err != nil {
		t.Fatalf("upsert target: %v", err)
	}

	// Enqueue two jobs with different scheduled_at so FIFO order is decidable.
	base := time.Now().UTC()
	later, err := scrape.CreateScrapeJob(ctx, domain.ScrapeJob{
		Type:        domain.JobTypeScrapeMetric,
		TargetID:    tgt.ID,
		AccountID:   &accountID,
		ScheduledAt: base.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create job later: %v", err)
	}
	earlier, err := scrape.CreateScrapeJob(ctx, domain.ScrapeJob{
		Type:        domain.JobTypeScrapeMetric,
		TargetID:    tgt.ID,
		AccountID:   &accountID,
		ScheduledAt: base.Add(-time.Hour), // due now
	})
	if err != nil {
		t.Fatalf("create job earlier: %v", err)
	}

	// Nothing due for a *different* account -> ErrNotFound, not an error.
	if _, err := scrape.ClaimNextScrapeJob(ctx, "00000000-0000-0000-0000-000000000099"); err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound for empty account, got %v", err)
	}

	// Claim returns the due job ordered by scheduled_at: earlier first.
	claimed, err := scrape.ClaimNextScrapeJob(ctx, accountID)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.ID != earlier.ID {
		t.Fatalf("FIFO broken: claimed %s, expected %s", claimed.ID, earlier.ID)
	}
	if claimed.Status != domain.JobStatusRunning {
		t.Fatalf("claimed job not RUNNING: %s", claimed.Status)
	}
	if claimed.Attempts != 1 {
		t.Fatalf("claim should bump attempts to 1, got %d", claimed.Attempts)
	}

	// The future job must NOT be claimable yet.
	if _, err := scrape.ClaimNextScrapeJob(ctx, accountID); err != domain.ErrNotFound {
		t.Fatalf("future job should not be claimable, got %v", err)
	}
	_ = later

	// Completion flips status terminal and stamps finished_at.
	done, err := scrape.CompleteScrapeJob(ctx, claimed.ID, domain.JobStatusSuccess, nil)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if done.Status != domain.JobStatusSuccess || done.FinishedAt == nil {
		t.Fatalf("job not completed: %+v", done)
	}
	pending, _ := scrape.ListPendingScrapeJobsByAccount(ctx, accountID, 10)
	if len(pending) != 1 || pending[0].ID != later.ID {
		t.Fatalf("pending list should only hold the future job: %d rows", len(pending))
	}
}

// --- scrape: metric time-series ----------------------------------------------

func TestMetricSnapshotSeries(t *testing.T) {
	scrape, _ := newP2TestRepos(t)
	ctx := context.Background()

	post, err := scrape.UpsertPost(ctx, domain.Post{
		Platform:     domain.PlatformInstagram,
		ExternalID:   "ig-post-metrics-" + p2Suffix(t),
		AuthorHandle: "brand",
		AuthorID:     "1",
	})
	if err != nil {
		t.Fatalf("upsert post: %v", err)
	}
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		if err := scrape.CreateMetricSnapshot(ctx, domain.MetricSnapshot{
			PostID: post.ID,
			TS:     now.Add(time.Duration(i) * time.Hour),
			Views:  int64(100 + i),
			Likes:  int64(10 + i),
			Reach:  ptr(int64(1000 + i)),
		}); err != nil {
			t.Fatalf("create snapshot %d: %v", i, err)
		}
	}

	from := now.Add(-time.Hour)
	to := now.Add(24 * time.Hour)
	series, err := scrape.ListMetricSnapshots(ctx, post.ID, from, to)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(series) != 3 {
		t.Fatalf("expected 3 snapshots, got %d", len(series))
	}
	if series[0].Views != 100 {
		t.Fatalf("series not ordered by ts asc: first views %d", series[0].Views)
	}

	top, err := scrape.TopPostsByMetric(ctx, "views", 10)
	if err != nil {
		t.Fatalf("top posts: %v", err)
	}
	if len(top) == 0 {
		t.Fatalf("top posts empty")
	}
	if top[0].Views != 102 {
		t.Fatalf("top post should report latest views 102, got %d", top[0].Views)
	}
}

// --- analytics: official accounts + snapshots + ingest runs -------------------

func TestAnalyticsRoundTrip(t *testing.T) {
	_, analytics := newP2TestRepos(t)
	ctx := context.Background()

	acc, err := analytics.CreateOfficialAccount(ctx, domain.OfficialAccount{
		Platform:    domain.PlatformInstagram,
		Handle:      "brand-official-" + p2Suffix(t),
		DisplayName: ptr("Brand"),
		Provider:    domain.AnalyticsProviderA,
		Tags:        []string{"client"},
	})
	if err != nil {
		t.Fatalf("create official account: %v", err)
	}
	if acc.Status != domain.OfficialAccountActive {
		t.Fatalf("default status should be ACTIVE: %s", acc.Status)
	}

	// Duplicate (platform, handle) is a conflict, not a second row.
	if _, err := analytics.CreateOfficialAccount(ctx, domain.OfficialAccount{
		Platform: domain.PlatformInstagram,
		Handle:   "brand-official-" + p2Suffix(t),
	}); err == nil {
		t.Fatal("duplicate official account should conflict")
	}

	byPlatform, err := analytics.ListOfficialAccountsByPlatform(ctx, domain.PlatformInstagram, nil, nil)
	if err != nil {
		t.Fatalf("list by platform: %v", err)
	}
	if len(byPlatform) != 1 || byPlatform[0].ID != acc.ID {
		t.Fatalf("list by platform wrong: %d rows", len(byPlatform))
	}
	if n, _ := analytics.CountOfficialAccountsByPlatform(ctx, domain.PlatformInstagram); n != 1 {
		t.Fatalf("count by platform: %d", n)
	}

	// A never-fetched active account is stale by definition (freshness badge).
	if !acc.IsStale(time.Now()) {
		t.Fatal("never-fetched account should be stale")
	}

	// Snapshot upsert is idempotent on (account, ts, provider).
	ts := time.Now().UTC().Truncate(time.Second)
	one := ptr(int64(1000))
	s1, err := analytics.UpsertAnalyticsSnapshot(ctx, domain.AnalyticsSnapshot{
		OfficialAccountID: acc.ID,
		Platform:          domain.PlatformInstagram,
		TS:                ts,
		Followers:         one,
		Metrics:           json.RawMessage(`{"src":"a"}`),
		Provider:          domain.AnalyticsProviderA,
	})
	if err != nil {
		t.Fatalf("upsert snapshot: %v", err)
	}
	two := ptr(int64(1500))
	s2, err := analytics.UpsertAnalyticsSnapshot(ctx, domain.AnalyticsSnapshot{
		OfficialAccountID: acc.ID,
		Platform:          domain.PlatformInstagram,
		TS:                ts,
		Followers:         two,
		Provider:          domain.AnalyticsProviderA,
	})
	if err != nil {
		t.Fatalf("upsert snapshot again: %v", err)
	}
	if s1.ID != s2.ID {
		t.Fatalf("re-ingest duplicated snapshot: %s != %s", s1.ID, s2.ID)
	}
	if s2.Followers == nil || *s2.Followers != 1500 {
		t.Fatalf("followers not refreshed: %+v", s2.Followers)
	}

	// The trend query aggregates the series into daily buckets.
	trend, err := analytics.AnalyticsTrendByPlatform(ctx, domain.PlatformInstagram, "followers", 30*24*time.Hour)
	if err != nil {
		t.Fatalf("trend by platform: %v", err)
	}
	if len(trend) == 0 {
		t.Fatalf("trend empty")
	}
	if trend[len(trend)-1].Value != 1500 {
		t.Fatalf("last trend bucket should be 1500, got %d", trend[len(trend)-1].Value)
	}

	// Ingest run audit trail.
	run, err := analytics.CreateAnalyticsIngestRun(ctx, domain.AnalyticsProviderA, "platform:instagram")
	if err != nil {
		t.Fatalf("create ingest run: %v", err)
	}
	if run.Status != domain.IngestStatusRunning {
		t.Fatalf("run should start RUNNING: %s", run.Status)
	}
	run.Status = domain.IngestStatusSuccess
	run.AccountsOk = 1
	closed, err := analytics.UpdateAnalyticsIngestRun(ctx, run)
	if err != nil {
		t.Fatalf("update ingest run: %v", err)
	}
	if closed.Status != domain.IngestStatusSuccess || closed.FinishedAt == nil {
		t.Fatalf("run not closed: %+v", closed)
	}
	latest, err := analytics.GetLatestAnalyticsIngestRun(ctx)
	if err != nil {
		t.Fatalf("latest run: %v", err)
	}
	if latest.ID != closed.ID {
		t.Fatalf("latest run wrong: %s", latest.ID)
	}

	// Archiving keeps the row (history stays queryable) but drops it from ingest.
	arch, err := analytics.ArchiveOfficialAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if arch.Status != domain.OfficialAccountArchived {
		t.Fatalf("not archived: %s", arch.Status)
	}
	forIngest, _ := analytics.ListOfficialAccountsForIngest(ctx, domain.AnalyticsProviderA, 10)
	for _, a := range forIngest {
		if a.ID == acc.ID {
			t.Fatal("archived account returned by ingest list")
		}
	}
}

func TestAnalyticsMentionDedupe(t *testing.T) {
	_, analytics := newP2TestRepos(t)
	ctx := context.Background()

	acc, err := analytics.CreateOfficialAccount(ctx, domain.OfficialAccount{
		Platform: domain.PlatformX,
		Handle:   "brand-x-" + p2Suffix(t),
	})
	if err != nil {
		t.Fatalf("create official account: %v", err)
	}
	posted := time.Now().UTC().Add(-time.Hour)
	m1, err := analytics.UpsertAnalyticsMention(ctx, domain.AnalyticsMention{
		OfficialAccountID: acc.ID,
		Platform:          domain.PlatformX,
		ExternalID:        "x-mention-" + p2Suffix(t),
		Text:              "love this",
		URL:               "https://x.com/status/1",
		PostedAt:          posted,
	})
	if err != nil {
		t.Fatalf("upsert mention: %v", err)
	}
	m2, err := analytics.UpsertAnalyticsMention(ctx, domain.AnalyticsMention{
		OfficialAccountID: acc.ID,
		Platform:          domain.PlatformX,
		ExternalID:        "x-mention-" + p2Suffix(t),
		Text:              "love this (edited)",
		URL:               "https://x.com/status/1",
		PostedAt:          posted,
		Sentiment:         ptr("positive"),
	})
	if err != nil {
		t.Fatalf("upsert mention again: %v", err)
	}
	if m1.ID != m2.ID {
		t.Fatalf("re-ingest duplicated mention: %s != %s", m1.ID, m2.ID)
	}
	if m2.Sentiment == nil || *m2.Sentiment != "positive" {
		t.Fatalf("sentiment not refreshed: %+v", m2.Sentiment)
	}
	list, err := analytics.ListAnalyticsMentionsByAccount(ctx, acc.ID, nil, nil)
	if err != nil {
		t.Fatalf("list mentions: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 mention after dedupe, got %d", len(list))
	}
}
