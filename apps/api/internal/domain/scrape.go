package domain

import (
	"encoding/json"
	"time"
)

// P2 entities: the scrape pipeline and official-account analytics.
//
// Two disjoint subject areas live here:
//   - Scrape: Target/Post/Comment/ScrapeJob/ApifyRun/RawPayload/MetricSnapshot.
//     Content scraped from platforms (via Apify), consumed by the dashboard and
//     by the action engine (a Target is the join point).
//   - Analytics: OfficialAccount/AnalyticsSnapshot/AnalyticsMention/
//     AnalyticsIngestRun. Metrics for monitored brand accounts, fed by a
//     3rd-party provider. An OfficialAccount is NOT an Account: it has no
//     credentials and never performs actions.

// JobType enumerates scrape and action work. It spans P2 (SCRAPE_*, SESSION_
// REFRESH) and P3 (ACTION_*); both share one queue-side enum because a worker
// callback references the same status machine either way.
type JobType string

const (
	JobTypeScrapeLike         JobType = "scrape_like"
	JobTypeScrapeComment      JobType = "scrape_comment"
	JobTypeScrapeMetric       JobType = "scrape_metric"
	JobTypeSessionRefresh     JobType = "session_refresh"
	JobTypeActionLike         JobType = "action_like"
	JobTypeActionComment      JobType = "action_comment"
	JobTypeActionReport       JobType = "action_report"
	JobTypeActionReplyComment JobType = "action_reply_comment"
	JobTypeActionLikeComment  JobType = "action_like_comment"
)

// AllJobTypes lists every job type in a stable order.
var AllJobTypes = []JobType{
	JobTypeScrapeLike,
	JobTypeScrapeComment,
	JobTypeScrapeMetric,
	JobTypeSessionRefresh,
	JobTypeActionLike,
	JobTypeActionComment,
	JobTypeActionReport,
	JobTypeActionReplyComment,
	JobTypeActionLikeComment,
}

// Valid reports whether t is a known job type.
func (t JobType) Valid() bool {
	for _, known := range AllJobTypes {
		if t == known {
			return true
		}
	}
	return false
}

// IsScrape reports whether the job type belongs to the scrape pipeline (P2).
func (t JobType) IsScrape() bool {
	switch t {
	case JobTypeScrapeLike, JobTypeScrapeComment, JobTypeScrapeMetric,
		JobTypeSessionRefresh:
		return true
	}
	return false
}

// JobStatus is the shared state machine for scrape and action jobs.
// PENDING != FAILED: PENDING means a worker never claimed it; FAILED means a
// worker ran it and reported failure. There is no reaper that silently turns
// old PENDING rows into FAILED (ERD notes).
type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusSuccess   JobStatus = "success"
	JobStatusFailed    JobStatus = "failed"
	JobStatusRetry     JobStatus = "retry"
	JobStatusCancelled JobStatus = "cancelled"
)

// IsTerminal reports whether no further state transition is expected.
func (s JobStatus) IsTerminal() bool {
	switch s {
	case JobStatusSuccess, JobStatusFailed, JobStatusCancelled:
		return true
	}
	return false
}

// TargetKind distinguishes a target pointing at a post vs a comment.
type TargetKind string

const (
	TargetKindPost    TargetKind = "post"
	TargetKindComment TargetKind = "comment"
)

// OfficialAccountStatus is the lifecycle of a monitored account. ARCHIVED keeps
// history queryable while excluding the account from fresh ingest runs.
type OfficialAccountStatus string

const (
	OfficialAccountActive   OfficialAccountStatus = "active"
	OfficialAccountPaused   OfficialAccountStatus = "paused"
	OfficialAccountArchived OfficialAccountStatus = "archived"
)

// AnalyticsProvider is provider-agnostic; concrete values are bound when the
// real integration is wired (PRD F5). The domain never branches on a provider.
type AnalyticsProvider string

const (
	AnalyticsProviderA AnalyticsProvider = "thirdparty_a"
	AnalyticsProviderB AnalyticsProvider = "thirdparty_b"
)

// IngestStatus is the state machine of one analytics ingest run.
type IngestStatus string

const (
	IngestStatusPending IngestStatus = "pending"
	IngestStatusRunning IngestStatus = "running"
	IngestStatusSuccess IngestStatus = "success"
	IngestStatusFailed  IngestStatus = "failed"
	IngestStatusPartial IngestStatus = "partial"
)

// AnalyticsMetric names a cross-platform scalar the dashboard can chart. The
// provider may not populate all of them for every platform; missing values stay
// NULL rather than being coerced to zero.
type AnalyticsMetric string

const (
	AnalyticsMetricFollowers    AnalyticsMetric = "followers"
	AnalyticsMetricReach        AnalyticsMetric = "reach"
	AnalyticsMetricViews        AnalyticsMetric = "views"
	AnalyticsMetricMentions     AnalyticsMetric = "mentions"
	AnalyticsMetricEngagements  AnalyticsMetric = "engagements"
	AnalyticsMetricProfileViews AnalyticsMetric = "profile_views"
)

