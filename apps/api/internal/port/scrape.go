package port

import (
	"context"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// P2 store contracts. Each store is one subject area; services depend on the
// port, the sqlc-backed implementation lives in internal/repository.
//
// Pagination uses *int params so nil = "no limit/first page" is expressible
// without a sentinel value; repos clamp nil to a default page size.

// ScrapeStore covers the scrape pipeline tables: target resolution, content
// upsert (idempotent by (platform, external_id)), job bookkeeping and the
// metric time-series.
type ScrapeStore interface {
	// Targets.
	UpsertTarget(ctx context.Context, t domain.Target) (domain.Target, error)
	GetTarget(ctx context.Context, id string) (domain.Target, error)
	GetTargetByExternalID(ctx context.Context, p domain.Platform, externalID string) (domain.Target, error)
	LinkTargetPost(ctx context.Context, targetID, postID string) error
	LinkTargetComment(ctx context.Context, targetID, commentID string) error
	ListTargets(ctx context.Context, limit, offset *int) ([]domain.Target, error)

	// Content (idempotent upserts; dedup by (platform, external_id)).
	UpsertPost(ctx context.Context, p domain.Post) (domain.Post, error)
	GetPost(ctx context.Context, id string) (domain.Post, error)
	GetPostByExternalID(ctx context.Context, p domain.Platform, externalID string) (domain.Post, error)
	ListPosts(ctx context.Context, limit, offset *int) ([]domain.Post, error)
	ListTopPosts(ctx context.Context, p domain.Platform, metric string, limit int) ([]domain.Post, error)
	ListRecentPosts(ctx context.Context, p domain.Platform, limit int) ([]domain.Post, error)
	UpsertComment(ctx context.Context, c domain.Comment) (domain.Comment, error)
	GetComment(ctx context.Context, id string) (domain.Comment, error)
	ListCommentsByPost(ctx context.Context, postID string, limit, offset *int) ([]domain.Comment, error)
	CountCommentsByPost(ctx context.Context, postID string) (int, error)

	// Scrape jobs.
	CreateScrapeJob(ctx context.Context, j domain.ScrapeJob) (domain.ScrapeJob, error)
	GetScrapeJob(ctx context.Context, id string) (domain.ScrapeJob, error)
	ClaimNextScrapeJob(ctx context.Context, accountID string) (domain.ScrapeJob, error)
	ListScrapeJobs(ctx context.Context, limit, offset *int) ([]domain.ScrapeJob, error)
	ListScrapeJobsByStatus(ctx context.Context, status domain.JobStatus, limit, offset *int) ([]domain.ScrapeJob, error)
	ListPendingScrapeJobsByAccount(ctx context.Context, accountID string, limit int) ([]domain.ScrapeJob, error)
	RescheduleScrapeJob(ctx context.Context, id string, scheduledAt time.Time) (domain.ScrapeJob, error)
	CompleteScrapeJob(ctx context.Context, id string, status domain.JobStatus, errMsg *string) (domain.ScrapeJob, error)

	// Apify runs + raw payload pointers.
	CreateApifyRun(ctx context.Context, r domain.ApifyRun) (domain.ApifyRun, error)
	UpdateApifyRun(ctx context.Context, r domain.ApifyRun, finished bool) (domain.ApifyRun, error)
	CreateRawPayload(ctx context.Context, p domain.RawPayload) (domain.RawPayload, error)
	ListRawPayloadsByRunID(ctx context.Context, apifyRunID string) ([]string, error)

	// Metric time-series.
	CreateMetricSnapshot(ctx context.Context, s domain.MetricSnapshot) error
	ListMetricSnapshots(ctx context.Context, postID string, from, to time.Time) ([]domain.MetricSnapshot, error)
	LatestMetricSnapshots(ctx context.Context) ([]domain.MetricSnapshot, error)
	TopPostsByMetric(ctx context.Context, metric string, limit int) ([]domain.MetricSnapshot, error)
}

// AnalyticsStore covers official-account analytics: the monitored accounts
// themselves, their metric time-series, mentions, and the ingest run audit
// trail. This path is fed by a 3rd-party provider and is fully disjoint from
// the worker/action path.
type AnalyticsStore interface {
	// Official accounts (CRUD).
	CreateOfficialAccount(ctx context.Context, a domain.OfficialAccount) (domain.OfficialAccount, error)
	GetOfficialAccount(ctx context.Context, id string) (domain.OfficialAccount, error)
	GetOfficialAccountByHandle(ctx context.Context, p domain.Platform, handle string) (domain.OfficialAccount, error)
	ListOfficialAccounts(ctx context.Context, limit, offset *int) ([]domain.OfficialAccount, error)
	ListOfficialAccountsByPlatform(ctx context.Context, p domain.Platform, limit, offset *int) ([]domain.OfficialAccount, error)
	CountOfficialAccountsByPlatform(ctx context.Context, p domain.Platform) (int, error)
	UpdateOfficialAccount(ctx context.Context, a domain.OfficialAccount) (domain.OfficialAccount, error)
	TouchOfficialAccountFetched(ctx context.Context, id string) error
	ArchiveOfficialAccount(ctx context.Context, id string) (domain.OfficialAccount, error)
	ListOfficialAccountsForIngest(ctx context.Context, provider domain.AnalyticsProvider, limit int) ([]domain.OfficialAccount, error)

	// Snapshots (idempotent upsert by (account, ts, provider)).
	UpsertAnalyticsSnapshot(ctx context.Context, s domain.AnalyticsSnapshot) (domain.AnalyticsSnapshot, error)
	ListAnalyticsSnapshots(ctx context.Context, accountID string, from, to time.Time) ([]domain.AnalyticsSnapshot, error)
	AnalyticsOverview(ctx context.Context, p domain.Platform, window time.Duration) ([]domain.AnalyticsSnapshot, error)
	AnalyticsTrendByPlatform(ctx context.Context, p domain.Platform, metric string, window time.Duration) ([]domain.TrendPoint, error)
	AnalyticsTrendByAccount(ctx context.Context, accountID string, metric string, window time.Duration) ([]domain.TrendPoint, error)

	// Mentions.
	UpsertAnalyticsMention(ctx context.Context, m domain.AnalyticsMention) (domain.AnalyticsMention, error)
	ListAnalyticsMentionsByAccount(ctx context.Context, accountID string, limit, offset *int) ([]domain.AnalyticsMention, error)
	ListAnalyticsMentionsByPlatform(ctx context.Context, p domain.Platform, limit, offset *int) ([]domain.AnalyticsMention, error)

	// Ingest run audit trail.
	CreateAnalyticsIngestRun(ctx context.Context, provider domain.AnalyticsProvider, scope string) (domain.AnalyticsIngestRun, error)
	UpdateAnalyticsIngestRun(ctx context.Context, r domain.AnalyticsIngestRun) (domain.AnalyticsIngestRun, error)
	ListAnalyticsIngestRuns(ctx context.Context, limit, offset *int) ([]domain.AnalyticsIngestRun, error)
	GetLatestAnalyticsIngestRun(ctx context.Context) (domain.AnalyticsIngestRun, error)
}
