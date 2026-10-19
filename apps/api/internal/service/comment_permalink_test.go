package service

import (
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// The permalink we hand the dashboard must be one Enqueue will accept. The
// shape is platform-specific: IG `/p/<post>/c/<id>/`, Threads `/post/<comment
// code>` (Threads has no `/c/` route — it 404s). Asserting the round trip
// rather than the literal string alone keeps this honest: a derivation that
// produced a URL Enqueue rejects would look right in the UI and then fail on
// submit — the exact "the button did nothing" bug this field prevents.
func TestCommentPermalinkSurvivesEnqueueValidation(t *testing.T) {
	cases := []struct {
		name      string
		platform  domain.Platform
		postCode  string
		commentID string
		wantURL   string
	}{
		{
			name:      "instagram",
			platform:  domain.PlatformInstagram,
			postCode:  "DdpmBpCEzpX",
			commentID: "18130064662817820",
			wantURL:   "https://www.instagram.com/p/DdpmBpCEzpX/c/18130064662817820/",
		},
		{
			// A Threads reply is itself a post: its own code is the address.
			name:      "threads",
			platform:  domain.PlatformThreads,
			postCode:  "Czzz99",
			commentID: "Cyyy11",
			wantURL:   "https://www.threads.net/post/Cyyy11",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := commentPermalink(c.platform, c.postCode, c.commentID)
			if got != c.wantURL {
				t.Fatalf("commentPermalink = %q, want %q", got, c.wantURL)
			}
			if id := replyTargetID(c.platform, got); id != c.commentID {
				t.Errorf("replyTargetID(%q) = %q, want %q — Enqueue would reject this target", got, id, c.commentID)
			}
		})
	}
}

// A comment we cannot address must yield an empty URL, never a half-built one:
// the UI gates the Reply button on it, so a malformed URL here would surface as
// a reply button that always fails validation server-side.
func TestCommentPermalinkEmptyWithoutBothIDs(t *testing.T) {
	cases := []struct {
		name      string
		platform  domain.Platform
		postCode  string
		commentID string
	}{
		{"no post code", domain.PlatformInstagram, "", "123"},
		{"no comment id", domain.PlatformInstagram, "DdpmBpCEzpX", ""},
		{"blank post code", domain.PlatformInstagram, "   ", "123"},
		{"unsupported platform", domain.PlatformTikTok, "DdpmBpCEzpX", "123"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := commentPermalink(c.platform, c.postCode, c.commentID); got != "" {
				t.Errorf("commentPermalink = %q, want empty (no addressable target)", got)
			}
		})
	}
}

// One post code must address every comment on that post — a comment permalink
// whose post segment drifted per row would send replies to the wrong thread.
func TestWithCommentPermalinksUsesThePostCodeForEveryComment(t *testing.T) {
	comments := []domain.Comment{
		{ExternalID: "111", AuthorHandle: "a"},
		{ExternalID: "222", AuthorHandle: "b"},
	}
	got := withCommentPermalinks(domain.PlatformInstagram, "DdpmBpCEzpX", comments)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for i, want := range []string{
		"https://www.instagram.com/p/DdpmBpCEzpX/c/111/",
		"https://www.instagram.com/p/DdpmBpCEzpX/c/222/",
	} {
		if got[i].URL != want {
			t.Errorf("comment %d URL = %q, want %q", i, got[i].URL, want)
		}
	}
}
