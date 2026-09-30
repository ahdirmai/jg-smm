package port

import (
	"context"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// ActionStore (P3-01) covers the action engine tables: ActionJob (the queue)
// and ActionLog (the per-attempt verdict). It is deliberately a separate store
// from ScrapeStore: the action path is written by worker callbacks, the scrape
// path by the ingestor, and keeping them apart stops a callback bug from
// corrupting scrape history.
type ActionStore interface {
	// Action jobs (the queue). Claim is FIFO per account, same shape as the
	// scrape scheduler.
	CreateActionJob(ctx context.Context, j domain.ActionJob) (domain.ActionJob, error)
	GetActionJob(ctx context.Context, id string) (domain.ActionJob, error)
	ClaimNextActionJob(ctx context.Context, accountID, workerID string) (domain.ActionJob, error)
	CompleteActionJob(ctx context.Context, id string, status domain.JobStatus, errMsg *string) (domain.ActionJob, error)
	// SetActionJobTemplate records which template produced a job's text, once a
	// template exists to record. The row is created before the text is composed,
	// so this is a post-claim write rather than an INSERT field.
	SetActionJobTemplate(ctx context.Context, id, templateID string) error
	RescheduleActionJob(ctx context.Context, id string, scheduledAt time.Time) (domain.ActionJob, error)
	// SetPendingActionJobsScheduledAt pulls every PENDING job forward to one
	// instant in a single statement and reports how many rows moved. It is the
	// whole of "run pending now": paging a set while rewriting its sort key is
	// how the previous implementation both double-counted and skipped rows.
	SetPendingActionJobsScheduledAt(ctx context.Context, scheduledAt time.Time) (int64, error)
	ListActionJobs(ctx context.Context, limit, offset *int) ([]domain.ActionJob, error)
	ListActionJobsByStatus(ctx context.Context, status domain.JobStatus, limit, offset *int) ([]domain.ActionJob, error)
	ListActionJobsByAccount(ctx context.Context, accountID string, limit, offset *int) ([]domain.ActionJob, error)
	// SweepStaleRunningJobs requeues jobs stuck in RUNNING for longer than
	// staleAfter. A worker that dies before its callback leaves no other
	// writer, so without this a job is stranded in "Verifying" permanently.
	SweepStaleRunningJobs(ctx context.Context, staleAfter time.Duration) (int64, error)

	// Action logs (the verdict). UpsertActionLog is the callback heart: RUNNING
	// and its terminal verdict write the same (job, attempt) row.
	UpsertActionLog(ctx context.Context, l domain.ActionLog) (domain.ActionLog, error)
	GetActionLog(ctx context.Context, jobID string, attempt int) (domain.ActionLog, error)
	ListActionLogsByJob(ctx context.Context, jobID string) ([]domain.ActionLog, error)
	// LatestActionLogsByJobs is the queue view's read (P3-13): the newest
	// attempt of each job in one round trip, so a page never runs N queries.
	LatestActionLogsByJobs(ctx context.Context, jobIDs []string) ([]domain.ActionLog, error)
	ListActionLogsByErrorClass(ctx context.Context, class domain.ErrorClass, limit, offset *int) ([]domain.ActionLog, error)
}
