package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/repository/sqlcgen"
)

// ActionRepo implements port.ActionStore on top of the sqlc query handle. It
// covers the P3 action engine: the job queue (claimed FIFO per account by a
// worker) and the per-attempt verdict log that worker callbacks write.
type ActionRepo struct {
	q *sqlcgen.Queries
}

// NewActionRepo binds the repo to a sqlc query handle.
func NewActionRepo(q *sqlcgen.Queries) *ActionRepo { return &ActionRepo{q: q} }

var _ port.ActionStore = (*ActionRepo)(nil)

// ---------------------------------------------------------------------------
// action jobs
// ---------------------------------------------------------------------------

// CreateActionJob enqueues one action. status defaults to PENDING and
// scheduled_at to now; the caller only sets the latter to honour cooldown and
// rate-limit gates (P3-09/P3-10). The defaults live here rather than relying
// on the column DEFAULT because the INSERT passes status explicitly.
func (r *ActionRepo) CreateActionJob(ctx context.Context, j domain.ActionJob) (domain.ActionJob, error) {
	if j.Status == "" {
		j.Status = domain.JobStatusPending
	}
	if j.ScheduledAt.IsZero() {
		j.ScheduledAt = time.Now().UTC()
	}
	row, err := r.q.CreateActionJob(ctx, sqlcgen.CreateActionJobParams{
		Type:        jobTypeEnum(j.Type),
		TargetID:    uuidValue(j.TargetID),
		AccountID:   uuidValue(j.AccountID),
		WorkerID:    uuidValue(j.WorkerID),
		Status:      jobStatusEnum(j.Status),
		ScheduledAt: tsPtr(&j.ScheduledAt),
	})
	if err != nil {
		return domain.ActionJob{}, fmt.Errorf("repository.action.CreateActionJob: %w", err)
	}
	return actionJobDomain(row), nil
}

// GetActionJob returns one queued action by id.
func (r *ActionRepo) GetActionJob(ctx context.Context, id string) (domain.ActionJob, error) {
	row, err := r.q.GetActionJobByID(ctx, uuidValue(id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ActionJob{}, domain.ErrNotFound
		}
		return domain.ActionJob{}, fmt.Errorf("repository.action.GetActionJob: %w", err)
	}
	return actionJobDomain(row), nil
}

