package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// ScrapeService (on-demand) runs an Apify actor SYNCHRONOUSLY for a dashboard
// request: paste a post URL → the actor's dataset items are ingested → the
// stored post + its comments are returned. It rides the same tables as the
// scheduler path (target → scrape_job → apify_run → raw_payload), so the store
// stays the single source of truth: a repeat scrape of the same URL refreshes
// the rows instead of duplicating them, and the keyword-scrape feature reads
// the same post/comment tables.
type ScrapeService struct {
	scrapes  port.ScrapeStore
	runner   port.ApifyRunner
	actorFor func(domain.Platform) string
	// storage is the object store the ingestor reads dataset items back from
	// (the same bucket the runner wrote to). Required whenever runner is set.
	storageRef port.RawStorage
	// MaxItems caps one on-demand run (cost bound per click).
	MaxItems int
	log      *slog.Logger
}

// NewScrapeService wires the on-demand scraper. runner/actorFor/storage may be
// nil together: the endpoint then reports "unavailable" (503), never a panic.
func NewScrapeService(store port.ScrapeStore, runner port.ApifyRunner, actorFor func(domain.Platform) string, storage port.RawStorage, logger *slog.Logger) *ScrapeService {
	if logger == nil {
		logger = slog.Default()
	}
	return &ScrapeService{scrapes: store, runner: runner, actorFor: actorFor, storageRef: storage, MaxItems: 100, log: logger}
}

// PostDetail is one post plus its comments — the card the dashboard renders.
type PostDetail struct {
	Post     domain.Post      `json:"post"`
	Comments []domain.Comment `json:"comments"`
}

// platformFromURL infers the platform from a post permalink. The dashboard
// passes the platform explicitly, but the fallback keeps a thin client working.
func platformFromURL(raw string) domain.Platform {
	switch {
	case strings.Contains(raw, "instagram.com"):
		return domain.PlatformInstagram
	case strings.Contains(raw, "threads.com"), strings.Contains(raw, "threads.net"):
		return domain.PlatformThreads
	}
	return ""
}

