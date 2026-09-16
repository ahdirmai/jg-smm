package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/repository/sqlcgen"
)

// ScrapeRepo implements port.ScrapeStore on top of the sqlc query handle. It
// covers the whole P2 scrape pipeline: targets, posts/comments (idempotent
// upserts), scrape jobs, Apify runs, raw payload pointers and the metric
// time-series.
type ScrapeRepo struct {
	q *sqlcgen.Queries
}

// NewScrapeRepo binds the repo to a sqlc query handle.
func NewScrapeRepo(q *sqlcgen.Queries) *ScrapeRepo { return &ScrapeRepo{q: q} }

var _ port.ScrapeStore = (*ScrapeRepo)(nil)

// ---------------------------------------------------------------------------
// targets
// ---------------------------------------------------------------------------

// UpsertTarget creates the target if absent and refreshes url/meta if present,
// returning the canonical row. The resolution columns (post_id/comment_id) are
// NOT touched here: they are set by the ingest pipeline once content exists.
func (r *ScrapeRepo) UpsertTarget(ctx context.Context, t domain.Target) (domain.Target, error) {
	row, err := r.q.UpsertTarget(ctx, sqlcgen.UpsertTargetParams{
		Kind:       targetKindEnum(t.Kind),
		Platform:   platformEnum(t.Platform),
		ExternalID: t.ExternalID,
		Url:        t.URL,
		Meta:       jsonbBytes(t.Meta),
	})
	if err != nil {
		return domain.Target{}, fmt.Errorf("repository.scrape.UpsertTarget: %w", err)
	}
	return toTarget(row), nil
}

