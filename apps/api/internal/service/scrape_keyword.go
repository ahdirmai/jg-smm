package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// KeywordScrapeMaxKeywords bounds one request. Five keywords is the operator
// contract; more is a batch, not a search.
const KeywordScrapeMaxKeywords = 5

// keywordScrapeAttempts bounds how many times one batch runs the actor.
//
// The Instagram hashtag actor intermittently answers with a RELATED hashtag's
// metadata row instead of posts: asked for "batulicin" it returned
// {searchTerm:"batulicin", name:"peturing", posts:"824"} — a formatted count,
// no posts, marked SUCCEEDED. Measured at roughly one run in three on an
// identical payload, so a single keyword search silently came back empty. One
// retry recovers it; a keyword that is genuinely empty just pays one extra run.
const keywordScrapeAttempts = 2

// KeywordScrapeService (search) runs the platform's search actor over up to 5
// keywords inside a time window.
//   - Batch scrape (keyword): async. POST /api/scrape/keywords → 202 {batch},
//     background goroutine drives Apify+ingest, batch dashboard at
//     GET /api/scrape/batches/:id and /:id/posts. No request blocks on Apify.
//   - Post scrape (satuan): sync, single URL → POST /api/scrape/target (ScrapeService).
//
// Rows land in the same post/comment tables, so both are reusable by actions.
type KeywordScrapeService struct {
	scrapes        port.ScrapeStore
	batches        port.KeywordBatchStore
	runner         port.ApifyRunner
	actorFor       func(domain.Platform) string
	searchActorFor func(domain.Platform) string
	storageRef     port.RawStorage
	log            *slog.Logger
	// spend caps how many PAID keyword batches one caller may start inside a
	// window. A batch is the most expensive surface in the product (5 keywords ×
	// 200 posts × 2 attempts per click), so it carries the same ceiling shape as
	// the account import. nil means unlimited — only tests construct it that way;
	// the wiring always attaches one.
	spend *importLimiter
}

// NewKeywordScrapeService wires the keyword scraper. runner nil → 503.
// searchActorFor nil → falls back to empty mapping.
// batches nil → batch endpoints 503 (legacy sync path unsupported without batch table).
func NewKeywordScrapeService(
	store port.ScrapeStore,
	runner port.ApifyRunner,
	searchActorFor func(domain.Platform) string,
	storage port.RawStorage,
	logger *slog.Logger,
) *KeywordScrapeService {
	if logger == nil {
		logger = slog.Default()
	}
	if searchActorFor == nil {
		searchActorFor = func(domain.Platform) string { return "" }
	}
	return &KeywordScrapeService{
		scrapes:        store,
		runner:         runner,
		searchActorFor: searchActorFor,
		storageRef:     storage,
		log:            logger,
	}
}

// SetBatchStore attaches the batch store (called from wiring after repo exists).
func (s *KeywordScrapeService) SetBatchStore(b port.KeywordBatchStore) { s.batches = b }

// SetSpendLimit attaches the per-caller ceiling on PAID keyword batches, reusing
// the account-import limiter shape (an in-process fixed-window counter keyed by
// caller). Called from wiring; a limit <= 0 leaves the surface unlimited so a
// self-hosted deployment with no configured ceiling behaves as before.
func (s *KeywordScrapeService) SetSpendLimit(limit int, window time.Duration) {
	if limit <= 0 || window <= 0 {
		s.spend = nil
		return
	}
	s.spend = newImportLimiter(window, limit)
}

// allowSpend reports whether caller may start a paid batch now. A nil limiter is
// unlimited (tests), so this can be called unconditionally.
func (s *KeywordScrapeService) allowSpend(caller string) bool {
	if s.spend == nil {
		return true
	}
	return s.spend.Allow(caller, time.Now())
}

// KeywordWindow is the time filter the search runs under.
type KeywordWindow struct {
	From time.Time
	To   time.Time
}

