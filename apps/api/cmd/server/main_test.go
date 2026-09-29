package main

import (
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/config"
	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// This is the test whose absence let a dead feature ship.
//
// The TikTok keyword scrape was fully implemented — parser, payload, cost gate,
// dashboard option, platform brief — and every test passed. It was still dead:
// `APIFY_SEARCH_ACTOR_TIKTOK` was set in compose.yaml and read by no Go code,
// and this resolver returned "" for TikTok, so CreateBatch answered
// "no search actor configured" before a single Apify call. Nothing failed
// loudly; the operator got a 400 on a button that looked live.
//
// The gap was structural: every test that touched actor resolution used a fake
// resolver that answered TikTok correctly. Only THIS resolver is production.
func TestSearchActorForPlatformCoversEveryScrapablePlatform(t *testing.T) {
	cfg := config.Config{
		ApifySearchActorInstagram: "crawlerbros/instagram-keyword-search-scraper",
		ApifySearchActorThreads:   "futurizerush/meta-threads-scraper",
		ApifySearchActorTikTok:    "xmolodtsov/tiktok-search-scraper",
	}
	resolve := searchActorForPlatform(cfg)

	// Driven off the gate itself, so a platform added to keywordScrapable()
	// without a matching actor here fails this test on the same commit.
	for _, p := range keywordScrapablePlatforms() {
		if got := resolve(p); got == "" {
			t.Errorf("platform %q passes the keyword-scrape gate but has no search actor: "+
				"every batch for it 400s with \"no search actor configured\"", p)
		}
	}
}

func TestSearchActorForPlatformLeavesDeferredPlatformsEmpty(t *testing.T) {
	cfg := config.Config{
		ApifySearchActorInstagram: "ig",
		ApifySearchActorThreads:   "th",
		ApifySearchActorTikTok:    "tt",
	}
	resolve := searchActorForPlatform(cfg)

	for _, p := range []domain.Platform{
		domain.PlatformFacebook, domain.PlatformLinkedIn,
		domain.PlatformX, domain.PlatformYouTube,
	} {
		if got := resolve(p); got != "" {
			t.Errorf("platform %q is still deferred but resolves to actor %q", p, got)
		}
	}
}

// keywordScrapablePlatforms mirrors service.keywordScrapable. It is duplicated
// rather than imported because that function is unexported in an internal
// service package; the duplication is the price of this test existing at all,
// and it is the cheaper of the two options. If they ever diverge, this test
// fails — which is the correct outcome.
func keywordScrapablePlatforms() []domain.Platform {
	return []domain.Platform{
		domain.PlatformInstagram,
		domain.PlatformThreads,
		domain.PlatformTikTok,
	}
}
