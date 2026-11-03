package service

import (
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// A permalink scrape has to pull the post id the ingestor keys on out of each
// platform's OWN route. Every case below is a URL the dashboard actually hands
// over from a stored scraped post; a miss here rejects the scrape before it
// spends anything, and a WRONG id silently rewrites a different post's row.
func TestPostExternalIDFromURLPerPlatform(t *testing.T) {
	cases := []struct {
		platform domain.Platform
		url      string
		want     string
	}{
		{domain.PlatformInstagram, "https://www.instagram.com/p/ABC123/?img_index=1", "ABC123"},
		{domain.PlatformInstagram, "https://www.instagram.com/reel/XYZ/", "XYZ"},
		{domain.PlatformThreads, "https://www.threads.com/post/TH1", "TH1"},
		{domain.PlatformTikTok, "https://www.tiktok.com/@some.user/video/7692013391996620084", "7692013391996620084"},
		{domain.PlatformYouTube, "https://www.youtube.com/watch?v=k_ldBC5mQu8", "k_ldBC5mQu8"},
		{domain.PlatformYouTube, "https://www.youtube.com/shorts/abc123?feature=share", "abc123"},
		{domain.PlatformYouTube, "https://youtu.be/shortid", "shortid"},
		{domain.PlatformX, "https://x.com/someone/status/2112141142557089900", "2112141142557089900"},
		{domain.PlatformX, "https://twitter.com/someone/status/2112", "2112"},
		// Not a post: a profile. Must yield "", so no paid run starts.
		{domain.PlatformTikTok, "https://www.tiktok.com/@some.user", ""},
		{domain.PlatformYouTube, "https://www.youtube.com/@channel", ""},
		{domain.PlatformX, "https://x.com/someone", ""},
	}
	for _, c := range cases {
		if got := postExternalIDFromURL(c.platform, c.url); got != c.want {
			t.Errorf("postExternalIDFromURL(%s, %q) = %q, want %q", c.platform, c.url, got, c.want)
		}
	}
}

// The TikTok on-demand actor and the TikTok search actor spell the same facts
// under different keys, under different nesting, and with different types
// (collectCount is a STRING). One parser reads both — but only if every
// fallback is wired, and a missed one is silent: the post stores with an empty
// caption or a 1970 date and still looks like a successful scrape.
func TestParseTikTokReadsBothActorShapes(t *testing.T) {
	// Captured from logical_scrapers/tiktok-realtime-post-details-scraper,
	// run 2026-10-22 against @mirealilly/7695526042013846792.
	postActorRow := `{
	  "id": "7695526042013846792",
	  "desc": "Slamat datang di Bandar udara Bersujud,Batulicin",
	  "createTime": "1791766800",
	  "video": {"cover": "https://cdn.example/cover.jpg"},
	  "author": {"id": "7060485688155980827", "uniqueId": "mirealilly"},
	  "stats": {"diggCount": 2517, "shareCount": 154, "commentCount": 78, "playCount": 53400, "collectCount": "120"}
	}`
	items, err := parseTikTokItems([]byte(postActorRow), port.KeywordTimeWindow{})
	if err != nil {
		t.Fatalf("post-actor row: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("post-actor row yielded %d items, want 1", len(items))
	}
	got := items[0]
	if got.Text == "" {
		t.Error("caption empty: the post actor spells it `desc`, not `title`")
	}
	if got.AuthorHandle != "mirealilly" {
		t.Errorf("author_handle = %q, want mirealilly (from author.uniqueId)", got.AuthorHandle)
	}
	if got.PublishedAt == nil || got.PublishedAt.IsZero() {
		t.Error("published_at empty: the post actor sends createTime as a Unix-seconds STRING")
	}
	for _, key := range []string{"views", "likes", "comments", "shares", "collects"} {
		if _, ok := got.Metrics[key]; !ok {
			t.Errorf("metrics[%q] missing: the post actor sends stats.{play,digg,comment,share}Count", key)
		}
	}

	// The search actor's shape, unchanged: this is the regression guard that
	// the second spelling did not break the first.
	searchRow := `{
	  "id": "7692013391996620084",
	  "title": "Search-actor caption",
	  "uploadedAtFormatted": "2026-10-02T10:20:38.000Z",
	  "views": 1, "likes": 2, "comments": 3, "shares": 4, "bookmarks": 5,
	  "video": {"cover": "https://cdn.example/c.jpg"},
	  "channel": {"id": "99", "username": "nazada.741.coffee"}
	}`
	items, err = parseTikTokItems([]byte(searchRow), port.KeywordTimeWindow{})
	if err != nil {
		t.Fatalf("search-actor row: %v", err)
	}
	got = items[0]
	if got.Text != "Search-actor caption" || got.AuthorHandle != "nazada.741.coffee" {
		t.Errorf("search-actor row regressed: text=%q handle=%q", got.Text, got.AuthorHandle)
	}
	if got.Metrics["collects"] != int64(5) {
		t.Errorf("search-actor collects = %v, want 5", got.Metrics["collects"])
	}
}

// Only the two platforms whose post actor returns comment bodies may stamp a
// post comment-fetched. Stamping the rest would serve "no comments" from cache
// for six hours on a post whose comments were never requested.
func TestOnlyCommentReturningActorsStampFetched(t *testing.T) {
	for _, p := range []domain.Platform{domain.PlatformInstagram, domain.PlatformThreads} {
		if !platformFetchesComments(p) {
			t.Errorf("%s should stamp comments-fetched", p)
		}
	}
	for _, p := range []domain.Platform{domain.PlatformTikTok, domain.PlatformYouTube, domain.PlatformX} {
		if platformFetchesComments(p) {
			t.Errorf("%s must not stamp comments-fetched: its actor returns the post only", p)
		}
	}
}