// KeywordScrapeInput is one search request.
type KeywordScrapeInput struct {
	Platform domain.Platform
	Keywords []string
	Window   KeywordWindow
	MaxPosts int
	// CreatedBy is the authenticated user (audit, nullable).
	CreatedBy *string
	// Caller is the rate-limit key for the spend ceiling (the client IP at the
	// HTTP boundary, the user id for internal callers). Empty means unlimited —
	// an unattributable spend cannot be metered, and inventing a shared bucket
	// for every anonymous caller would refuse the second operator on a shared
	// host.
	Caller string
}

// KeywordScrapeResult is the outcome (legacy sync, kept for tests).
type KeywordScrapeResult struct {
	Posts     []domain.Post `json:"posts"`
	Comments  int           `json:"comments"`
	Keywords  []string      `json:"keywords"`
	Platform  string        `json:"platform"`
	ActorID   string        `json:"actorId"`
	ItemsRead int           `json:"itemsRead"`
}

// keywordScrapable is the gate on which platforms can run a keyword batch.
// Each one has a search actor, a per-actor payload in the Apify runner, and a
// dataset parser in the ingestor — all three, or the platform is not scrapable
// and saying so here is kinder than a run that dies mid-ingest.
//
// The list is the single source of truth for both this gate and the message
// below, which is rendered from it. A hand-written enumeration here went stale
// the moment a platform was added without the string being updated, so the
// error now derives from this list and cannot drift.
func keywordScrapable(p domain.Platform) bool {
	return keywordScrapablePlatforms[p]
}

// keywordScrapablePlatforms is the allowlist. Declared as a map (not a switch)
// so scrapablePlatformNames can render the user-facing error from the same
// data instead of a second hand-maintained copy of the list.
var keywordScrapablePlatforms = map[domain.Platform]bool{
	domain.PlatformInstagram: true,
	domain.PlatformThreads:   true,
	domain.PlatformTikTok:    true,
	domain.PlatformYouTube:   true,
	domain.PlatformLinkedIn:  true,
	domain.PlatformX:         true,
	domain.PlatformFacebook:  true,
}

// scrapablePlatformNames lists the allowlist in the order a user would say it,
// for the validation error. Derived, never typed by hand.
func scrapablePlatformNames() string {
	names := make([]string, 0, len(keywordScrapablePlatforms))
	for _, p := range domain.AllPlatforms {
		if keywordScrapablePlatforms[p] {
			names = append(names, string(p))
		}
	}
	return strings.Join(names, ", ")
}

// CreateBatch validates, inserts keyword_batch PENDING and launches background
// processing. Returns the batch immediately (202).
func (s *KeywordScrapeService) CreateBatch(ctx context.Context, in KeywordScrapeInput) (domain.KeywordBatch, error) {
	in.Platform = domain.Platform(strings.TrimSpace(string(in.Platform)))
	if !keywordScrapable(in.Platform) {
		return domain.KeywordBatch{}, fmt.Errorf("%w: keyword scrape supports %s only, got %q", domain.ErrValidation, scrapablePlatformNames(), in.Platform)
	}
	cleaned := make([]string, 0, len(in.Keywords))
	seen := map[string]struct{}{}
	for _, k := range in.Keywords {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if _, dup := seen[strings.ToLower(k)]; dup {
			continue
		}
		seen[strings.ToLower(k)] = struct{}{}
		cleaned = append(cleaned, k)
	}
	if len(cleaned) == 0 {
		return domain.KeywordBatch{}, fmt.Errorf("%w: at least one keyword is required", domain.ErrValidation)
	}
	if len(cleaned) > KeywordScrapeMaxKeywords {
		return domain.KeywordBatch{}, fmt.Errorf("%w: at most %d keywords, got %d", domain.ErrValidation, KeywordScrapeMaxKeywords, len(cleaned))
	}
	if s.runner == nil {
		return domain.KeywordBatch{}, fmt.Errorf("%w: scrape runner is not configured", domain.ErrUnavailable)
	}
	if s.batches == nil {
		return domain.KeywordBatch{}, fmt.Errorf("%w: keyword batch store is not configured", domain.ErrUnavailable)
	}
	actorID := s.searchActorFor(in.Platform)
	if actorID == "" {
		return domain.KeywordBatch{}, fmt.Errorf("%w: no search actor configured for %s", domain.ErrValidation, in.Platform)
	}
	// The ceiling goes here, before the batch row exists and long before the
	// actor runs: every check above is free, and a refusal must not leave a
	// PENDING row that a background goroutine would pay for anyway.
	if in.Caller != "" && !s.allowSpend(in.Caller) {
		return domain.KeywordBatch{}, fmt.Errorf("%w: keyword scrape rate limit exceeded; try again in a minute", domain.ErrRateLimited)
	}
	maxPosts := in.MaxPosts
	if maxPosts <= 0 {
		maxPosts = 50
	}
	if maxPosts > 200 {
		maxPosts = 200
	}
	var wf, wt *time.Time
	if !in.Window.From.IsZero() {
		t := in.Window.From.UTC()
		wf = &t
	}
	if !in.Window.To.IsZero() {
		t := in.Window.To.UTC()
		wt = &t
	}
	batch, err := s.batches.CreateKeywordBatch(ctx, domain.KeywordBatch{
		Platform:   in.Platform,
		Keywords:   cleaned,
		WindowFrom: wf,
		WindowTo:   wt,
		MaxPosts:   maxPosts,
		ActorID:    actorID,
		Status:     domain.KeywordBatchPending,
		CreatedBy:  in.CreatedBy,
	})
	if err != nil {
		return domain.KeywordBatch{}, fmt.Errorf("keyword batch: create: %w", err)
	}
	// Background: detached context so request cancel does not abort Apify run.
	bg := context.Background()
	go s.processBatch(bg, batch.ID, in.Platform, cleaned, in.Window, maxPosts, actorID)
	return batch, nil
}