// Target is the join point between scrape and action: one row per platform
// URL/external id. A scrape resolves it to a Post or a Comment; an action is
// dispatched against it. Exactly one of PostID/CommentID is set once resolved.
type Target struct {
	ID         string
	Kind       TargetKind
	Platform   Platform
	ExternalID string
	URL        string
	Meta       json.RawMessage
	PostID     *string
	CommentID  *string
	CreatedAt  time.Time
}

// Post is a scraped post. Deduped by (Platform, ExternalID) so a re-scrape
// refreshes Metrics instead of inserting a duplicate.
type Post struct {
	ID              string
	Platform        Platform
	ExternalID      string
	AuthorHandle    string
	AuthorID        string
	Text            *string
	MediaURLs       []string
	Metrics         json.RawMessage
	ScrapedAt       time.Time
	AuthorAccountID *string
}

// Comment is a scraped comment, optionally a reply (ParentID chain).
type Comment struct {
	ID           string
	PostID       string
	Platform     Platform
	ExternalID   string
	AuthorHandle string
	Text         string
	Metrics      json.RawMessage
	ScrapedAt    time.Time
	ParentID     *string
}

// MetricSnapshot is one time-series sample of a post's metrics. History table;
// Post.Metrics holds only the latest value.
type MetricSnapshot struct {
	ID         string
	PostID     string
	TS         time.Time
	Views      int64
	Likes      int64
	Comments   int64
	Shares     int64
	Reach      *int64
	ReelsViews *int64
}

// ScrapeJob is one unit of scrape work, scheduled FIFO per account.
type ScrapeJob struct {
	ID          string
	Type        JobType
	TargetID    string
	AccountID   *string
	WorkerID    *string
	Status      JobStatus
	ScheduledAt time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
	Attempts    int
	Error       *string
	CreatedAt   time.Time
}

// ApifyRun is one Apify actor execution, 1:1 with a ScrapeJob.
type ApifyRun struct {
	ID          string
	ScrapeJobID string
	ActorID     string
	RunID       string
	Status      string
	StartedAt   time.Time
	FinishedAt  *time.Time
}

// RawPayload points at the raw Apify output held in object storage. Only the S3
// key is persisted; the payload itself never enters the DB (ERD notes).
type RawPayload struct {
	ID         string
	ApifyRunID string
	S3Key      string
	Bytes      int64
	ReceivedAt time.Time
}

// OfficialAccount is a monitored brand/client account on one platform. It has
// no credentials and performs no actions; its metrics come from a provider.
type OfficialAccount struct {
	ID            string
	Platform      Platform
	Handle        string
	DisplayName   *string
	ProfileURL    *string
	AvatarURL     *string
	Status        OfficialAccountStatus
	Provider      AnalyticsProvider
	ProviderRef   *string
	Tags          []string
	LastFetchedAt *time.Time
	CreatedAt     time.Time
}

// AnalyticsSnapshot is one time-series sample of an official account's metrics.
// Metrics holds provider-specific values + the raw payload; the scalars are the
// cross-platform projections that get indexed and charted.
type AnalyticsSnapshot struct {
	ID                string
	OfficialAccountID string
	Platform          Platform
	TS                time.Time
	Followers         *int64
	Reach             *int64
	Views             *int64
	Mentions          *int64
	Engagements       *int64
	ProfileViews      *int64
	Metrics           json.RawMessage
	Provider          AnalyticsProvider
	ProviderRunID     *string
	FetchedAt         time.Time
}

// AnalyticsMention is one mention of an official account, sourced from a
// provider and deduped by (Platform, ExternalID).
type AnalyticsMention struct {
	ID                string
	OfficialAccountID string
	Platform          Platform
	ExternalID        string
	AuthorHandle      *string
	Text              string
	URL               string
	PostedAt          time.Time
	Sentiment         *string
	FetchedAt         time.Time
}

// AnalyticsIngestRun records one attempt to pull metrics from a provider — the
// audit trail for the analytics path, mirroring ProvisionLog for workers.
type AnalyticsIngestRun struct {
	ID          string
	Provider    AnalyticsProvider
	Scope       string // "platform:instagram" | "account:<id>" | "all"
	Status      IngestStatus
	StartedAt   time.Time
	FinishedAt  *time.Time
	AccountsOk  int
	AccountsErr int
	ErrorClass  *string
	Error       *string
}

// TrendPoint is one bucket of a time-series chart: the aggregated metric value
// over a time window. Used by the analytics trend queries (gap-filled daily
// buckets) and by the KPI strip.
type TrendPoint struct {
	Bucket time.Time
	Value  int64
}

// StaleThreshold is how long an analytics view can go unsynced before the
// dashboard's freshness badge flips to "stale" (PRD F5, TICKETS P2-15 AC).
const StaleThreshold = 60 * time.Minute

// IsStale reports whether the last successful fetch is older than the freshness
// threshold. A never-fetched active account is stale by definition.
func (o OfficialAccount) IsStale(now time.Time) bool {
	if o.LastFetchedAt == nil {
		return true
	}
	return now.Sub(*o.LastFetchedAt) > StaleThreshold
}
