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
	// spend caps how many PAID on-demand runs one caller may start inside a
	// window. `force` is a caller-controlled boolean that skips the only other
	// cost guard (the 6h freshness cache), so without this the endpoint is an
	// unmetered way to spend Apify credit in a loop. nil means unlimited.
	spend *importLimiter
	log   *slog.Logger
}

// NewScrapeService wires the on-demand scraper. runner/actorFor/storage may be
// nil together: the endpoint then reports "unavailable" (503), never a panic.
func NewScrapeService(store port.ScrapeStore, runner port.ApifyRunner, actorFor func(domain.Platform) string, storage port.RawStorage, logger *slog.Logger) *ScrapeService {
	if logger == nil {
		logger = slog.Default()
	}
	return &ScrapeService{scrapes: store, runner: runner, actorFor: actorFor, storageRef: storage, MaxItems: 100, log: logger}
}

// SetSpendLimit attaches the per-caller ceiling on PAID on-demand scrapes,
// reusing the account-import limiter shape. Called from wiring; a limit <= 0
// leaves the surface unlimited (a self-hosted host with no ceiling configured).
func (s *ScrapeService) SetSpendLimit(limit int, window time.Duration) {
	if limit <= 0 || window <= 0 {
		s.spend = nil
		return
	}
	s.spend = newImportLimiter(window, limit)
}