// processBatch runs on a detached goroutine: no request context, no recovery.
// A panic anywhere in this stack — the store, the ingestor, the Apify runner —
// would otherwise take the whole API process down over one batch, and leave the
// batch row RUNNING forever with no trace of why. So the panic is contained
// here, recorded, and the batch is failed like any other terminal state.
func (s *KeywordScrapeService) processBatch(ctx context.Context, batchID string, platform domain.Platform, keywords []string, window KeywordWindow, maxPosts int, actorID string) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("keyword batch: panic", "batch", batchID, "platform", platform, "panic", r)
			s.failBatch(ctx, batchID, fmt.Sprintf("panic: %v", r))
		}
	}()
	batch, err := s.batches.GetKeywordBatch(ctx, batchID)
	if err != nil {
		s.log.Error("keyword batch: fetch failed", "batch", batchID, "err", err)
		return
	}
	batch.Status = domain.KeywordBatchRunning
	if _, err := s.batches.UpdateKeywordBatch(ctx, batch); err != nil {
		s.log.Warn("keyword batch: mark running failed", "batch", batchID, "err", err)
	}

	// One apify_run per attempt, not one per batch: raw payloads are keyed to the
	// run id, so reusing a run would leave the failed attempt's items in storage
	// for the retry's ingest to read back.
	var (
		res port.ScrapeIngestResult
		run domain.ApifyRun
	)
	for attempt := 1; attempt <= keywordScrapeAttempts; attempt++ {
		run, err = s.scrapes.CreateApifyRun(ctx, domain.ApifyRun{ActorID: actorID, Status: "RUNNING"})
		if err != nil {
			s.failBatch(ctx, batchID, fmt.Sprintf("create run: %v", err))
			return
		}
		if _, err := s.batches.UpdateKeywordBatch(ctx, domain.KeywordBatch{ID: batchID, Status: domain.KeywordBatchRunning, ApifyRunID: &run.ID}); err != nil {
			s.log.Warn("keyword batch: set apify_run failed", "batch", batchID, "err", err)
		}
		res, err = s.runOnce(ctx, run.ID, actorID, platform, keywords, window, maxPosts)
		if err != nil {
			s.failBatch(ctx, batchID, err.Error())
			return
		}
		if res.PostsUpserted > 0 || attempt == keywordScrapeAttempts {
			break
		}
		s.log.Warn("keyword scrape: empty result, retrying", "batch", batchID, "attempt", attempt)
	}

	// Join exactly the posts this run ingested. Reading the platform's newest
	// posts instead would let a batch claim rows another batch (or a single
	// post scrape) just wrote.
	for _, postID := range res.PostIDs {
		// Logged, not discarded: a failed link means the post was paid for and
		// stored but never appears in ListBatchPosts, so the batch dashboard
		// under-reports with nothing on the run itself to explain why.
		if err := s.batches.CreateKeywordBatchPost(ctx, batchID, postID); err != nil {
			s.log.Warn("keyword batch: link post failed", "batch", batchID, "post", postID, "err", err)
		}
	}
	n := time.Now().UTC()
	if _, err := s.batches.UpdateKeywordBatch(ctx, domain.KeywordBatch{
		ID: batchID, Status: domain.KeywordBatchSucceeded, ApifyRunID: &run.ID,
		// ItemsRead is the actor's delivered item count; PostsCount is what
		// survived the operator's time window. They differ by design, and the
		// divergence is how a too-narrow window shows up on the dashboard.
		ItemsRead: res.ItemsRead, PostsCount: res.PostsUpserted, CommentsCount: res.CommentsUpserted,
		FinishedAt: &n,
	}); err != nil {
		s.log.Warn("keyword batch: mark succeeded failed", "batch", batchID, "err", err)
	}
}

