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

// KeywordScrapeService (search) runs the platform's search actor over up to 5
// keywords inside a time window, ingests the dataset (posts + comments), and
// returns the stored posts. The rows land in the same post/comment tables the
// on-demand scrape uses, so a keyword search result is reusable by every other
// scrape-driven feature.
type KeywordScrapeService struct {
	scrapes  port.ScrapeStore
	runner   port.ApifyRunner
	actorFor func(domain.Platform) string
	// searchActorFor maps a platform to its SEARCH actor (distinct from the
	// post-scraper: it takes keywords, not a permalink). Empty → no search for
	// that platform. Falls back to actorFor when no dedicated search actor is
	// configured — the IG/Threads scrapers accept searchTerms directly.
	searchActorFor func(domain.Platform) string
	storageRef     port.RawStorage
	log            *slog.Logger
}

// NewKeywordScrapeService wires the keyword scraper. runner nil → 503.
// searchActorFor nil → falls back to the post-scraper mapping (same actor ids).
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

// KeywordWindow is the time filter the search runs under.
type KeywordWindow struct {
	// From/To bound the post dates the actor filters on (UTC). Zero values
	// mean "no bound" for that side (the actor decides).
	From time.Time
	To   time.Time
}

// KeywordScrapeInput is one search request.
type KeywordScrapeInput struct {
	Platform domain.Platform
	// Keywords is 1..5 search terms.
	Keywords []string
	Window   KeywordWindow
	// MaxPosts bounds posts returned across the whole run (default 50).
	MaxPosts int
}

// KeywordScrapeResult is the outcome: the posts stored for this search (fresh
// first) plus counters for the progress UI.
type KeywordScrapeResult struct {
	Posts     []domain.Post `json:"posts"`
	Comments  int           `json:"comments"`
	Keywords  []string      `json:"keywords"`
	Platform  string        `json:"platform"`
	ActorID   string        `json:"actorId"`
	ItemsRead int           `json:"itemsRead"`
}

// ScrapeKeywords runs the search actor and ingests everything it returns.
// Synchronous: the dashboard shows live progress and reads the result when the
// request lands.
func (s *KeywordScrapeService) ScrapeKeywords(ctx context.Context, in KeywordScrapeInput) (KeywordScrapeResult, error) {
	in.Platform = domain.Platform(strings.TrimSpace(string(in.Platform)))
	if in.Platform != domain.PlatformInstagram && in.Platform != domain.PlatformThreads {
		return KeywordScrapeResult{}, fmt.Errorf("%w: keyword scrape supports instagram and threads only, got %q", domain.ErrValidation, in.Platform)
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
		return KeywordScrapeResult{}, fmt.Errorf("%w: at least one keyword is required", domain.ErrValidation)
	}
	if len(cleaned) > KeywordScrapeMaxKeywords {
		return KeywordScrapeResult{}, fmt.Errorf("%w: at most %d keywords, got %d", domain.ErrValidation, KeywordScrapeMaxKeywords, len(cleaned))
	}
	if s.runner == nil {
		return KeywordScrapeResult{}, fmt.Errorf("%w: scrape runner is not configured", domain.ErrUnavailable)
	}
	actorID := s.searchActorFor(in.Platform)
	if actorID == "" {
		return KeywordScrapeResult{}, fmt.Errorf("%w: no search actor configured for %s", domain.ErrValidation, in.Platform)
	}
	maxPosts := in.MaxPosts
	if maxPosts <= 0 {
		maxPosts = 50
	}
	if maxPosts > 200 {
		maxPosts = 200
	}

	// Audit row for the run; the ingestor keys off it.
	run, err := s.scrapes.CreateApifyRun(ctx, domain.ApifyRun{
		ActorID: actorID,
		Status:  "RUNNING",
	})
	if err != nil {
		return KeywordScrapeResult{}, fmt.Errorf("keyword scrape: create run: %w", err)
	}

	out, err := s.runner.RunSearch(ctx, port.ApifySearchInput{
		RunID:    run.ID,
		ActorID:  actorID,
		Platform: in.Platform,
		Keywords: cleaned,
		Window: port.KeywordTimeWindow{
			From: in.Window.From,
			To:   in.Window.To,
		},
		MaxPosts: maxPosts,
	})
	if err != nil {
		s.log.Warn("keyword scrape: run failed", "err", err)
		return KeywordScrapeResult{}, fmt.Errorf("keyword scrape: run: %w", err)
	}
	if out.Status != "SUCCEEDED" {
		return KeywordScrapeResult{}, fmt.Errorf("keyword scrape: actor %s: %s", out.Status, out.Error)
	}
	if _, err := s.scrapes.UpdateApifyRun(ctx, domain.ApifyRun{ID: run.ID, RunID: out.RunID, Status: out.Status}, true); err != nil {
		s.log.Warn("keyword scrape: update run failed", "run", run.ID, "err", err)
	}
	for _, key := range out.ItemKeys {
		if _, err := s.scrapes.CreateRawPayload(ctx, domain.RawPayload{ApifyRunID: run.ID, S3Key: key}); err != nil {
			s.log.Warn("keyword scrape: payload pointer failed", "key", key, "err", err)
		}
	}

	// The search actor ignores since/until, so the ingestor applies the window
	// itself (drop posts whose taken_at is outside it).
	ingestor := NewSearchScrapeIngestor(s.scrapes, s.storageRef, in.Platform, port.KeywordTimeWindow{
		From: in.Window.From,
		To:   in.Window.To,
	}, s.log)
	res, err := ingestor.IngestRun(ctx, run.ID)
	if err != nil {
		return KeywordScrapeResult{}, fmt.Errorf("keyword scrape: ingest: %w", err)
	}

	// Read the posts back. The ingest is keyed by (platform, external_id);
	// this search's rows just got the freshest scraped_at, so a read by
	// recent scrape time returns exactly what the run stored.
	posts, err := s.scrapes.ListRecentPosts(ctx, in.Platform, maxPosts)
	if err != nil {
		return KeywordScrapeResult{}, fmt.Errorf("keyword scrape: read back: %w", err)
	}
	return KeywordScrapeResult{
		Posts:     posts,
		Comments:  res.CommentsUpserted,
		Keywords:  cleaned,
		Platform:  string(in.Platform),
		ActorID:   actorID,
		ItemsRead: res.PostsUpserted,
	}, nil
}
