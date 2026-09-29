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

// The TikTok payload is pinned against xmolodtsov/tiktok-search-scraper's input
// schema (verified 2026-09 against the live actor) for the same reason the
// others are: a wrong field name here fails SILENTLY. The actor accepts the
// payload, falls back to its own defaults, and returns a 200 with the wrong data.
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

// The actor reads `keywords`. Terms pass through unchanged: a `#tag` is a
// hashtag query and a bare word a full-text one, so stripping the `#` would
// turn a hashtag scrape into a keyword one.
func TestSearchInputTikTokUsesKeywords(t *testing.T) {
	in, got := tiktokInput()
	q, ok := got["keywords"].([]string)
	if !ok || len(q) != 2 || q[0] != "#jadibully" || q[1] != "kopi susu" {
		t.Errorf("keywords = %v, want the terms unchanged: %v", got["keywords"], in.Keywords)
	}
}

// maxItems is a real cap on rows returned, and this actor bills per result
// returned — so this is the whole cost ceiling of a run, not a page size like
// the previous actor's resultsPerPage. Dropping or renaming it does not fail
// the run, it just bills for the actor's own default instead of ours.
func TestSearchInputTikTokCapsTheBillWithMaxItems(t *testing.T) {
	_, got := tiktokInput()
	if v := got["maxItems"]; v != 30 {
		t.Errorf("maxItems = %v, want 30 — it caps rows returned, and rows returned are what we are billed for", v)
	}
}

// Every field here belongs to the actor this one replaced, and none of them did
// what this codebase believed they did. `oldestPostDateUnified`/`newestPostDate`
// were sent on the theory that the actor filtered server-side under
// profileSorting: latest; its input schema has no date field of any kind, so
// both were accepted and discarded. That belief is what a run's price was being
// read against — a 50-row page billed in full while 47 of the rows fell outside
// the window and were thrown away. They must not come back, and a re-added
// date field would cost money and do nothing, silently.
func TestSearchInputTikTokOmitsThePreviousActorsFields(t *testing.T) {
	_, got := tiktokInput()
	for _, k := range []string{
		"searchQueries", "profileSorting", "resultsPerPage", "commentsPerPost",
		"oldestPostDateUnified", "newestPostDate",
	} {
		if v, ok := got[k]; ok {
			t.Errorf("%q belongs to clockworks/tiktok-scraper and does nothing here (%v) — "+
				"this actor has no date input, so a date field would be billed-on-and-ignored, not a filter", k, v)
		}
	}
}

// The actor can harvest related suggest/hashtag/music rows. They are billed as
// results and they are not the posts this batch asked for, so the pools stay
// off — which is also the actor's own default.
func TestSearchInputTikTokOmitsExpansionPools(t *testing.T) {
	_, got := tiktokInput()
	if v, ok := got["expansion"]; ok {
		t.Errorf("expansion = %v, want it absent — the pools are billed per result and "+
			"return suggest/hashtag/music rows rather than the keyword's posts", v)
	}
}