// ScrapeTarget runs the platform actor for one post URL and returns the stored
// post with its comments. Synchronous: the HTTP request lasts as long as the
// actor run (tens of seconds); the dashboard shows a loader for it.
//
// Step order mirrors the scheduler path: target upsert (find-or-create, the
// URL→post join) → scrape_job row (the run's anchor) → apify_run audit row →
// actor run → ingest → read back through the target's post link.
func (s *ScrapeService) ScrapeTarget(ctx context.Context, platform domain.Platform, rawURL string) (PostDetail, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return PostDetail{}, fmt.Errorf("%w: url is required", domain.ErrValidation)
	}
	if platform == "" {
		platform = platformFromURL(rawURL)
	}
	if !platform.Valid() {
		return PostDetail{}, fmt.Errorf("%w: unknown platform %q", domain.ErrValidation, platform)
	}
	if s.runner == nil || s.actorFor == nil {
		return PostDetail{}, fmt.Errorf("%w: scrape runner is not configured", domain.ErrUnavailable)
	}
	actorID := s.actorFor(platform)
	if actorID == "" {
		return PostDetail{}, fmt.Errorf("%w: no actor configured for %s", domain.ErrValidation, platform)
	}

	// Find-or-create the target: on a repeat scrape this is the cache join
	// (same (platform, external_id) never duplicates).
	tgt, err := s.scrapes.UpsertTarget(ctx, domain.Target{
		Kind:       domain.TargetKindPost,
		Platform:   platform,
		ExternalID: rawURL,
		URL:        rawURL,
	})
	if err != nil {
		return PostDetail{}, fmt.Errorf("scrape: target upsert: %w", err)
	}

	// Short-circuit cache: a fresh scrape (< 6h) returns the stored post
	// without spending an actor run.
	if detail, err := s.readBack(ctx, tgt); err == nil && time.Since(detail.Post.ScrapedAt) < 6*time.Hour {
		return detail, nil
	}

	job, err := s.scrapes.CreateScrapeJob(ctx, domain.ScrapeJob{
		Type:        domain.JobTypeScrapeLike, // any scrape type; the driver is on-demand, not the scheduler
		TargetID:    tgt.ID,
		Status:      domain.JobStatusRunning,
		ScheduledAt: time.Now().UTC(),
	})
	if err != nil {
		return PostDetail{}, fmt.Errorf("scrape: create job: %w", err)
	}

	run, err := s.scrapes.CreateApifyRun(ctx, domain.ApifyRun{
		ScrapeJobID: job.ID,
		ActorID:     actorID,
		Status:      "RUNNING",
	})
	if err != nil {
		return PostDetail{}, fmt.Errorf("scrape: create run: %w", err)
	}

	out, err := s.runner.Run(ctx, port.ApifyInput{
		ScrapeJobID: job.ID,
		ActorID:     actorID,
		Platform:    platform,
		TargetURL:   rawURL,
		MaxItems:    s.MaxItems,
	})
	if err != nil {
		s.fail(ctx, job.ID, err.Error())
		return PostDetail{}, fmt.Errorf("scrape: run: %w", err)
	}
	if out.Status != "SUCCEEDED" {
		s.fail(ctx, job.ID, out.Error)
		return PostDetail{}, fmt.Errorf("scrape: actor %s: %s", out.Status, out.Error)
	}

	// Record the run id + dataset keys, then normalize the payload into
	// posts/comments/metric snapshots (idempotent upserts).
	if _, err := s.scrapes.UpdateApifyRun(ctx, domain.ApifyRun{
		ID:     run.ID,
		RunID:  out.RunID,
		Status: out.Status,
	}, true); err != nil {
		s.log.Warn("scrape: update run failed", "run", run.ID, "err", err)
	}
	for _, key := range out.ItemKeys {
		if _, err := s.scrapes.CreateRawPayload(ctx, domain.RawPayload{ApifyRunID: run.ID, S3Key: key}); err != nil {
			s.log.Warn("scrape: raw payload pointer failed", "key", key, "err", err)
		}
	}
	ingestor := NewPlatformScrapeIngestor(s.scrapes, s.storageRef, platform, s.log)
	if _, err := ingestor.IngestRun(ctx, run.ID); err != nil {
		return PostDetail{}, fmt.Errorf("scrape: ingest: %w", err)
	}

	detail, err := s.readBack(ctx, tgt)
	if err != nil {
		// The actor succeeded but produced no post for this URL (a profile
		// link, a deleted post). Tell the operator rather than 500-ing.
		s.fail(ctx, job.ID, "actor output held no post for this URL")
		return PostDetail{}, fmt.Errorf("%w: scrape produced no post for %s", domain.ErrValidation, rawURL)
	}
	s.scrapes.LinkTargetPost(ctx, tgt.ID, detail.Post.ID)
	if _, err := s.scrapes.CompleteScrapeJob(ctx, job.ID, domain.JobStatusSuccess, nil); err != nil {
		s.log.Warn("scrape: complete job failed", "job", job.ID, "err", err)
	}
	return detail, nil
}

// fail marks the scrape_job FAILED with a reason (best-effort: the caller
// already has the error to report).
func (s *ScrapeService) fail(ctx context.Context, jobID, reason string) {
	reason = truncateErr(reason)
	if _, err := s.scrapes.CompleteScrapeJob(ctx, jobID, domain.JobStatusFailed, &reason); err != nil {
		s.log.Warn("scrape: fail job write failed", "job", jobID, "err", err)
	}
}

// readBack resolves the target's linked post and loads its comments. Not found
// until the first successful ingest links the post.
func (s *ScrapeService) readBack(ctx context.Context, tgt domain.Target) (PostDetail, error) {
	if tgt.PostID == nil {
		return PostDetail{}, errors.New("target has no linked post")
	}
	post, err := s.scrapes.GetPost(ctx, *tgt.PostID)
	if err != nil {
		return PostDetail{}, fmt.Errorf("get post: %w", err)
	}
	comments, err := s.scrapes.ListCommentsByPost(ctx, post.ID, nil, nil)
	if err != nil {
		return PostDetail{}, fmt.Errorf("list comments: %w", err)
	}
	return PostDetail{Post: post, Comments: comments}, nil
}