// runOnce drives the actor once and ingests what it returned. A non-SUCCEEDED
// run is an error (the batch is terminal); zero posts is a result the caller
// decides how to treat (see keywordScrapeAttempts).
//
// The apify_run audit row is closed on EVERY exit path, including the failure
// ones. It previously sat after the SUCCEEDED check, so a transport error, an
// HTTP 4xx/5xx (the runner reports those as Status: FAILED with a nil error),
// or an ingest error each left the row RUNNING with finished_at NULL — the
// audit table reporting the opposite of what happened, and attempt 1's run
// never closed by the retry.
func (s *KeywordScrapeService) runOnce(ctx context.Context, runID, actorID string, platform domain.Platform, keywords []string, window KeywordWindow, maxPosts int) (port.ScrapeIngestResult, error) {
	// closeRun writes the terminal status and stamps finished_at. Called from
	// every return below. The write is deliberately on a context that survives
	// the run's cancellation: a batch goroutine can be cancelled mid-flight, and
	// an audit row left RUNNING is exactly the bug this closes.
	closeRun := func(status string) {
		if _, err := s.scrapes.UpdateApifyRun(context.WithoutCancel(ctx), domain.ApifyRun{
			ID: runID, Status: status,
		}, true); err != nil {
			s.log.Warn("keyword scrape: close run failed", "run", runID, "status", status, "err", err)
		}
	}

	out, err := s.runner.RunSearch(ctx, port.ApifySearchInput{
		RunID: runID, ActorID: actorID, Platform: platform, Keywords: keywords,
		Window: port.KeywordTimeWindow{From: window.From, To: window.To}, MaxPosts: maxPosts,
	})
	if err != nil {
		s.log.Warn("keyword scrape: run failed", "run", runID, "err", err)
		closeRun("FAILED")
		return port.ScrapeIngestResult{}, fmt.Errorf("run: %w", err)
	}
	if out.Status != "SUCCEEDED" {
		closeRun(out.Status)
		return port.ScrapeIngestResult{}, fmt.Errorf("actor %s: %s", out.Status, out.Error)
	}
	// Record the run id + dataset keys, then normalize the payload.
	if _, err := s.scrapes.UpdateApifyRun(ctx, domain.ApifyRun{ID: runID, RunID: out.RunID, Status: out.Status}, true); err != nil {
		s.log.Warn("keyword scrape: update run failed", "run", runID, "err", err)
	}
	for _, item := range out.Items {
		if _, err := s.scrapes.CreateRawPayload(ctx, domain.RawPayload{
			ApifyRunID: runID, S3Key: item.Key, Bytes: item.Bytes,
		}); err != nil {
			s.log.Warn("keyword scrape: payload pointer failed", "key", item.Key, "err", err)
		}
	}
	ingestor := NewSearchScrapeIngestor(s.scrapes, s.storageRef, platform, port.KeywordTimeWindow{From: window.From, To: window.To}, s.log)
	res, err := ingestor.IngestRun(ctx, runID)
	if err != nil {
		// The actor succeeded; the ingest did not. The row must not stay RUNNING
		// just because the failure happened one step later.
		closeRun("FAILED")
		return port.ScrapeIngestResult{}, fmt.Errorf("ingest: %w", err)
	}
	return res, nil
}

