package apify

import (
	"encoding/json"
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
	// sasky's `limit` is a STRING. An int is silently ignored and the run falls
	// back to an enormous default, which is how a "max 50" batch ends up
	// scraping thousands.
	if ig["limit"] != "30" {
		t.Errorf("instagram limit = %#v, want the string \"30\"", ig["limit"])
	}
	// The previous actor's spellings. Sending them is not harmless: the actor
	// ignores unknown keys, so the run searches for nothing and returns an empty
	// dataset while still being billed.
	if _, ok := ig["maxPosts"]; ok {
		t.Error("the search actor takes no `maxPosts`; the cap is `limit` (a string)")
	}
	if _, ok := ig["search"]; ok {
		t.Error("instagram has no comma-joined `search` field; the actor ignores it")
	}
	if _, ok := ig["resultsLimit"]; ok {
		t.Error("instagram has no `resultsLimit` field")
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

// The four newer platforms are pinned against their live input schemas
// (verified 2026-09-29 by running each actor) for the reason the ones above
// are: a wrong field name is silent. Apify accepts the payload, the actor falls
// back to its own default, and the run returns 200 with whatever it wanted.
// Three of these four were caught only by running the actor — the top-priced
// actor per platform is not the right one for a keyword batch, and the store
// page never says so.

func searchInputFor(p domain.Platform, kw []string, w port.KeywordTimeWindow, n int) map[string]any {
	r := &Runner{}
	in := port.ApifySearchInput{Platform: p, Keywords: kw, Window: w, MaxPosts: n}
	return r.searchInput(in, n)
}

// fullWindow is anchored to time.Now, never to a literal date. youTubeUploadDateBucket
// compares time.Since(From) against bucket boundaries, so a fixed 2026-09-01
// silently aged the window past 32 days on 2026-10-03 and turned the YouTube
// uploadDate assertion into a permanent failure no code change could explain.
// A 28-day-old lower bound sits inside the month bucket and always will.
func fullWindow() port.KeywordTimeWindow {
	now := time.Now()
	return port.KeywordTimeWindow{
		From: now.Add(-28 * 24 * time.Hour),
		To:   now,
	}
}

func TestSearchInputYouTubePinsKeywordsMaxItemsAndUploadDate(t *testing.T) {
	got := searchInputFor(domain.PlatformYouTube, []string{"batulicin"}, fullWindow(), 10)
	if q, ok := got["keywords"].([]string); !ok || len(q) != 1 || q[0] != "batulicin" {
		t.Errorf("keywords = %v, want the terms unchanged", got["keywords"])
	}
	if got["maxItems"] != 10 {
		t.Errorf("maxItems = %v, want 10", got["maxItems"])
	}
	// uploadDate is an ENUM of durations, not a date. Sending the window's
	// start as a date string is rejected outright (400 invalid-input), and
	// sending a real date would be worse than useless if it were accepted.
	if got["uploadDate"] != "m" {
		t.Errorf("uploadDate = %v, want \"m\" — a 28-day-old lower bound maps to the month bucket", got["uploadDate"])
	}
}

func TestSearchInputLinkedInPinsSearchQueriesAndMaxPosts(t *testing.T) {
	got := searchInputFor(domain.PlatformLinkedIn, []string{"digital marketing"}, fullWindow(), 10)
	if q, ok := got["searchQueries"].([]string); !ok || len(q) != 1 {
		t.Errorf("searchQueries = %v, want the terms as an array — this actor spells it searchQueries, not keywords", got["searchQueries"])
	}
	// maxPosts, not maxItems. Sending maxItems to this actor is ignored and it
	// bills its own default of 100 posts per query: measured at 97 rows and
	// $0.096 for a batch that asked for 5.
	if got["maxPosts"] != 10 {
		t.Errorf("maxPosts = %v, want 10 — omitting it bills the actor's default of 100 per query", got["maxPosts"])
	}
	// Derived from the window, not a literal: postedLimitDate is a date and a
	// date does not age, but fullWindow() is anchored to time.Now so the YouTube
	// bucket stays inside its enum. Deriving the expectation keeps the two tests
	// from disagreeing about which day "now" is.
	want := fullWindow().From.UTC().Format("2006-01-02")
	if got["postedLimitDate"] != want {
		t.Errorf("postedLimitDate = %v, want %s — a real server-side lower bound", got["postedLimitDate"], want)
	}
}

func TestSearchInputXSendsNoCapTheActorDoesNotHave(t *testing.T) {
	got := searchInputFor(domain.PlatformX, []string{"batulicin"}, fullWindow(), 10)
	if q, ok := got["searchTerms"].([]string); !ok || len(q) != 1 {
		t.Errorf("searchTerms = %v, want the terms as an array", got["searchTerms"])
	}
	if got["sort"] != "Latest" {
		t.Errorf("sort = %v, want Latest", got["sort"])
	}
	// This actor has no result cap and no date input. Sending either is a key
	// it ignores, which reads as a working filter and is not one.
	for _, k := range []string{"maxItems", "maxResults", "startDate", "endDate", "since", "until"} {
		if v, ok := got[k]; ok {
			t.Errorf("%q = %v, but this actor has no such input — it is ignored silently", k, v)
		}
	}
}

// The actor's allowed uploadDate set is exactly {all, l, t, w, m, y}. This is
// pinned because an out-of-set value is not a near-miss: Apify rejects the
// whole run with a 400, so a bucket that "looks close enough" costs the entire
// batch. "h" was written first and would have 400'd every window under two
// hours — the sub-daily bucket is "l".
func TestYouTubeUploadDateBucketStaysInsideTheAllowedEnum(t *testing.T) {
	allowed := map[string]bool{"all": true, "l": true, "t": true, "w": true, "m": true, "y": true}
	now := time.Now()
	for _, age := range []time.Duration{
		0, time.Minute, 90 * time.Minute, 2 * time.Hour, 36 * time.Hour,
		3 * 24 * time.Hour, 7 * 24 * time.Hour, 20 * 24 * time.Hour,
		29 * 24 * time.Hour, 200 * 24 * time.Hour, 900 * 24 * time.Hour,
	} {
		got := youTubeUploadDateBucket(port.KeywordTimeWindow{From: now.Add(-age)})
		if !allowed[got] {
			t.Errorf("window %s old -> uploadDate %q, which the actor rejects (allowed: all, l, t, w, m, y)", age, got)
		}
	}
	// And an unbounded window must ask for everything, not a bucket.
	if got := youTubeUploadDateBucket(port.KeywordTimeWindow{}); got != "all" {
		t.Errorf("open window -> uploadDate %q, want all", got)
	}
}

func TestSearchInputFacebookSendsOneQueryAndForcesPostType(t *testing.T) {
	got := searchInputFor(domain.PlatformFacebook, []string{"batulicin", "kotabaru"}, fullWindow(), 10)
	// This actor takes ONE string, not an array, and its default search_type is
	// not posts — it also searches groups, people and events.
	if got["query"] != "batulicin kotabaru" {
		t.Errorf("query = %v, want the terms joined into one string", got["query"])
	}
	if got["search_type"] != "posts" {
		t.Errorf("search_type = %v, want posts", got["search_type"])
	}
	if got["max_posts"] != 10 {
		t.Errorf("max_posts = %v, want 10", got["max_posts"])
	}
}

// A platform with no branch returns nil rather than falling through to the
// Instagram payload. The old if-chain sent `keywords`/`maxPosts` to every
// platform it did not know, so a newly added platform was billed for a search
// the actor ignored and answered with its own default results.
func TestSearchInputUnknownPlatformYieldsNoPayload(t *testing.T) {
	if got := searchInputFor(domain.Platform("mastodon"), []string{"x"}, fullWindow(), 10); got != nil {
		t.Errorf("unknown platform payload = %v, want nil — an unlisted platform must not borrow another actor's schema", got)
	}
}

// The Instagram search actor returns post URLs only, so the runner enriches
// each one with the post actor. These cases pin the URL extraction that decides
// what gets paid for.
func TestSearchPostURLs(t *testing.T) {
	rows := []json.RawMessage{
		json.RawMessage(`{"keyword":"a","post_url":"https://www.instagram.com/p/AAA/"}`),
		// Same post under a second keyword: paying twice is the waste.
		json.RawMessage(`{"keyword":"b","post_url":"https://www.instagram.com/p/AAA/"}`),
		// Query string must not make it a different post.
		json.RawMessage(`{"keyword":"c","post_url":"https://www.instagram.com/p/BBB/?img_index=1"}`),
		// The page-metadata row the actor emits; not an error, just skipped.
		json.RawMessage(`{"keyword":"d","post_url":""}`),
		json.RawMessage(`{"keyword":"e"}`),
		// A non-object row must not take the batch down.
		json.RawMessage(`"nonsense"`),
	}
	got := searchPostURLs(rows)
	want := []string{
		"https://www.instagram.com/p/AAA/",
		"https://www.instagram.com/p/BBB/", // query stripped
	}
	if len(got) != len(want) {
		t.Fatalf("urls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("urls[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// An empty result set is not an error: a keyword with no posts is a real
// answer, and the caller must not run the post actor for it.
func TestSearchPostURLsEmpty(t *testing.T) {
	// nil input → nothing to pay for. The caller tests len(), so an empty
	// slice would also be correct; asserting emptiness keeps the contract at
	// the level the caller relies on.
	if got := searchPostURLs(nil); len(got) != 0 {
		t.Fatalf("urls = %v, want empty", got)
	}
	if got := searchPostURLs([]json.RawMessage{json.RawMessage(`{"post_url":""}`)}); len(got) != 0 {
		t.Fatalf("urls = %v, want empty", got)
	}
}

// Apify puts the actor's own reason for a failed run in statusMessage. Dropping
// it is what made a geo-block indistinguishable from a crash in the batch error
// (it read only "actor FAILED: search actor run: FAILED"), so the reason must
// reach the operator. A run that fails without one still reads cleanly.
func TestFailedRunErrorCarriesTheActorsReason(t *testing.T) {
	cases := []struct{ label, status, msg, want string }{
		{"search actor", "FAILED", "geo: All 1 keywords returned zero results — blocked or errored — no valid response from any exit.",
			"search actor run FAILED: geo: All 1 keywords returned zero results — blocked or errored — no valid response from any exit."},
		{"search actor", "TIMED-OUT", "", "search actor run: TIMED-OUT"},
		{"threads actor", "ABORTED", "   ", "threads actor run: ABORTED"},
	}
	for _, c := range cases {
		if got := failedRunError(c.label, c.status, c.msg); got != c.want {
			t.Errorf("failedRunError(%q,%q,%q) = %q, want %q", c.label, c.status, c.msg, got, c.want)
		}
	}
}

// A per-post run input is the same silent-wrong-answer shape as the search
// input: a key the actor does not have is ignored and the actor returns its
// own default (usually a profile's recent posts), which the ingestor then
// discards — after the run was paid for. These assertions pin each actor's
// own spelling against its live schema.
func TestRunInputPerPlatform(t *testing.T) {
	const url = "https://example.com/post/1"

	ig := runInput(domain.PlatformInstagram, url, 10)
	if _, ok := ig["username"]; !ok {
		t.Error("instagram: expected `username` (the post actor reads the permalink from it)")
	}
	if _, ok := ig["startUrls"]; ok {
		t.Error("instagram: `startUrls` was removed from this actor's schema; sending it is ignored")
	}

	tt := runInput(domain.PlatformTikTok, url, 10)
	urls, ok := tt["url"].([]string)
	if !ok || len(urls) != 1 || urls[0] != url {
		t.Errorf("tiktok: expected url:[%q], got %#v (the actor's field is `url`, an ARRAY of strings)", url, tt)
	}
	if _, ok := tt["urls"]; ok {
		t.Error("tiktok: `urls` is the WRONG field — thenetaji's actor takes it but requires minItems 4, " +
			"which rejects a single-post scrape before the run starts")
	}

	for _, p := range []domain.Platform{domain.PlatformYouTube, domain.PlatformX} {
		in := runInput(p, url, 3)
		start, ok := in["startUrls"].([]map[string]string)
		if !ok || len(start) != 1 || start[0]["url"] != url {
			t.Errorf("%s: expected startUrls:[{url:%q}], got %#v", p, url, in)
		}
		if in["maxItems"] != 3 {
			t.Errorf("%s: maxItems = %v, want 3 (both actors honour it as a real cap)", p, in["maxItems"])
		}
	}
}
