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

// CreateBatch validates, inserts keyword_batch PENDING and launches background
// processing. Returns the batch immediately (202).
func (s *KeywordScrapeService) CreateBatch(ctx context.Context, in KeywordScrapeInput) (domain.KeywordBatch, error) {
	in.Platform = domain.Platform(strings.TrimSpace(string(in.Platform)))
	if in.Platform != domain.PlatformInstagram && in.Platform != domain.PlatformThreads {
		return domain.KeywordBatch{}, fmt.Errorf("%w: keyword scrape supports instagram and threads only, got %q", domain.ErrValidation, in.Platform)
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

func (s *KeywordScrapeService) processBatch(ctx context.Context, batchID string, platform domain.Platform, keywords []string, window KeywordWindow, maxPosts int, actorID string) {
	batch, err := s.batches.GetKeywordBatch(ctx, batchID)
	if err != nil {
		s.log.Error("keyword batch: fetch failed", "batch", batchID, "err", err)
		return
	}
	batch.Status = domain.KeywordBatchRunning
	if _, err := s.batches.UpdateKeywordBatch(ctx, batch); err != nil {
		s.log.Warn("keyword batch: mark running failed", "batch", batchID, "err", err)
	}
	if _, err := s.batches.UpdateKeywordBatch(ctx, domain.KeywordBatch{ID: batchID, Status: domain.KeywordBatchRunning}); err != nil {
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
		_ = s.batches.CreateKeywordBatchPost(ctx, batchID, postID)
	}
	n := time.Now().UTC()
	if _, err := s.batches.UpdateKeywordBatch(ctx, domain.KeywordBatch{
		ID: batchID, Status: domain.KeywordBatchSucceeded, ApifyRunID: &run.ID,
		ItemsRead: res.PostsUpserted, PostsCount: res.PostsUpserted, CommentsCount: res.CommentsUpserted,
		FinishedAt: &n,
	}); err != nil {
		s.log.Warn("keyword batch: mark succeeded failed", "batch", batchID, "err", err)
	}
}

// runOnce drives the actor once and ingests what it returned. A non-SUCCEEDED
// run is an error (the batch is terminal); zero posts is a result the caller
// decides how to treat (see keywordScrapeAttempts).
func (s *KeywordScrapeService) runOnce(ctx context.Context, runID, actorID string, platform domain.Platform, keywords []string, window KeywordWindow, maxPosts int) (port.ScrapeIngestResult, error) {
	out, err := s.runner.RunSearch(ctx, port.ApifySearchInput{
		RunID: runID, ActorID: actorID, Platform: platform, Keywords: keywords,
		Window: port.KeywordTimeWindow{From: window.From, To: window.To}, MaxPosts: maxPosts,
	})
	if err != nil {
		s.log.Warn("keyword scrape: run failed", "run", runID, "err", err)
		return port.ScrapeIngestResult{}, fmt.Errorf("run: %w", err)
	}
	if out.Status != "SUCCEEDED" {
		return port.ScrapeIngestResult{}, fmt.Errorf("actor %s: %s", out.Status, out.Error)
	}
	if _, err := s.scrapes.UpdateApifyRun(ctx, domain.ApifyRun{ID: runID, RunID: out.RunID, Status: out.Status}, true); err != nil {
		s.log.Warn("keyword scrape: update run failed", "run", runID, "err", err)
	}
	for _, key := range out.ItemKeys {
		if _, err := s.scrapes.CreateRawPayload(ctx, domain.RawPayload{ApifyRunID: runID, S3Key: key}); err != nil {
			s.log.Warn("keyword scrape: payload pointer failed", "key", key, "err", err)
		}
	}
	ingestor := NewSearchScrapeIngestor(s.scrapes, s.storageRef, platform, port.KeywordTimeWindow{From: window.From, To: window.To}, s.log)
	res, err := ingestor.IngestRun(ctx, runID)
	if err != nil {
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
