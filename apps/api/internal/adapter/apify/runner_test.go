package apify

import "testing"

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
