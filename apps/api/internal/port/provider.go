package port

import (
	"context"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// P2 provider-side ports. Both Apify (scrape) and the analytics provider are
// external HTTP services behind a port so the domain never imports an HTTP
// client and so a fake can stand in for tests and local dev (no network).

// RawStorage is the object-store backend for raw scrape payloads (MinIO in
// every environment). Only Put is needed on the write path; the ingestor reads
// via Get.
type RawStorage interface {
	Put(ctx context.Context, key string, payload []byte) (int64, error)
	Get(ctx context.Context, key string) ([]byte, error)
}

// ApifyRunner runs one Apify actor and blocks until it terminates. The scrape
// scheduler drives this: it claims a job, runs the actor for the target's
// platform, then hands the run id + output keys to the ingest pipeline.
type ApifyRunner interface {
	// Run starts an actor for a scrape job and returns when the actor finishes
	// (or the context dies). The returned ApifyRun carries the actor/run ids for
	// the audit trail plus the S3 keys of every dataset item the run produced.
	Run(ctx context.Context, in ApifyInput) (ApifyOutput, error)
}

// ApifyInput is the job the scheduler hands to the runner.
type ApifyInput struct {
	ScrapeJobID string
	ActorID     string // the Apify actor id for the target's platform.
	TargetURL   string
	// AccountHandle is the worker account the scrape is attributed to; Apify
	// uses it to make the scrape look like a logged-in browser, never as a
	// credential (the actor gets no password).
	AccountHandle string
	// MaxItems caps the dataset rows pulled back (budget guard).
	MaxItems int
}

// ApifyOutput is the terminal state of one actor run.
type ApifyOutput struct {
	RunID    string
	Status   string
	ItemKeys []string // S3 keys of the raw dataset items.
	Error    string
}

// ScrapeIngestor normalizes raw actor output into domain entities. It is the
// second half of P2-04: raw payloads land in object storage, this turns them
// into Postgres rows idempotently.
type ScrapeIngestor interface {
	// IngestRun reads the items of one Apify run, writes the raw payload
	// pointers, and upserts posts/comments/metric snapshots. Re-running it on the
	// same run must be a no-op on row counts (idempotent by (platform,
	// external_id)).
	IngestRun(ctx context.Context, runID string) (ScrapeIngestResult, error)
}

// ScrapeIngestResult is the outcome of normalizing one run.
type ScrapeIngestResult struct {
	PostsUpserted    int
	CommentsUpserted int
	SnapshotsWritten int
	BytesIngested    int64
}

// AnalyticsProvider is the 3rd-party source of official-account metrics (PRD
// F5). The domain never knows which provider it is talking to: the adapter
// translates provider payloads into the domain shape. P2-11 ships a stub
// adapter; a real one replaces it without touching the ingestor.
type AnalyticsProvider interface {
	// FetchMetrics pulls the latest metrics for one official account. The
	// returned snapshot is normalized to the domain shape by the adapter.
	FetchMetrics(ctx context.Context, acc domain.OfficialAccount) (domain.AnalyticsSnapshot, error)
	// FetchMentions pulls new mentions since the last fetch.
	FetchMentions(ctx context.Context, acc domain.OfficialAccount) ([]domain.AnalyticsMention, error)
	// Health pings the provider so the ingestor can report a partial run with a
	// clear error class instead of a generic failure.
	Health(ctx context.Context) error
}