func (r *ScrapeRepo) GetTarget(ctx context.Context, id string) (domain.Target, error) {
	row, err := r.q.GetTargetByID(ctx, uuidValue(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Target{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Target{}, fmt.Errorf("repository.scrape.GetTarget: %w", err)
	}
	return toTarget(row), nil
}

func (r *ScrapeRepo) GetTargetByExternalID(ctx context.Context, p domain.Platform, externalID string) (domain.Target, error) {
	row, err := r.q.GetTargetByPlatformExternalID(ctx, sqlcgen.GetTargetByPlatformExternalIDParams{
		Platform:   platformEnum(p),
		ExternalID: externalID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Target{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Target{}, fmt.Errorf("repository.scrape.GetTargetByExternalID: %w", err)
	}
	return toTarget(row), nil
}

func (r *ScrapeRepo) LinkTargetPost(ctx context.Context, targetID, postID string) error {
	if err := r.q.LinkTargetPost(ctx, sqlcgen.LinkTargetPostParams{
		ID:     uuidValue(targetID),
		PostID: uuidValue(postID),
	}); err != nil {
		return fmt.Errorf("repository.scrape.LinkTargetPost: %w", err)
	}
	return nil
}

func (r *ScrapeRepo) LinkTargetComment(ctx context.Context, targetID, commentID string) error {
	if err := r.q.LinkTargetComment(ctx, sqlcgen.LinkTargetCommentParams{
		ID:        uuidValue(targetID),
		CommentID: uuidValue(commentID),
	}); err != nil {
		return fmt.Errorf("repository.scrape.LinkTargetComment: %w", err)
	}
	return nil
}

func (r *ScrapeRepo) ListTargets(ctx context.Context, limit, offset *int) ([]domain.Target, error) {
	l, o := ptrPage(limit, offset)
	rows, err := r.q.ListTargets(ctx, sqlcgen.ListTargetsParams{Limit: l, Offset: o})
	if err != nil {
		return nil, fmt.Errorf("repository.scrape.ListTargets: %w", err)
	}
	out := make([]domain.Target, 0, len(rows))
	for _, row := range rows {
		out = append(out, toTarget(row))
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// content: posts + comments (idempotent by (platform, external_id))
// ---------------------------------------------------------------------------

func (r *ScrapeRepo) UpsertPost(ctx context.Context, p domain.Post) (domain.Post, error) {
	media := p.MediaURLs
	if media == nil {
		media = []string{}
	}
	row, err := r.q.UpsertPost(ctx, sqlcgen.UpsertPostParams{
		Platform:        platformEnum(p.Platform),
		ExternalID:      p.ExternalID,
		AuthorHandle:    p.AuthorHandle,
		AuthorID:        p.AuthorID,
		Text:            p.Text,
		MediaUrls:       media,
		Metrics:         jsonbBytes(p.Metrics),
		AuthorAccountID: uuidValuePtr(p.AuthorAccountID),
	})
	if err != nil {
		return domain.Post{}, fmt.Errorf("repository.scrape.UpsertPost: %w", err)
	}
	return toPost(row), nil
}

func (r *ScrapeRepo) GetPost(ctx context.Context, id string) (domain.Post, error) {
	row, err := r.q.GetPostByID(ctx, uuidValue(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Post{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Post{}, fmt.Errorf("repository.scrape.GetPost: %w", err)
	}
	return toPost(row), nil
}

func (r *ScrapeRepo) GetPostByExternalID(ctx context.Context, p domain.Platform, externalID string) (domain.Post, error) {
	row, err := r.q.GetPostByPlatformExternalID(ctx, sqlcgen.GetPostByPlatformExternalIDParams{
		Platform:   platformEnum(p),
		ExternalID: externalID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Post{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Post{}, fmt.Errorf("repository.scrape.GetPostByExternalID: %w", err)
	}
	return toPost(row), nil
}

func (r *ScrapeRepo) ListPosts(ctx context.Context, limit, offset *int) ([]domain.Post, error) {
	l, o := ptrPage(limit, offset)
	rows, err := r.q.ListPosts(ctx, sqlcgen.ListPostsParams{Limit: l, Offset: o})
	if err != nil {
		return nil, fmt.Errorf("repository.scrape.ListPosts: %w", err)
	}
	out := make([]domain.Post, 0, len(rows))
	for _, row := range rows {
		out = append(out, toPost(row))
	}
	return out, nil
}

// ListTopPosts returns the highest-value posts on a platform by one jsonb
// metric field. metricKey must name a numeric field in post.metrics.
func (r *ScrapeRepo) ListTopPosts(ctx context.Context, p domain.Platform, metricKey string, limit int) ([]domain.Post, error) {
	rows, err := r.q.ListTopPostsByPlatform(ctx, sqlcgen.ListTopPostsByPlatformParams{
		Platform: platformEnum(p),
		Column2:  metricKey,
		Limit:    clampLimit(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("repository.scrape.ListTopPosts: %w", err)
	}
	out := make([]domain.Post, 0, len(rows))
	for _, row := range rows {
		out = append(out, toPost(row))
	}
	return out, nil
}

func (r *ScrapeRepo) UpsertComment(ctx context.Context, c domain.Comment) (domain.Comment, error) {
	row, err := r.q.UpsertComment(ctx, sqlcgen.UpsertCommentParams{
		PostID:       uuidValue(c.PostID),
		Platform:     platformEnum(c.Platform),
		ExternalID:   c.ExternalID,
		AuthorHandle: c.AuthorHandle,
		Text:         c.Text,
		Metrics:      jsonbBytes(c.Metrics),
		ParentID:     uuidValuePtr(c.ParentID),
	})
	if err != nil {
		return domain.Comment{}, fmt.Errorf("repository.scrape.UpsertComment: %w", err)
	}
	return toComment(row), nil
}

func (r *ScrapeRepo) GetComment(ctx context.Context, id string) (domain.Comment, error) {
	row, err := r.q.GetCommentByID(ctx, uuidValue(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Comment{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Comment{}, fmt.Errorf("repository.scrape.GetComment: %w", err)
	}
	return toComment(row), nil
}

func (r *ScrapeRepo) ListCommentsByPost(ctx context.Context, postID string, limit, offset *int) ([]domain.Comment, error) {
	l, o := ptrPage(limit, offset)
	rows, err := r.q.ListCommentsByPost(ctx, sqlcgen.ListCommentsByPostParams{
		PostID: uuidValue(postID),
		Limit:  l,
		Offset: o,
	})
	if err != nil {
		return nil, fmt.Errorf("repository.scrape.ListCommentsByPost: %w", err)
	}
	out := make([]domain.Comment, 0, len(rows))
	for _, row := range rows {
		out = append(out, toComment(row))
	}
	return out, nil
}

func (r *ScrapeRepo) CountCommentsByPost(ctx context.Context, postID string) (int, error) {
	n, err := r.q.CountCommentsByPost(ctx, uuidValue(postID))
	if err != nil {
		return 0, fmt.Errorf("repository.scrape.CountCommentsByPost: %w", err)
	}
	return int(n), nil
}

// ---------------------------------------------------------------------------
// scrape jobs
// ---------------------------------------------------------------------------

func (r *ScrapeRepo) CreateScrapeJob(ctx context.Context, j domain.ScrapeJob) (domain.ScrapeJob, error) {
	status := j.Status
	if status == "" {
		status = domain.JobStatusPending
	}
	row, err := r.q.CreateScrapeJob(ctx, sqlcgen.CreateScrapeJobParams{
		Type:        jobTypeEnum(j.Type),
		TargetID:    uuidValue(j.TargetID),
		AccountID:   uuidValuePtr(j.AccountID),
		WorkerID:    uuidValuePtr(j.WorkerID),
		Status:      jobStatusEnum(status),
		ScheduledAt: tsPtr(&j.ScheduledAt),
	})
	if err != nil {
		return domain.ScrapeJob{}, fmt.Errorf("repository.scrape.CreateScrapeJob: %w", err)
	}
	return toScrapeJob(row), nil
}

func (r *ScrapeRepo) GetScrapeJob(ctx context.Context, id string) (domain.ScrapeJob, error) {
	row, err := r.q.GetScrapeJobByID(ctx, uuidValue(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ScrapeJob{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ScrapeJob{}, fmt.Errorf("repository.scrape.GetScrapeJob: %w", err)
	}
	return toScrapeJob(row), nil
}

// ClaimNextScrapeJob atomically claims the oldest due PENDING job for an
// account (FIFO by scheduled_at). Returns ErrNotFound when nothing is due —
// the scheduler treats that as an empty tick, not an error.
func (r *ScrapeRepo) ClaimNextScrapeJob(ctx context.Context, accountID string) (domain.ScrapeJob, error) {
	row, err := r.q.ClaimNextScrapeJob(ctx, uuidValue(accountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ScrapeJob{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ScrapeJob{}, fmt.Errorf("repository.scrape.ClaimNextScrapeJob: %w", err)
	}
	return toScrapeJob(row), nil
}

func (r *ScrapeRepo) ListScrapeJobs(ctx context.Context, limit, offset *int) ([]domain.ScrapeJob, error) {
	l, o := ptrPage(limit, offset)
	rows, err := r.q.ListScrapeJobs(ctx, sqlcgen.ListScrapeJobsParams{Limit: l, Offset: o})
	if err != nil {
		return nil, fmt.Errorf("repository.scrape.ListScrapeJobs: %w", err)
	}
	out := make([]domain.ScrapeJob, 0, len(rows))
	for _, row := range rows {
		out = append(out, toScrapeJob(row))
	}
	return out, nil
}

func (r *ScrapeRepo) ListScrapeJobsByStatus(ctx context.Context, status domain.JobStatus, limit, offset *int) ([]domain.ScrapeJob, error) {
	l, o := ptrPage(limit, offset)
	rows, err := r.q.ListScrapeJobsByStatus(ctx, sqlcgen.ListScrapeJobsByStatusParams{
		Status: jobStatusEnum(status),
		Limit:  l,
		Offset: o,
	})
	if err != nil {
		return nil, fmt.Errorf("repository.scrape.ListScrapeJobsByStatus: %w", err)
	}
	out := make([]domain.ScrapeJob, 0, len(rows))
	for _, row := range rows {
		out = append(out, toScrapeJob(row))
	}
	return out, nil
}

func (r *ScrapeRepo) ListPendingScrapeJobsByAccount(ctx context.Context, accountID string, limit int) ([]domain.ScrapeJob, error) {
	rows, err := r.q.ListPendingScrapeJobsByAccount(ctx, sqlcgen.ListPendingScrapeJobsByAccountParams{
		AccountID: uuidValue(accountID),
		Limit:     clampLimit(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("repository.scrape.ListPendingScrapeJobsByAccount: %w", err)
	}
	out := make([]domain.ScrapeJob, 0, len(rows))
	for _, row := range rows {
		out = append(out, toScrapeJob(row))
	}
	return out, nil
}

// RescheduleScrapeJob pushes a job's scheduled_at out and resets it to PENDING
// so the next eligible tick claims it again (backoff + jitter path).
func (r *ScrapeRepo) RescheduleScrapeJob(ctx context.Context, id string, scheduledAt time.Time) (domain.ScrapeJob, error) {
	row, err := r.q.RescheduleScrapeJob(ctx, sqlcgen.RescheduleScrapeJobParams{
		ID:          uuidValue(id),
		ScheduledAt: tsPtr(&scheduledAt),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ScrapeJob{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ScrapeJob{}, fmt.Errorf("repository.scrape.RescheduleScrapeJob: %w", err)
	}
	return toScrapeJob(row), nil
}

// CompleteScrapeJob marks a job terminal. A nil errMsg with a success status is
// the normal path; a non-nil message records the failure reason.
func (r *ScrapeRepo) CompleteScrapeJob(ctx context.Context, id string, status domain.JobStatus, errMsg *string) (domain.ScrapeJob, error) {
	row, err := r.q.CompleteScrapeJob(ctx, sqlcgen.CompleteScrapeJobParams{
		ID:     uuidValue(id),
		Status: jobStatusEnum(status),
		Error:  errMsg,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ScrapeJob{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ScrapeJob{}, fmt.Errorf("repository.scrape.CompleteScrapeJob: %w", err)
	}
	return toScrapeJob(row), nil
}

// ---------------------------------------------------------------------------
// apify runs + raw payload pointers
// ---------------------------------------------------------------------------

func (r *ScrapeRepo) CreateApifyRun(ctx context.Context, a domain.ApifyRun) (domain.ApifyRun, error) {
	row, err := r.q.CreateApifyRun(ctx, sqlcgen.CreateApifyRunParams{
		ScrapeJobID: uuidValue(a.ScrapeJobID),
		ActorID:     a.ActorID,
		RunID:       a.RunID,
		Status:      a.Status,
	})
	if err != nil {
		return domain.ApifyRun{}, fmt.Errorf("repository.scrape.CreateApifyRun: %w", err)
	}
	return toApifyRun(row), nil
}

// UpdateApifyRun refreshes run status. A non-nil FinishedAt stamps the end of
// the run; nil leaves it open (the run is still in flight).
func (r *ScrapeRepo) UpdateApifyRun(ctx context.Context, a domain.ApifyRun, finished bool) (domain.ApifyRun, error) {
	var fin pgtype.Timestamptz
	if finished {
		fin = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	}
	row, err := r.q.UpdateApifyRun(ctx, sqlcgen.UpdateApifyRunParams{
		ID:         uuidValue(a.ID),
		Status:     a.Status,
		FinishedAt: fin,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ApifyRun{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ApifyRun{}, fmt.Errorf("repository.scrape.UpdateApifyRun: %w", err)
	}
	return toApifyRun(row), nil
}

func (r *ScrapeRepo) CreateRawPayload(ctx context.Context, p domain.RawPayload) (domain.RawPayload, error) {
	row, err := r.q.CreateRawPayload(ctx, sqlcgen.CreateRawPayloadParams{
		ApifyRunID: uuidValue(p.ApifyRunID),
		S3Key:      p.S3Key,
		Bytes:      p.Bytes,
	})
	if err != nil {
		return domain.RawPayload{}, fmt.Errorf("repository.scrape.CreateRawPayload: %w", err)
	}
	return toRawPayload(row), nil
}

// ListRawPayloadsByRunID returns the S3 keys for one Apify run. Key-only: the
// ingestor reads the bodies from object storage, never from the DB.
func (r *ScrapeRepo) ListRawPayloadsByRunID(ctx context.Context, apifyRunID string) ([]string, error) {
	keys, err := r.q.ListRawPayloadsByRun(ctx, uuidValue(apifyRunID))
	if err != nil {
		return nil, fmt.Errorf("repository.scrape.ListRawPayloadsByRunID: %w", err)
	}
	return keys, nil
}

// ---------------------------------------------------------------------------
// metric time-series
// ---------------------------------------------------------------------------

func (r *ScrapeRepo) CreateMetricSnapshot(ctx context.Context, s domain.MetricSnapshot) error {
	if err := r.q.CreateMetricSnapshot(ctx, sqlcgen.CreateMetricSnapshotParams{
		PostID:     uuidValue(s.PostID),
		Views:      s.Views,
		Likes:      s.Likes,
		Comments:   s.Comments,
		Shares:     s.Shares,
		Reach:      s.Reach,
		ReelsViews: s.ReelsViews,
	}); err != nil {
		return fmt.Errorf("repository.scrape.CreateMetricSnapshot: %w", err)
	}
	return nil
}

func (r *ScrapeRepo) ListMetricSnapshots(ctx context.Context, postID string, from, to time.Time) ([]domain.MetricSnapshot, error) {
	rows, err := r.q.ListMetricSnapshotsByPost(ctx, sqlcgen.ListMetricSnapshotsByPostParams{
		PostID: uuidValue(postID),
		Ts:     tsPtr(&from),
		Ts_2:   tsPtr(&to),
	})
	if err != nil {
		return nil, fmt.Errorf("repository.scrape.ListMetricSnapshots: %w", err)
	}
	out := make([]domain.MetricSnapshot, 0, len(rows))
	for _, row := range rows {
		out = append(out, toMetricSnapshot(row))
	}
	return out, nil
}

// LatestMetricSnapshots returns the most recent sample per post over the
// retention window (the query filters ts >= now()-90d internally).
func (r *ScrapeRepo) LatestMetricSnapshots(ctx context.Context) ([]domain.MetricSnapshot, error) {
	rows, err := r.q.LatestMetricSnapshotsByPost(ctx)
	if err != nil {
		return nil, fmt.Errorf("repository.scrape.LatestMetricSnapshots: %w", err)
	}
	out := make([]domain.MetricSnapshot, 0, len(rows))
	for _, row := range rows {
		out = append(out, metricSnapshotFromAgg(row.PostID, row.Views, row.Likes,
			row.Comments, row.Shares, row.Reach, row.ReelsViews, row.Ts))
	}
	return out, nil
}

// TopPostsByMetric returns the top-N posts by one metric over the retention
// window. metric is the domain metric name ("views", "likes", ...).
func (r *ScrapeRepo) TopPostsByMetric(ctx context.Context, metric string, limit int) ([]domain.MetricSnapshot, error) {
	rows, err := r.q.TopPostsByMetric(ctx, sqlcgen.TopPostsByMetricParams{
		Column1: metric,
		Limit:   clampLimit(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("repository.scrape.TopPostsByMetric: %w", err)
	}
	out := make([]domain.MetricSnapshot, 0, len(rows))
	for _, row := range rows {
		out = append(out, metricSnapshotFromAgg(row.PostID, row.Views, row.Likes,
			row.Comments, row.Shares, row.Reach, row.ReelsViews, row.Ts))
	}
	return out, nil
}
