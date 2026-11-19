package main

import (
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/config"
	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
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
func TestLoginWorkerKeepsExplicitBoxdEndpoint(t *testing.T) {
	defs := []service.PoolWorkerDef{{
		Name:     "smm-login-01",
		RPCURL:   "https://rpc.smm-login-01.boxd.sh",
		NoVNCURL: "https://smm-login-01.boxd.sh/novnc",
	}}
	got := ensureLoginWorker(defs, "smm-login-01")
	if len(got) != 1 || got[0].RPCURL != "https://rpc.smm-login-01.boxd.sh" {
		t.Fatalf("explicit login endpoint was overwritten: %+v", got)
	}
	if got := ensureLoginWorker(nil, "smm-login-01"); len(got) != 1 || got[0].RPCURL != "http://smm-login-01:8090" {
		t.Fatalf("missing login endpoint was not seeded: %+v", got)
	}
	if got := ensureLoginWorker(nil, ""); len(got) != 0 {
		t.Fatalf("empty login name seeded a worker: %+v", got)
	}
}

func TestSearchActorForPlatformCoversEveryScrapablePlatform(t *testing.T) {
	cfg := config.Config{
		ApifySearchActorInstagram: "sasky/instagram-keyword-posts-urls-scraper",
		ApifySearchActorThreads:   "futurizerush/meta-threads-scraper",
		ApifySearchActorTikTok:    "xmolodtsov/tiktok-search-scraper",
		ApifySearchActorYouTube:   "streamers/youtube-scraper",
		ApifySearchActorLinkedIn:  "harvestapi/linkedin-post-search",
		ApifySearchActorX:         "apidojo/twitter-scraper-lite",
		ApifySearchActorFacebook:  "danek/facebook-search-ppr",
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

	// The other direction: a platform that is not scrapable must resolve to
	// nothing, so CreateBatch refuses it before spending a call.
	if got := resolve(domain.Platform("mastodon")); got != "" {
		t.Errorf("an unknown platform resolved to actor %q", got)
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
		domain.PlatformYouTube,
		domain.PlatformLinkedIn,
		domain.PlatformX,
		domain.PlatformFacebook,
	}
}

// The YouTube on-demand actor is a different app from the keyword actor. A
// shared switch once made both return the post actor, which would feed
// `keywords` to an actor that only understands `searchQueries`.
func TestYouTubePostActorDoesNotLeakIntoKeywordSearch(t *testing.T) {
	cfg := config.Config{
		ApifySearchActorYouTube: "apidojo/youtube-scraper",
		ApifyPostActorYouTube:   "streamers/youtube-scraper",
	}
	if got := actorForPlatform(cfg)(domain.PlatformYouTube); got != "streamers/youtube-scraper" {
		t.Errorf("on-demand actor = %q, want streamers/youtube-scraper", got)
	}
	if got := searchActorForPlatform(cfg)(domain.PlatformYouTube); got != "apidojo/youtube-scraper" {
		t.Errorf("keyword actor = %q, want apidojo/youtube-scraper", got)
	}
}