// allowSpend reports whether caller may start a paid run now. A nil limiter
// means unlimited, so callers do not need to nil-check.
func (s *ScrapeService) allowSpend(caller string) bool {
	if s.spend == nil {
		return true
	}
	return s.spend.Allow(caller, time.Now())
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
// force skips the 6h freshness short-circuit. It exists because a keyword batch
// scrape updates post.scraped_at for every post it ingests (UpsertPost sets it
// to now()) without ever fetching their comments — the search actor returns no
// comment bodies. So a post the operator just scraped in a batch looks "fresh"
// and the button would silently return zero comments forever. The operator
// asking for comments means a paid run is what they want.
//
// Step order mirrors the scheduler path: target upsert (find-or-create, the
// URL→post join) → scrape_job row (the run's anchor) → apify_run audit row →
// actor run → ingest → read back through the target's post link.
func (s *ScrapeService) ScrapeTarget(ctx context.Context, platform domain.Platform, rawURL string, force bool) (PostDetail, error) {
	return s.scrapeTarget(ctx, platform, rawURL, force, "")
}

// ScrapeTargetAs is ScrapeTarget with an explicit spend-ceiling key (the
// client IP from the HTTP boundary). An empty caller is unlimited.
func (s *ScrapeService) ScrapeTargetAs(ctx context.Context, platform domain.Platform, rawURL string, force bool, caller string) (PostDetail, error) {
	return s.scrapeTarget(ctx, platform, rawURL, force, caller)
}

// scrapeTarget carries the caller key for the spend ceiling. The public
// entry point has no caller (internal callers, tests) and is therefore
// unlimited; the HTTP handler passes the client IP so the ceiling is measured
// on the one thing that spends money.
func (s *ScrapeService) scrapeTarget(ctx context.Context, platform domain.Platform, rawURL string, force bool, caller string) (PostDetail, error) {
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
	// One guard for both entry points: the operator's URL is validated the same
	// way an action target is, so `ScrapeTarget` cannot be used to reach an
	// arbitrary host. (It previously persisted rawURL unvalidated.)
	u, err := safeTargetURL(rawURL, platform)
	if err != nil {
		return PostDetail{}, err
	}
	// The actor's `username` input also accepts profile and post URLs, so a
	// profile link would silently buy a paid 100-post scrape that ingests 100
	// unrelated rows before readBack fails. A target must address one post.
	if postExternalIDFromURL(platform, u.String()) == "" {
		return PostDetail{}, fmt.Errorf("%w: %q is not a post permalink for %s (expected /p/, /reel/ or /tv/ on instagram.com, /post/ on threads)", domain.ErrValidation, rawURL, platform)
	}
	rawURL = u.String()

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
	// without spending an actor run. Skipped on force — see the doc comment.
	if !force {
		if detail, err := s.readBack(ctx, tgt); err == nil && time.Since(detail.Post.ScrapedAt) < 6*time.Hour {
			return detail, nil
		}
	}

	// Past this point the click costs money: the cache above is the only free
	// exit, and `force` (caller-controlled) jumps it. Meter before creating the
	// job row or the apify_run row, so a refused call leaves nothing behind and
	// the actor is never reached.
	if caller != "" && !s.allowSpend(caller) {
		return PostDetail{}, fmt.Errorf("%w: scrape rate limit exceeded; try again in a minute", domain.ErrRateLimited)
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
	for _, item := range out.Items {
		if _, err := s.scrapes.CreateRawPayload(ctx, domain.RawPayload{
			ApifyRunID: run.ID, S3Key: item.Key, Bytes: item.Bytes,
		}); err != nil {
			s.log.Warn("scrape: raw payload pointer failed", "key", item.Key, "err", err)
		}
	}
	ingestor := NewPlatformScrapeIngestor(s.scrapes, s.storageRef, platform, postExternalIDFromURL(platform, rawURL), s.log)
	res, err := ingestor.IngestRun(ctx, run.ID)
	if err != nil {
		// The run was paid for and the actor succeeded; only the normalize step
		// failed. Close the job here — this branch previously returned without
		// touching the row, so a client that disconnected (ctx cancelled) left a
		// scrape_job stuck in `running` forever, with target.post_id unlinked and
		// the next scrape of the same URL paying again.
		s.fail(ctx, job.ID, "ingest: "+err.Error())
		return PostDetail{}, fmt.Errorf("scrape: ingest: %w", err)
	}
	// A run that delivered nothing is not a scrape that found nothing new. For a
	// forced re-scrape of a deleted post the ingest is a clean no-op, and readBack
	// would resolve tgt.PostID — the PRIOR scrape's link — and hand back the old
	// comments as if this fetch had produced them: HTTP 200, job `success`, and a
	// panel showing stale rows. Take the same miss branch a read-back failure
	// takes, so an empty run can never be reported as fresh evidence.
	if res.ItemsRead == 0 {
		s.fail(ctx, job.ID, "actor output held no items for this URL")
		return PostDetail{}, fmt.Errorf("%w: scrape produced no post for %s", domain.ErrValidation, rawURL)
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
//
// The write runs on a context detached from the request on purpose. The reason
// this method exists is a client that disconnected mid-scrape, and by then the
// request context is already cancelled — so a job write riding on it would fail
// with context.Canceled and leave the row `running` permanently, the exact
// outcome the call is here to prevent. Only the cancellation is dropped: values
// (trace ids, the logger) still flow through.
func (s *ScrapeService) fail(ctx context.Context, jobID, reason string) {
	reason = truncateErr(reason)
	if _, err := s.scrapes.CompleteScrapeJob(context.WithoutCancel(ctx), jobID, domain.JobStatusFailed, &reason); err != nil {
		s.log.Warn("scrape: fail job write failed", "job", jobID, "err", err)
	}
}

// ListRecentPosts returns stored posts newest-scrape-first — the dashboard's
// "already scraped" history. Capped so a stray ?limit=999999 can't dump the
// whole table.
func (s *ScrapeService) ListRecentPosts(ctx context.Context, limit int) ([]domain.Post, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	return s.scrapes.ListPosts(ctx, &limit, nil)
}

// readBack resolves the target's post and loads its comments. On the first
// scrape the target is keyed by the raw URL while the post is keyed by its
// shortcode, and LinkTargetPost has not run yet — so fall back to a
// shortcode-from-URL lookup when the target carries no post link.
func (s *ScrapeService) readBack(ctx context.Context, tgt domain.Target) (PostDetail, error) {
	post, err := s.resolvePost(ctx, tgt)
	if err != nil {
		return PostDetail{}, err
	}
	comments, err := s.scrapes.ListCommentsByPost(ctx, post.ID, nil, nil)
	if err != nil {
		return PostDetail{}, fmt.Errorf("list comments: %w", err)
	}
	return PostDetail{Post: post, Comments: comments}, nil
}

// resolvePost finds the target's post: the linked id when one exists, else the
// post the just-completed ingest stored under the URL's shortcode.
func (s *ScrapeService) resolvePost(ctx context.Context, tgt domain.Target) (domain.Post, error) {
	if tgt.PostID != nil {
		return s.scrapes.GetPost(ctx, *tgt.PostID)
	}
	code := postExternalIDFromURL(tgt.Platform, tgt.URL)
	if code == "" {
		return domain.Post{}, errors.New("target has no linked post")
	}
	return s.scrapes.GetPostByExternalID(ctx, tgt.Platform, code)
}

// postExternalIDFromURL pulls the post's external id (its shortcode) out of a
// permalink — the id the ingestor keys the stored post on. IG permalinks put
// the code after /p/, /reel(s)/ or /tv/; Threads puts it after /post/. Query
// and fragment are dropped (…/p/CODE/?img_index=1 → CODE).
func postExternalIDFromURL(p domain.Platform, rawURL string) string {
	u := rawURL
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	segs := strings.Split(strings.Trim(u, "/"), "/")
	markers := map[string]struct{}{}
	switch p {
	case domain.PlatformInstagram:
		markers = map[string]struct{}{"p": {}, "reel": {}, "reels": {}, "tv": {}}
	case domain.PlatformThreads:
		markers = map[string]struct{}{"post": {}}
	}
	for i := 0; i+1 < len(segs); i++ {
		if _, ok := markers[segs[i]]; ok {
			return segs[i+1]
		}
	}
	return ""
}