func (s *KeywordScrapeService) failBatch(ctx context.Context, batchID, msg string) {
	n := time.Now().UTC()
	if _, err := s.batches.UpdateKeywordBatch(ctx, domain.KeywordBatch{
		ID: batchID, Status: domain.KeywordBatchFailed, Error: &msg, FinishedAt: &n,
	}); err != nil {
		s.log.Warn("keyword batch: mark failed failed", "batch", batchID, "err", err)
	}
}

// ScrapeKeywords legacy sync (used by tests). Prefer CreateBatch.
func (s *KeywordScrapeService) ScrapeKeywords(ctx context.Context, in KeywordScrapeInput) (KeywordScrapeResult, error) {
	batch, err := s.CreateBatch(ctx, in)
	if err != nil {
		return KeywordScrapeResult{}, err
	}
	// Poll until terminal (for tests; production uses async batch endpoints).
	for i := 0; i < 120; i++ {
		time.Sleep(100 * time.Millisecond)
		b, err := s.batches.GetKeywordBatch(ctx, batch.ID)
		if err != nil {
			continue
		}
		if b.Status.IsTerminal() {
			if b.Status == domain.KeywordBatchFailed {
				msg := ""
				if b.Error != nil {
					msg = *b.Error
				}
				return KeywordScrapeResult{}, fmt.Errorf("keyword scrape: %s", msg)
			}
			posts, _ := s.batches.ListKeywordBatchPosts(ctx, batch.ID, &b.MaxPosts, nil)
			if len(posts) == 0 {
				posts, _ = s.scrapes.ListRecentPosts(ctx, in.Platform, b.MaxPosts)
			}
			return KeywordScrapeResult{Posts: posts, Comments: b.CommentsCount, Keywords: b.Keywords, Platform: string(b.Platform), ActorID: b.ActorID, ItemsRead: b.ItemsRead}, nil
		}
	}
	b, _ := s.batches.GetKeywordBatch(ctx, batch.ID)
	if b.Status == domain.KeywordBatchFailed && b.Error != nil {
		return KeywordScrapeResult{}, fmt.Errorf("keyword scrape: %s", *b.Error)
	}
	return KeywordScrapeResult{}, fmt.Errorf("keyword scrape: timeout waiting for batch %s", batch.ID)
}

// GetBatch fetches one batch.
func (s *KeywordScrapeService) GetBatch(ctx context.Context, id string) (domain.KeywordBatch, error) {
	if s.batches == nil {
		return domain.KeywordBatch{}, fmt.Errorf("%w: batch store not configured", domain.ErrUnavailable)
	}
	return s.batches.GetKeywordBatch(ctx, id)
}

// ListBatches lists batches newest first.
func (s *KeywordScrapeService) ListBatches(ctx context.Context, limit, offset *int) ([]domain.KeywordBatch, error) {
	if s.batches == nil {
		return nil, fmt.Errorf("%w: batch store not configured", domain.ErrUnavailable)
	}
	return s.batches.ListKeywordBatches(ctx, limit, offset)
}

// ListBatchPosts returns posts ingested as part of a batch.
func (s *KeywordScrapeService) ListBatchPosts(ctx context.Context, batchID string, limit, offset *int) ([]domain.Post, error) {
	if s.batches == nil {
		return nil, fmt.Errorf("%w: batch store not configured", domain.ErrUnavailable)
	}
	return s.batches.ListKeywordBatchPosts(ctx, batchID, limit, offset)
}

// PostComments returns the comments already stored for one post. Keyword search
// ingests each item's comment list, so the batch dashboard can show them
// without a second paid scrape — the point is to read what we already paid for.
func (s *KeywordScrapeService) PostComments(ctx context.Context, postID string, limit, offset *int) ([]domain.Comment, error) {
	if s.scrapes == nil {
		return nil, fmt.Errorf("%w: scrape store not configured", domain.ErrUnavailable)
	}
	return s.scrapes.ListCommentsByPost(ctx, postID, limit, offset)
}
