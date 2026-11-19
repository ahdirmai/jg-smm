package service

import (
	"net/url"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

func TestCanonicalScrapeURLRewritesYouTubeShareLinks(t *testing.T) {
	cases := []struct {
		raw, want string
	}{
		{"https://youtu.be/CWh1yOIeL_I", "https://www.youtube.com/watch?v=CWh1yOIeL_I"},
		{"https://youtu.be/CWh1yOIeL_I?t=30", "https://www.youtube.com/watch?v=CWh1yOIeL_I"},
		{"https://www.youtube.com/watch?v=CWh1yOIeL_I", "https://www.youtube.com/watch?v=CWh1yOIeL_I"},
		// Share/tracking query is what made the actor return ten null rows.
		{"https://youtube.com/shorts/RyNumF4OEUk?feature=share", "https://www.youtube.com/shorts/RyNumF4OEUk"},
		{"https://www.youtube.com/shorts/RyNumF4OEUk?si=abc", "https://www.youtube.com/shorts/RyNumF4OEUk"},
		{"https://youtube.com/watch?v=CWh1yOIeL_I&feature=share", "https://www.youtube.com/watch?v=CWh1yOIeL_I"},
	}
	for _, c := range cases {
		u, err := url.Parse(c.raw)
		if err != nil {
			t.Fatalf("parse %q: %v", c.raw, err)
		}
		if got := canonicalScrapeURL(domain.PlatformYouTube, u).String(); got != c.want {
			t.Errorf("canonicalScrapeURL(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
	// Another platform's youtu.be-looking URL must not be rewritten.
	u, _ := url.Parse("https://youtu.be/abc")
	if got := canonicalScrapeURL(domain.PlatformInstagram, u).String(); got != "https://youtu.be/abc" {
		t.Errorf("non-YouTube platform rewritten: %q", got)
	}
}
