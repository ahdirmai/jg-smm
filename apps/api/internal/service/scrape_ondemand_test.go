package service

import (
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

func TestPostExternalIDFromURL(t *testing.T) {
	cases := []struct {
		p    domain.Platform
		url  string
		want string
	}{
		{domain.PlatformInstagram, "https://www.instagram.com/p/DdaJ8Y8gp3u/?img_index=1", "DdaJ8Y8gp3u"},
		{domain.PlatformInstagram, "https://www.instagram.com/p/DdaJ8Y8gp3u/", "DdaJ8Y8gp3u"},
		{domain.PlatformInstagram, "https://www.instagram.com/reel/ABC123/", "ABC123"},
		{domain.PlatformInstagram, "https://instagram.com/tv/XYZ/#frag", "XYZ"},
		{domain.PlatformInstagram, "https://www.instagram.com/someuser/", ""},
		{domain.PlatformThreads, "https://www.threads.com/@user/post/Czzz99", "Czzz99"},
		{domain.PlatformThreads, "https://www.threads.net/@user/post/Czzz99?x=1", "Czzz99"},
	}
	for _, c := range cases {
		if got := postExternalIDFromURL(c.p, c.url); got != c.want {
			t.Errorf("postExternalIDFromURL(%s, %q) = %q, want %q", c.p, c.url, got, c.want)
		}
	}
}

// A permalink scrape accepts only the post the operator asked for. The Threads
// actor's post mode returns the share/quote CONTEXT rows around the requested
// post — auto-generated "Dengan <name> – Saya baru saja diakui…" text,
// is_reply:false, no parent field — with nothing in the row that separates
// them from the requested post. Without this filter they land in the post
// table as ordinary posts. A run with no code (an empty onlyCode) keeps
// everything, which is the keyword path.
func TestPermalinkScrapeDropsContextRows(t *testing.T) {
	// Copied from the actor's real post-mode output for one permalink:
	// the requested post, then four share/quote context rows.
	rows := []string{
		`{"code":"DdGJIzFiWkJ","username":"ahdirmai","text":"Ridha Fahmi Junaidi","posted_at":"2026-09-10T10:00:00+00:00","like_count":1,"reply_count":2,"repost_count":0,"quote_count":0,"media_urls":[],"is_reply":false}`,
		`{"code":"DdG-XPPo1j5","username":"nandomrp13","text":"Dengan Ridha Ramdani – Saya baru saja diakui sebagai salah satu penggemar beratnya! 🎉","posted_at":"2026-09-10T13:45:30+00:00","like_count":0,"reply_count":0,"repost_count":0,"quote_count":0,"media_urls":[],"is_reply":false}`,
		`{"code":"DdGvEZeCQ3q","username":"nenitirtapratama","text":"Dengan Riri Fajriah – Saya baru saja diakui sebagai salah satu penggemar beratnya! 🎉","posted_at":"2026-09-10T11:31:52+00:00","like_count":0,"reply_count":0,"repost_count":0,"quote_count":0,"media_urls":[],"is_reply":false}`,
		`{"code":"DdIEE-1E1Kn","username":"pecakarlangit2","text":"Dengan Riri Fajriah – Saya baru saja diakui sebagai salah satu penggemar beratnya! 🎉","posted_at":"2026-09-10T23:54:41+00:00","like_count":0,"reply_count":0,"repost_count":0,"quote_count":0,"media_urls":[],"is_reply":false}`,
	}
	window := port.KeywordTimeWindow{To: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}

	t.Run("only the requested post is kept", func(t *testing.T) {
		run := newThreadsRun()
		kept := 0
		for _, row := range rows {
			items, err := parseItems(domain.PlatformThreads, []byte(row), window, "DdGJIzFiWkJ", run)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			kept += len(items)
		}
		if kept != 1 {
			t.Fatalf("want only the requested post kept, got %d items", kept)
		}
	})

	t.Run("no code filter keeps them all", func(t *testing.T) {
		run := newThreadsRun()
		kept := 0
		for _, row := range rows {
			items, err := parseItems(domain.PlatformThreads, []byte(row), window, "", run)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			kept += len(items)
		}
		if kept != len(rows) {
			t.Fatalf("want all %d kept with no onlyCode, got %d", len(rows), kept)
		}
	})
}