// ClaimNextActionJob claims the oldest eligible action for one account and
// stamps the claiming worker. Returns ErrNotFound when nothing is claimable so
// the caller's poll loop can idle instead of guessing.
func (r *ActionRepo) ClaimNextActionJob(ctx context.Context, accountID, workerID string) (domain.ActionJob, error) {
	row, err := r.q.ClaimNextActionJob(ctx, sqlcgen.ClaimNextActionJobParams{
		AccountID: uuidValue(accountID),
		WorkerID:  uuidValue(workerID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ActionJob{}, domain.ErrNotFound
		}
		return domain.ActionJob{}, fmt.Errorf("repository.action.ClaimNextActionJob: %w", err)
	}
	return actionJobDomain(row), nil
}

// CompleteActionJob projects a terminal verdict onto the job row so the queue
// can be listed without joining the log. The ActionLog row is the source of
// truth; this is the projection.
func (r *ActionRepo) CompleteActionJob(ctx context.Context, id string, status domain.JobStatus, errMsg *string) (domain.ActionJob, error) {
	row, err := r.q.CompleteActionJob(ctx, sqlcgen.CompleteActionJobParams{
		ID:     uuidValue(id),
		Status: jobStatusEnum(status),
		Error:  errMsg,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ActionJob{}, domain.ErrNotFound
		}
		return domain.ActionJob{}, fmt.Errorf("repository.action.CompleteActionJob: %w", err)
	}
	return actionJobDomain(row), nil
}

// RescheduleActionJob returns a retryable job to the queue at a future time.
func (r *ActionRepo) RescheduleActionJob(ctx context.Context, id string, scheduledAt time.Time) (domain.ActionJob, error) {
	row, err := r.q.RescheduleActionJob(ctx, sqlcgen.RescheduleActionJobParams{
		ID:          uuidValue(id),
		ScheduledAt: tsPtr(&scheduledAt),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ActionJob{}, domain.ErrNotFound
		}
		return domain.ActionJob{}, fmt.Errorf("repository.action.RescheduleActionJob: %w", err)
	}
	return actionJobDomain(row), nil
}

// ListActionJobs returns the newest actions first (the dashboard's queue view).
func (r *ActionRepo) ListActionJobs(ctx context.Context, limit, offset *int) ([]domain.ActionJob, error) {
	l, o := ptrPage(limit, offset)
	rows, err := r.q.ListActionJobs(ctx, sqlcgen.ListActionJobsParams{Limit: l, Offset: o})
	if err != nil {
		return nil, fmt.Errorf("repository.action.ListActionJobs: %w", err)
	}
	out := make([]domain.ActionJob, 0, len(rows))
	for _, row := range rows {
		out = append(out, actionJobDomain(row))
	}
	return out, nil
}

// ListActionJobsByStatus returns actions in one status (e.g. the PENDING queue
// depth, or the FAILED triage view).
func (r *ActionRepo) ListActionJobsByStatus(ctx context.Context, status domain.JobStatus, limit, offset *int) ([]domain.ActionJob, error) {
	l, o := ptrPage(limit, offset)
	rows, err := r.q.ListActionJobsByStatus(ctx, sqlcgen.ListActionJobsByStatusParams{
		Status: jobStatusEnum(status),
		Limit:  l,
		Offset: o,
	})
	if err != nil {
		return nil, fmt.Errorf("repository.action.ListActionJobsByStatus: %w", err)
	}
	out := make([]domain.ActionJob, 0, len(rows))
	for _, row := range rows {
		out = append(out, actionJobDomain(row))
	}
	return out, nil
}

// ListActionJobsByAccount returns one account's actions (the per-account
// queue/concurrency view).
func (r *ActionRepo) ListActionJobsByAccount(ctx context.Context, accountID string, limit, offset *int) ([]domain.ActionJob, error) {
	l, o := ptrPage(limit, offset)
	rows, err := r.q.ListActionJobsByAccount(ctx, sqlcgen.ListActionJobsByAccountParams{
		AccountID: uuidValue(accountID),
		Limit:     l,
		Offset:    o,
	})
	if err != nil {
		return nil, fmt.Errorf("repository.action.ListActionJobsByAccount: %w", err)
	}
	out := make([]domain.ActionJob, 0, len(rows))
	for _, row := range rows {
		out = append(out, actionJobDomain(row))
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// action logs
// ---------------------------------------------------------------------------

// UpsertActionLog writes one attempt's verdict. The (job, attempt) pair is the
// upsert key: the RUNNING report and the terminal verdict are one row, and the
// COALESCE in the query keeps an earlier screenshot/excerpt from being blanked
// by a callback that omits it.
func (r *ActionRepo) UpsertActionLog(ctx context.Context, l domain.ActionLog) (domain.ActionLog, error) {
	if l.Attempt <= 0 {
		// The CHECK constraint requires attempt > 0; the attempt number is the
		// retry counter, and attempt 0 has no meaning.
		return domain.ActionLog{}, fmt.Errorf("%w: attempt must be >= 1", domain.ErrValidation)
	}
	row, err := r.q.UpsertActionLog(ctx, sqlcgen.UpsertActionLogParams{
		ActionJobID:     uuidValue(l.ActionJobID),
		Attempt:         int32(l.Attempt),
		Status:          attemptStatusEnum(l.Status),
		Verified:        l.Verified,
		WorkerID:        uuidValue(l.WorkerID),
		RenderedText:    l.RenderedText,
		ResponseExcerpt: textOrNull(l.ResponseExcerpt),
		ErrorClass:      textOrNull(l.ErrorClass),
		ScreenshotUrl:   textOrNull(l.ScreenshotURL),
		DurationMs:      int32(l.DurationMs),
	})
	if err != nil {
		return domain.ActionLog{}, fmt.Errorf("repository.action.UpsertActionLog: %w", err)
	}
	return actionLogDomain(row), nil
}

// GetActionLog returns one attempt of one job.
func (r *ActionRepo) GetActionLog(ctx context.Context, jobID string, attempt int) (domain.ActionLog, error) {
	row, err := r.q.GetActionLog(ctx, sqlcgen.GetActionLogParams{
		ActionJobID: uuidValue(jobID),
		Attempt:     int32(attempt),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ActionLog{}, domain.ErrNotFound
		}
		return domain.ActionLog{}, fmt.Errorf("repository.action.GetActionLog: %w", err)
	}
	return actionLogDomain(row), nil
}

// ListActionLogsByJob returns every attempt of a job, newest first — the
// dashboard drawer that debugs one action (P4-02).
func (r *ActionRepo) ListActionLogsByJob(ctx context.Context, jobID string) ([]domain.ActionLog, error) {
	rows, err := r.q.ListActionLogsByJob(ctx, uuidValue(jobID))
	if err != nil {
		return nil, fmt.Errorf("repository.action.ListActionLogsByJob: %w", err)
	}
	out := make([]domain.ActionLog, 0, len(rows))
	for _, row := range rows {
		out = append(out, actionLogDomain(row))
	}
	return out, nil
}

// ListActionLogsByErrorClass returns recent failures of one class (P3-12
// triage: how many AUTH vs RATE_LIMIT vs BANNED).
func (r *ActionRepo) ListActionLogsByErrorClass(ctx context.Context, class domain.ErrorClass, limit, offset *int) ([]domain.ActionLog, error) {
	l, o := ptrPage(limit, offset)
	rows, err := r.q.ListActionLogsByErrorClass(ctx, sqlcgen.ListActionLogsByErrorClassParams{
		ErrorClass: textOrNull(string(class)),
		Limit:      l,
		Offset:     o,
	})
	if err != nil {
		return nil, fmt.Errorf("repository.action.ListActionLogsByErrorClass: %w", err)
	}
	out := make([]domain.ActionLog, 0, len(rows))
	for _, row := range rows {
		out = append(out, actionLogDomain(row))
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// converters
// ---------------------------------------------------------------------------

func actionJobDomain(row sqlcgen.ActionJob) domain.ActionJob {
	return domain.ActionJob{
		ID:          uuidString(row.ID),
		Type:        jobTypeDomain(row.Type),
		TargetID:    uuidString(row.TargetID),
		AccountID:   uuidString(row.AccountID),
		WorkerID:    derefStr(uuidStrPtr(row.WorkerID)),
		Status:      jobStatusDomain(row.Status),
		ScheduledAt: tsTimeOrZero(row.ScheduledAt),
		StartedAt:   tsTime(row.StartedAt),
		FinishedAt:  tsTime(row.FinishedAt),
		Attempts:    int(row.Attempts),
		Error:       derefStr(row.Error),
		CreatedAt:   tsTimeOrZero(row.CreatedAt),
	}
}

func actionLogDomain(row sqlcgen.ActionLog) domain.ActionLog {
	return domain.ActionLog{
		ID:              uuidString(row.ID),
		ActionJobID:     uuidString(row.ActionJobID),
		Attempt:         int(row.Attempt),
		Status:          attemptStatusDomain(row.Status),
		Verified:        row.Verified,
		WorkerID:        derefStr(uuidStrPtr(row.WorkerID)),
		RenderedText:    row.RenderedText,
		ResponseExcerpt: derefStr(row.ResponseExcerpt),
		ErrorClass:      derefStr(row.ErrorClass),
		ScreenshotURL:   derefStr(row.ScreenshotUrl),
		DurationMs:      int64(row.DurationMs),
		Ts:              tsTimeOrZero(row.Ts),
	}
}
