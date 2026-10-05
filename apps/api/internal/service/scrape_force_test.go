package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// Force bypasses the 6h freshness cache. It has to, and the reason is specific
// enough to be worth pinning: a keyword batch marks every post it ingests as
// fresh (UpsertPost sets scraped_at = now()) but the search actor returns no
// comment bodies. So without force, a post the operator just scraped in a batch
// would answer "no comments" for the next 6 hours, every single time, and the
// button would look broken rather than cached.

// countingRunner records how many paid actor runs a scrape actually made.
type countingRunner struct {
	runs int
}

func (c *countingRunner) Run(context.Context, port.ApifyInput) (port.ApifyOutput, error) {
	c.runs++
	return port.ApifyOutput{RunID: "run-1", Status: "SUCCEEDED"}, nil
}

func (c *countingRunner) RunSearch(context.Context, port.ApifySearchInput) (port.ApifyOutput, error) {
	return port.ApifyOutput{}, nil
}

// freshStore returns a post scraped one minute ago — inside the 6h window.
// commentsFetched mirrors whether a permalink scrape (true) or a keyword-batch
// ingest (false) produced the row; the cache only serves the former.
func freshStore(t *testing.T, commentsFetched bool) *fakeScrapeStore {
	t.Helper()
	store := newFakeScrapeStore()
	const url = "https://www.instagram.com/p/ABC123/"
	// fakeScrapeStore keys targets by platform|externalID, like the SQL.
	store.targets[string(domain.PlatformInstagram)+"|"+url] = domain.Target{
		ID:         "tgt-1",
		Kind:       domain.TargetKindPost,
		Platform:   domain.PlatformInstagram,
		ExternalID: url,
		URL:        url,
		PostID:     strptr("post-1"),
	}
	store.posts["post-1"] = domain.Post{
		ID:              "post-1",
		Platform:        domain.PlatformInstagram,
		ExternalID:      "ABC123",
		ScrapedAt:       time.Now().Add(-time.Minute),
		CommentsFetched: commentsFetched,
	}
	return store
}

func strptr(s string) *string { return &s }

func actorForAny(domain.Platform) string { return "test/actor" }

// A post inside the freshness window whose comments were fetched is served
// from cache with force=false — the repeat-scrape case that used to pay.
func TestScrapeTargetCachesFreshPostWithoutForce(t *testing.T) {
	store := freshStore(t, true)
	runner := &countingRunner{}
	svc := NewScrapeService(store, runner, actorForAny, nil, nil)

	detail, err := svc.ScrapeTarget(context.Background(), domain.PlatformInstagram,
		"https://www.instagram.com/p/ABC123/", false)
	if err != nil {
		t.Fatalf("ScrapeTarget: %v", err)
	}
	if runner.runs != 0 {
		t.Errorf("made %d actor runs, want 0 (a fresh comment-complete post must be served from cache)", runner.runs)
	}
	if !detail.Cached {
		t.Error("a cache-served post must report Cached=true so the dashboard can say it is stale")
	}
}

// A keyword-batch row is fresh but its comments were never fetched, so the
// cache must NOT serve it: the operator would otherwise get "no comments"
// treated as a fresh answer for six hours. This is the case that forced the
// old force=true hack.
func TestScrapeTargetPaysForFreshPostWithoutComments(t *testing.T) {
	store := freshStore(t, false)
	runner := &countingRunner{}
	svc := NewScrapeService(store, runner, actorForAny, nil, nil)

	// The fake store has no apify_run/scrape_job rows, so ScrapeTarget fails
	// after the runner — which is fine: the assertion is that the runner was
	// reached at all, i.e. the cache did not short-circuit a comment-less row.
	_, _ = svc.ScrapeTarget(context.Background(), domain.PlatformInstagram,
		"https://www.instagram.com/p/ABC123/", false)

	if runner.runs == 0 {
		t.Error("a fresh post without fetched comments must still run the actor, not serve the empty cache")
	}
}

// force=true spends a run even on a fresh, comment-complete post. The operator
// pressed "re-scrape" and wants fresh data, not the cached copy.
func TestScrapeTargetForceBypassesFreshCache(t *testing.T) {
	store := freshStore(t, true)
	runner := &countingRunner{}
	svc := NewScrapeService(store, runner, actorForAny, nil, nil)

	// The fake store has no apify_run/scrape_job rows, so ScrapeTarget fails
	// after the runner — which is fine: the assertion is that the runner was
	// reached at all, i.e. the cache did not short-circuit.
	_, _ = svc.ScrapeTarget(context.Background(), domain.PlatformInstagram,
		"https://www.instagram.com/p/ABC123/", true)

	if runner.runs == 0 {
		t.Error("force=true did not reach the actor run; the freshness cache was not bypassed")
	}
}

// The actor's `username` input also accepts profile and post URLs, so a profile
// link used to buy a paid run that ingested up to 100 of that profile's posts
// before readBack failed — the operator paid, got an error, and 100 unrelated
// rows polluted post and content_metric. Reject the URL before any run.
func TestScrapeTargetRejectsNonPostURLsWithoutSpendingARun(t *testing.T) {
	for _, tc := range []struct{ name, url string }{
		{"profile", "https://www.instagram.com/nasa/"},
		{"hashtag", "https://www.instagram.com/explore/tags/nasa/"},
		{"reels profile path", "https://www.instagram.com/nasa/reels/"},
		{"cloud metadata", "http://169.254.169.254/latest/meta-data/"},
		{"cross-platform", "https://www.threads.net/post/ABC/"},
		{"garbage", "not-a-url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &countingRunner{}
			svc := NewScrapeService(newFakeScrapeStore(), runner, actorForAny, nil, nil)
			_, err := svc.ScrapeTarget(context.Background(), domain.PlatformInstagram, tc.url, true)
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("ScrapeTarget(%q) err = %v, want ErrValidation", tc.url, err)
			}
			if runner.runs != 0 {
				t.Fatalf("a rejected URL still cost %d paid actor runs", runner.runs)
			}
		})
	}
}

// A real post permalink must still get through — the guard is a filter, not a
// new rejection.
func TestScrapeTargetStillAcceptsPostPermalinks(t *testing.T) {
	runner := &countingRunner{}
	svc := NewScrapeService(newFakeScrapeStore(), runner, actorForAny, nil, nil)
	// The fake store has no apify_run/scrape_job rows, so this fails AFTER the
	// guard; reaching the runner is the assertion.
	_, _ = svc.ScrapeTarget(context.Background(), domain.PlatformInstagram,
		"https://www.instagram.com/p/ABC123/", true)
	if runner.runs == 0 {
		t.Fatal("a valid post permalink was rejected by the URL guard")
	}
}
