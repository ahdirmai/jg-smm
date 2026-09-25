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
	if got, want := ig["search"], "batulicin,kalsel"; got != want {
		t.Errorf("instagram search = %v, want comma-joined %q", got, want)
	}
	if _, isSlice := ig["search"].([]string); isSlice {
		t.Error("instagram search must be a comma-joined string, not an array")
	}
	if _, ok := ig["searchTerms"]; ok {
		t.Error("instagram has no searchTerms field; the actor ignores it and uses its default")
	}
	if ig["searchType"] != "hashtag" || ig["resultsType"] != "posts" {
		t.Errorf("instagram must ask for hashtag posts, got %v/%v", ig["searchType"], ig["resultsType"])
	}
	if ig["onlyPostsNewerThan"] != "2026-09-01" {
		t.Errorf("instagram from-bound: got %v", ig["onlyPostsNewerThan"])
	}
	if _, ok := ig["end_date"]; ok {
		t.Error("instagram has no upper date bound; the ingestor applies it")
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
// which an actor would read as 0001-01-01.
func TestSearchInputOmitsZeroWindow(t *testing.T) {
	r := &Runner{}
	in := port.ApifySearchInput{Platform: domain.PlatformInstagram, Keywords: []string{"x"}}

	ig := r.searchInput(in, 10)
	if v, ok := ig["onlyPostsNewerThan"]; ok {
		t.Errorf("zero window must omit onlyPostsNewerThan, got %v", v)
	}

	in.Platform = domain.PlatformThreads
	th := r.searchInput(in, 10)
	for _, k := range []string{"start_date", "end_date"} {
		if v, ok := th[k]; ok {
			t.Errorf("zero window must omit %s, got %v", k, v)
		}
	}
}
