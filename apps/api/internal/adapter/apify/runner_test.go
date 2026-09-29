package apify

import (
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// pathSegment must collapse the store-id slash to a tilde: Apify 404s on a
// literal "/" in acts/<id> (it reads acts/apify as the actor). Confirmed
// against the live API — apify/instagram-post-scraper → 404,
// apify~instagram-post-scraper → 200.
func TestPathSegment(t *testing.T) {
	cases := map[string]string{
		"apify/instagram-post-scraper":  "apify~instagram-post-scraper",
		"~smm/instagram-search-scraper": "~smm~instagram-search-scraper",
		"/apify/instagram-scraper/":     "apify~instagram-scraper",
		"apify~already-tilde":           "apify~already-tilde",
		"no-slash":                      "no-slash",
	}
	for in, want := range cases {
		if got := pathSegment(in); got != want {
			t.Errorf("pathSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

// The search actors name their fields differently, and a wrong name is SILENT:
// the actor falls back to its own default and returns unrelated posts.
// (apify/instagram-scraper's default search is "restaurant, restaurant prague",
// which is what a mislabelled field actually produced in production.) These
// assertions pin the field names, the array-vs-comma-string difference, and
// each actor's date-bound spelling.
func TestSearchInputPerActor(t *testing.T) {
	r := &Runner{}
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	in := port.ApifySearchInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{"batulicin", "kalsel"},
		Window:   port.KeywordTimeWindow{From: from, To: to},
		MaxPosts: 30,
	}

	ig := r.searchInput(in, 30)
	igkws, ok := ig["keywords"].([]string)
	if !ok || len(igkws) != 2 || igkws[0] != "batulicin" {
		t.Errorf("instagram keywords = %v, want the array %v", ig["keywords"], in.Keywords)
	}
	if ig["maxPosts"] != 30 {
		t.Errorf("instagram maxPosts = %v, want 30", ig["maxPosts"])
	}
	// The old apify/instagram-scraper spellings. Sending them to the crawlerbros
	// actor is not a harmless default: it ignores `search`, so the run would
	// search for nothing and return an empty dataset.
	if _, ok := ig["search"]; ok {
		t.Error("instagram has no comma-joined `search` field; the actor ignores it")
	}
	if _, ok := ig["resultsLimit"]; ok {
		t.Error("instagram has no `resultsLimit` field; the cap is `maxPosts`")
	}
	// This actor's input schema has no date field at all. The window is applied
	// by the ingestor, so sending a bound here would be ignored — and paying for
	// out-of-window posts we then throw away.
	for _, key := range []string{"onlyPostsNewerThan", "start_date", "end_date", "since"} {
		if _, ok := ig[key]; ok {
			t.Errorf("instagram actor takes no date input; %q must not be sent", key)
		}
	}

	in.Platform = domain.PlatformThreads
	th := r.searchInput(in, 30)
	if got, want := th["mode"], "search"; got != want {
		t.Errorf("threads mode = %v, want %q (default is user-posts)", got, want)
	}
	kws, ok := th["keywords"].([]string)
	if !ok || len(kws) != 2 || kws[0] != "batulicin" {
		t.Errorf("threads keywords = %v, want the array %v", th["keywords"], in.Keywords)
	}
	if th["start_date"] != "2026-09-01" || th["end_date"] != "2026-09-24" {
		t.Errorf("threads window: got %v..%v", th["start_date"], th["end_date"])
	}
	if _, ok := th["onlyPostsNewerThan"]; ok {
		t.Error("threads does not read onlyPostsNewerThan; it uses start_date/end_date")
	}
}

// A zero window omits the date fields entirely rather than sending a zero date,
// which an actor would read as 0001-01-01. (The Instagram actor takes no date
// input in any case — this guards against a future edit reintroducing one.)
func TestSearchInputOmitsZeroWindow(t *testing.T) {
	r := &Runner{}
	in := port.ApifySearchInput{Platform: domain.PlatformInstagram, Keywords: []string{"x"}}

	ig := r.searchInput(in, 10)
	for _, k := range []string{"onlyPostsNewerThan", "start_date", "end_date"} {
		if v, ok := ig[k]; ok {
			t.Errorf("instagram must omit %s (the actor has no date input), got %v", k, v)
		}
	}

	in.Platform = domain.PlatformThreads
	th := r.searchInput(in, 10)
	for _, k := range []string{"start_date", "end_date"} {
		if v, ok := th[k]; ok {
			t.Errorf("zero window must omit %s, got %v", k, v)
		}
	}
}

// The per-post Threads payload is pinned against themineworks/threads-scraper's
// live input schema, because a wrong field name there fails SILENTLY: the actor
// falls back to its own default and the run succeeds with the wrong data.
//
// `includeReplies` is the one that matters most. This actor emits each reply as
// its own dataset row and EXCLUDES them by default, so dropping the flag does
// not error — it makes every Threads post ingest with zero comments, which the
// dashboard renders as a post nobody replied to. Field names verified against
// the actor's input schema (2026-09).
func TestThreadsPostInput(t *testing.T) {
	p := threadsPostInput("https://www.threads.com/@ahdirmai/post/DdGJIzFiWkJ", 25)

	if got := p["mode"]; got != "post" {
		t.Errorf("mode = %v, want %q (this actor dispatches on mode, default is profile)", got, "post")
	}
	urls, ok := p["postUrls"].([]string)
	if !ok || len(urls) != 1 || urls[0] != "https://www.threads.com/@ahdirmai/post/DdGJIzFiWkJ" {
		t.Errorf("postUrls = %v, want the permalink in a one-element array", p["postUrls"])
	}
	if got := p["maxPosts"]; got != 25 {
		t.Errorf("maxPosts = %v, want 25", got)
	}
	// The load-bearing flag. Without it there are no reply rows and therefore no
	// comments for any Threads post.
	if got := p["includeReplies"]; got != true {
		t.Errorf("includeReplies = %v, want true — without it the actor drops every reply row "+
			"(excluded by default), so the post ingests with no comments", got)
	}
	// This actor's spelling is maxPosts; the previous one took maxItems. Sending
	// the old name would silently run unbounded (the actor's own default).
	if _, stale := p["maxItems"]; stale {
		t.Error("maxItems is the previous actor's field; this one reads maxPosts")
	}
	if _, stale := p["targets"]; stale {
		t.Error("targets/action are the previous actor's fields; this one reads mode/postsUrls")
	}
}

// The TikTok payload is pinned against clockworks/tiktok-scraper's input schema
// (GdWCkxBtKWOsKjdch, verified 2026-09) for the same reason the others are: a
// wrong field name here fails SILENTLY. The actor accepts the payload, falls
// back to its own defaults, and returns a 200 with the wrong data.
func tiktokInput() (port.ApifySearchInput, map[string]any) {
	r := &Runner{}
	in := port.ApifySearchInput{
		Platform: domain.PlatformTikTok,
		Keywords: []string{"#jadibully", "kopi susu"},
		Window: port.KeywordTimeWindow{
			From: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			To:   time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
		},
		MaxPosts: 30,
	}
	return in, r.searchInput(in, 30)
}

// The actor takes searchQueries, not keywords. A `#tag` is a hashtag query and a
// bare word is a full-text query to it, so the terms pass through unchanged —
// stripping the `#` would turn a hashtag scrape into a keyword one.
func TestSearchInputTikTokUsesSearchQueries(t *testing.T) {
	in, got := tiktokInput()
	q, ok := got["searchQueries"].([]string)
	if !ok || len(q) != 2 || q[0] != "#jadibully" || q[1] != "kopi susu" {
		t.Errorf("searchQueries = %v, want the terms unchanged: %v", got["searchQueries"], in.Keywords)
	}
	if _, stale := got["keywords"]; stale {
		t.Error("this actor reads searchQueries; `keywords` is a different actor's spelling")
	}
	if _, stale := got["hashtags"]; stale {
		t.Error("hashtags[] is a different scrape mode; sending it alongside searchQueries changes what runs")
	}
}

// The one thing this actor does that the Instagram keyword actor does not: it
// takes a date range. Not sending it means paying for out-of-window posts and
// dropping them in the ingestor — the measured 18% yield on run d3fc79a8.
func TestSearchInputTikTokSendsBothDateBounds(t *testing.T) {
	_, got := tiktokInput()
	if v, want := got["oldestPostDateUnified"], "2026-08-01"; v != want {
		t.Errorf("oldestPostDateUnified = %v, want %q", v, want)
	}
	if v, want := got["newestPostDate"], "2026-09-24"; v != want {
		t.Errorf("newestPostDate = %v, want %q", v, want)
	}
}

// profileSorting is what gates the date filter: the actor honours
// oldestPostDateUnified/newestPostDate ONLY under latest|oldest. Set popular and
// the date fields are accepted and ignored — the run still succeeds and returns
// the newest-and-hottest videos across all time. This test fails the day anyone
// "optimises" the sort, which is the whole point.
func TestSearchInputTikTokSortPinsDateFilter(t *testing.T) {
	_, got := tiktokInput()
	if v := got["profileSorting"]; v != "latest" {
		t.Errorf("profileSorting = %v, want \"latest\" — the actor honours the date "+
			"bounds only under latest|oldest, so any other sort silently returns unfiltered results", v)
	}
}

// A zero window omits the date fields rather than sending 0001-01-01, which
// would read as a lower bound before the beginning of time.
func TestSearchInputTikTokOmitsZeroWindow(t *testing.T) {
	r := &Runner{}
	got := r.searchInput(port.ApifySearchInput{Platform: domain.PlatformTikTok, Keywords: []string{"x"}}, 10)
	for _, k := range []string{"oldestPostDateUnified", "newestPostDate"} {
		if v, ok := got[k]; ok {
			t.Errorf("zero window must omit %s, got %v", k, v)
		}
	}
}

// Pay-per-event: $1.70 per 1000 results, and every download flag is an EXTRA
// event while the AI summary fields bill PER VIDEO SECOND — orders of magnitude
// more than the per-result charge. They must be absent from the payload, not
// merely left at their defaults, so a future edit cannot flip one on.
func TestSearchInputTikTokOmitsChargeableAddons(t *testing.T) {
	_, got := tiktokInput()
	for _, k := range []string{
		"shouldDownloadVideos", "shouldDownloadMusic", "shouldDownloadSubtitles",
		"shouldDownloadCoverImages", "shouldDownloadProfilePhotos",
		"downloadSummarizedVideoDescription", "downloadAIVideoSummaries",
		"downloadSubtitlesOptions",
	} {
		if v, ok := got[k]; ok {
			t.Errorf("%q is a chargeable add-on and must not be sent, got %v", k, v)
		}
	}
}

// resultsPerPage is a PAGE SIZE, not a total cap: 500 requested at
// resultsPerPage 100 still bills 500 results. commentsPerPost is the field that
// would actually bill, and this integration stores no comment bodies — the
// actor parks them in a second dataset behind commentsDatasetURL, which we do
// not fetch.
func TestSearchInputTikTokDoesNotBillForComments(t *testing.T) {
	_, got := tiktokInput()
	if v, ok := got["resultsPerPage"]; !ok || v != 30 {
		t.Errorf("resultsPerPage = %v, want 30 (a page size — it does not cap the run's bill)", v)
	}
	if v := got["commentsPerPost"]; v != 0 {
		t.Errorf("commentsPerPost = %v, want 0 — comments are billed per comment and "+
			"this integration does not fetch the second dataset they arrive in", v)
	}
}
