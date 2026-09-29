package service

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// realYouTubeRows is the captured dataset from a live apidojo/youtube-scraper-api
// run (Apify id gXSReGYeawn5nwDhI; keyword "batulicin", 2026-09-29). The rows are
// stored verbatim — every key, every nesting level and every value is exactly what
// the actor returned — with only two things removed:
//
//   - `streamingData` and `captions`, whose only content is short-lived signed
//     URLs (streamingData.expiresInSeconds was 21540, ~6 hours). Those blobs are
//     ~150 KB of signatures per row and the parser must never read them, so
//     keeping them would be a fixture that tests nothing while drowning the file
//     it lives in. Their ABSENCE is asserted below, so "we ignore them" is a
//     tested claim and not a hope.
//   - nothing else. The thumbnails, the channel object (including the
//     "21.2K subscribers" string), the three twin date fields, description,
//     keywords, superTitle and every boolean are exactly as received.
//
// It matches /tmp/real_rows.json key `youtube` verbatim, modulo those two blobs.
// A hand-built fixture would have flattened exactly the facts under test — that
// thumbnails is an array ordered smallest-first, that the handle carries its own
// "@", that the date has a -07:00 offset, that there is no shares/bookmarks key —
// and a test written against an invented row asserts the invention.
const realYouTubeRows = "testdata/youtube_rows.json"

// The captured rows' load-bearing values, asserted against the fixture itself in
// ytRows()
// so a stale fixture fails loudly instead of making the parser tests vacuous.
const (
	// Row 0: the video the parser's main test is written against.
	ytID        = "k_ldBC5mQu8"
	ytViews     = 14380
	ytLikes     = 241
	ytComments  = 46
	ytChannelID = "UCIs8UcIpEvcX2IgvqTiii4Q"
	ytHandle    = "@K7_Bakujuk"

	// The largest thumbnail's URL stem, row 0. 1920x1080, and it is the .webp —
	// chosen over the 1280x720 .jpg beside it because area, not index or width,
	// is the rule.
	ytLargestThumb = "maxresdefault.webp"

	// ytPublishedUTC is row 0's uploadDate ("2026-09-13T01:25:08-07:00")
	// resolved to UTC. The offset is REAL: 01:25 -07:00 is 08:25Z, and a parser
	// that dropped the offset would store a date eight hours off.
	ytPublishedUTC = "2026-09-13T08:25:08Z"
)

// ytRow loads the captured rows and pins the fields the tests below assert. The
// fixture is the ground truth, so a parse failure here is a fixture problem, not
// a parser one — and a fixture that lost a key would otherwise let a broken
// parser pass by asserting nothing.
func ytRow(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(realYouTubeRows)
	if err != nil {
		t.Fatalf("read captured rows: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("captured rows are not valid json: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("captured rows are empty")
	}
	first := rows[0]
	for _, k := range []string{
		"id", "title", "url", "uploadDate", "publishDate", "relativeDate",
		"views", "likes", "comments", "channel", "thumbnails", "status", "type",
	} {
		if _, ok := first[k]; !ok {
			t.Fatalf("captured row lost key %q — the fixture no longer resembles the actor's output", k)
		}
	}
	// The two blobs are removed on purpose (see the const's comment). Asserting
	// their absence keeps the removal deliberate: this parser must never read a
	// signed, expiring URL, and a fixture that reintroduced them would make the
	// "we ignore streamingData" claim untestable.
	for _, k := range []string{"streamingData", "captions"} {
		if _, ok := first[k]; ok {
			t.Fatalf("captured row carries %q — it was removed on purpose; the parser must not read it", k)
		}
	}
	if first["id"] != ytID {
		t.Fatalf("fixture row 0 id = %v, want %s — this fixture is not the one these tests were written against", first["id"], ytID)
	}
	one, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("re-marshal captured row: %v", err)
	}
	return one
}

// ytFixture decodes row 0 into a map so a test can edit one field while keeping
// every other nesting level exactly as the actor sent it.
func ytFixture(t *testing.T) map[string]any {
	t.Helper()
	var row map[string]any
	if err := json.Unmarshal(ytRow(t), &row); err != nil {
		t.Fatalf("decode captured row: %v", err)
	}
	return row
}

// ytRowsFrom parses an in-memory row (the fixture, edited) — for the cases that
// need a field the captured row does not have.
func ytRowsFrom(t *testing.T, row map[string]any, w port.KeywordTimeWindow) ([]scrapeItem, error) {
	t.Helper()
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal row: %v", err)
	}
	return parseYouTubeItems(raw, w)
}

// ytWindow brackets row 0 (2026-09-13) on both sides.
func ytWindow() port.KeywordTimeWindow {
	return port.KeywordTimeWindow{
		From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC),
	}
}

// ytWindowFarAfter places row 0 before the window, for the drop test.
func ytWindowFarAfter() port.KeywordTimeWindow {
	return port.KeywordTimeWindow{
		From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC),
	}
}

// The captured row is the ground truth this parser is written against: the
// nesting levels, the handle's "@", the thumbnail choice and the metric mapping
// are all asserted here. If the actor renames one or nests one differently, this
// fails instead of storing an empty field that reads as "this channel has no
// name" on the dashboard.
func TestParseYouTubeRealActorRow(t *testing.T) {
	items, err := parseYouTubeItems(ytRow(t), ytWindow())
	if err != nil {
		t.Fatalf("parse captured row: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	it := items[0]
	if it.Platform != "youtube" {
		t.Errorf("platform = %q, want youtube", it.Platform)
	}
	// The video id, not a URN and not the channel id.
	if it.ExternalID != ytID {
		t.Errorf("externalID = %q, want %s", it.ExternalID, ytID)
	}
	// channel is a nested object: the handle is one level down and already
	// carries its "@" — it must not be re-prefixed or trimmed.
	if it.AuthorHandle != ytHandle {
		t.Errorf("authorHandle = %q, want %q (channel.handle)", it.AuthorHandle, ytHandle)
	}
	if it.AuthorID != ytChannelID {
		t.Errorf("authorID = %q, want %q (channel.id)", it.AuthorID, ytChannelID)
	}
	// `title` is the post text, NOT `description`.
	if it.Text != "🔥UPDATE TERBARU JEMBATAN PULAU LAUT BATULICIN KOTABARU KALIMANTAN SELATAN" {
		t.Errorf("text = %q, want the video title", truncateForLog(it.Text))
	}
	if strings.Contains(it.Text, "proyek strategis nasional") {
		t.Error("text carries `description` — title is the post text on this platform")
	}
	// thumbnails is an array; the LARGEST by area wins, and on this row that is
	// the 1920x1080 webp rather than the first entry or the widest single number.
	if len(it.MediaURLs) != 1 {
		t.Fatalf("mediaUrls = %v, want exactly one entry (the largest thumbnail)", it.MediaURLs)
	}
	if !strings.Contains(it.MediaURLs[0], ytLargestThumb) {
		t.Errorf("mediaUrls[0] = %q, want the largest variant (%s)", it.MediaURLs[0], ytLargestThumb)
	}
	if strings.Contains(it.MediaURLs[0], "hqdefault") {
		t.Error("mediaUrls[0] is a small hqdefault variant — the array is smallest-first, so index 0 is the speck")
	}
	// uploadDate is ISO WITH an offset; the stored instant is UTC.
	want, err := time.Parse(time.RFC3339, ytPublishedUTC)
	if err != nil {
		t.Fatalf("bad expected instant: %v", err)
	}
	if it.PublishedAt == nil || !it.PublishedAt.Equal(want) {
		t.Fatalf("publishedAt = %v, want %s (uploadDate resolved from -07:00)", it.PublishedAt, ytPublishedUTC)
	}
	// The offset must actually have been applied: 01:25 local is NOT 01:25Z.
	if it.PublishedAt.Hour() != 8 {
		t.Errorf("publishedAt hour = %d, want 8 — the -07:00 offset was dropped, not resolved", it.PublishedAt.Hour())
	}
	checkMetric(t, mustJSON(t, it.Metrics), "views", ytViews)
	checkMetric(t, mustJSON(t, it.Metrics), "likes", ytLikes)
	checkMetric(t, mustJSON(t, it.Metrics), "comments", ytComments)
}

// Every row in the captured dataset must parse. Row 1 is a second real video
// from the same run; a parser that works only on the row its main test happens
// to use is a parser that breaks on the next variant.
func TestParseYouTubeEveryCapturedRowParses(t *testing.T) {
	raw, err := os.ReadFile(realYouTubeRows)
	if err != nil {
		t.Fatalf("read captured rows: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decode captured rows: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("captured rows = %d, want the two live rows", len(rows))
	}
	for i, row := range rows {
		body, err := json.Marshal(row)
		if err != nil {
			t.Fatalf("row %d: marshal: %v", i, err)
		}
		items, err := parseYouTubeItems(body, ytWindow())
		if err != nil {
			t.Fatalf("row %d: parse: %v", i, err)
		}
		if len(items) != 1 {
			t.Fatalf("row %d: items = %d, want 1", i, len(items))
		}
		if items[0].ExternalID == "" {
			t.Fatalf("row %d: empty external id", i)
		}
		if items[0].PublishedAt == nil {
			t.Fatalf("row %d: publishedAt = nil, want the row's uploadDate", i)
		}
		if len(items[0].MediaURLs) != 1 {
			t.Fatalf("row %d: mediaUrls = %v, want one largest thumbnail", i, items[0].MediaURLs)
		}
	}
}

// This actor DOES accept a server-side uploadDate filter, but it is relative and
// coarse ("this month") — a different predicate from an absolute from/to — so
// the ingestor's window still has to drop what the actor over-returns. An
// out-of-window post must be dropped, and dropping one must not fail the run.
func TestParseYouTubeFiltersByWindow(t *testing.T) {
	items, err := parseYouTubeItems(ytRow(t), ytWindow())
	if err != nil || len(items) != 1 {
		t.Fatalf("in-window row: items=%d err=%v, want 1 and no error", len(items), err)
	}
	items, err = parseYouTubeItems(ytRow(t), ytWindowFarAfter())
	if err != nil {
		t.Fatalf("out-of-window row must not error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %d, want 0 for a post before the window", len(items))
	}
}

// uploadDate, publishDate and relativeDate hold the SAME instant on every
// captured row, so this fallback only becomes observable when uploadDate is
// missing — the case a parser that reads uploadDate alone turns into an undated
// post while the date sits in the field right beside it.
func TestParseYouTubeFallsBackToPublishDateThenRelativeDate(t *testing.T) {
	for _, field := range []string{"publishDate", "relativeDate"} {
		row := ytFixture(t)
		date, ok := row["uploadDate"].(string)
		if !ok {
			t.Fatal("fixture uploadDate is not a string")
		}
		// Clear EVERY date field, then set only the one under test. Deleting
		// just uploadDate leaves publishDate holding the captured value, so a
		// relativeDate case would still be answered by publishDate and would
		// pass without ever exercising the fallback it claims to test.
		for _, f := range []string{"uploadDate", "publishDate", "relativeDate"} {
			delete(row, f)
		}
		// A DIFFERENT in-window instant from the one uploadDate held, so
		// reading the wrong field cannot accidentally produce the wanted value.
		row[field] = "2026-09-15T02:00:00-07:00"
		items, err := ytRowsFrom(t, row, ytWindow())
		if err != nil {
			t.Fatalf("%s: parse: %v", field, err)
		}
		if len(items) != 1 {
			t.Fatalf("%s: items = %d, want 1", field, len(items))
		}
		want, err := time.Parse(time.RFC3339, "2026-09-15T09:00:00Z")
		if err != nil {
			t.Fatalf("bad expected instant: %v", err)
		}
		if items[0].PublishedAt == nil || !items[0].PublishedAt.Equal(want) {
			t.Fatalf("%s: publishedAt = %v, want %s (fallback from uploadDate=%s)",
				field, items[0].PublishedAt, want.Format(time.RFC3339), date)
		}
	}
}

// uploadDate wins when the fields disagree: it is the instant the actor's own
// naming designates, and an earlier fallback must not override it.
func TestParseYouTubePrefersUploadDate(t *testing.T) {
	row := ytFixture(t)
	row["uploadDate"] = "2026-09-13T01:25:08-07:00"
	row["publishDate"] = "2026-09-20T02:00:00-07:00"
	row["relativeDate"] = "2026-09-21T02:00:00-07:00"
	items, err := ytRowsFrom(t, row, ytWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want, err := time.Parse(time.RFC3339, ytPublishedUTC)
	if err != nil {
		t.Fatalf("bad expected instant: %v", err)
	}
	if items[0].PublishedAt == nil || !items[0].PublishedAt.Equal(want) {
		t.Fatalf("publishedAt = %v, want %s (uploadDate)", items[0].PublishedAt, ytPublishedUTC)
	}
}

// A row with no readable date on any of the three fields is KEPT — a missing
// field is the actor's gap, not a date outside the window — and must not be
// given an invented one.
func TestParseYouTubeUndatedRowIsKeptWithNoPublishDate(t *testing.T) {
	row := ytFixture(t)
	row["uploadDate"] = ""
	row["publishDate"] = ""
	row["relativeDate"] = ""
	items, err := ytRowsFrom(t, row, ytWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want the undated row kept", len(items))
	}
	if items[0].PublishedAt != nil {
		t.Fatalf("publishedAt = %v, want nil — no date was supplied, so none may be stored", items[0].PublishedAt)
	}
}

// An unreadable date string is a different path from an empty one: the post must
// still be kept, and the date stored as NULL rather than as the zero time.
func TestParseYouTubeUnparseableDateIsKeptWithNoPublishDate(t *testing.T) {
	row := ytFixture(t)
	row["uploadDate"] = "13/09/2026 01:25:08"
	row["publishDate"] = ""
	row["relativeDate"] = ""
	items, err := ytRowsFrom(t, row, ytWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want the post kept", len(items))
	}
	if items[0].PublishedAt != nil {
		t.Fatalf("publishedAt = %v, want nil — an unparseable date must never be stored as something", items[0].PublishedAt)
	}
}

// The largest variant is chosen by AREA, not by index and not by width. The
// captured array is ordered smallest-first, so index -1 happens to be correct
// today; these cases pin the RULE so a reordered array still yields the biggest
// image instead of a 168x94 speck.
func TestParseYouTubePicksLargestThumbnailByArea(t *testing.T) {
	cases := []struct {
		name   string
		thumbs []map[string]any
		want   string
	}{
		{
			name: "reordered array, not the last entry",
			thumbs: []map[string]any{
				{"url": "https://i.ytimg.com/big.jpg", "width": 1280, "height": 720},
				{"url": "https://i.ytimg.com/small.jpg", "width": 168, "height": 94},
			},
			want: "https://i.ytimg.com/big.jpg",
		},
		{
			name: "taller portrait Short beats a wider landscape frame",
			thumbs: []map[string]any{
				{"url": "https://i.ytimg.com/landscape.jpg", "width": 1280, "height": 720},
				{"url": "https://i.ytimg.com/portrait.jpg", "width": 1080, "height": 1920},
			},
			want: "https://i.ytimg.com/portrait.jpg",
		},
		{
			name: "entry with no url is skipped",
			thumbs: []map[string]any{
				{"width": 1920, "height": 1080},
				{"url": "https://i.ytimg.com/real.jpg", "width": 336, "height": 188},
			},
			want: "https://i.ytimg.com/real.jpg",
		},
		{
			name: "equal areas keep the first",
			thumbs: []map[string]any{
				{"url": "https://i.ytimg.com/first.jpg", "width": 640, "height": 480},
				{"url": "https://i.ytimg.com/second.jpg", "width": 480, "height": 640},
			},
			want: "https://i.ytimg.com/first.jpg",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := ytFixture(t)
			row["thumbnails"] = tc.thumbs
			items, err := ytRowsFrom(t, row, ytWindow())
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(items) != 1 {
				t.Fatalf("items = %d, want 1", len(items))
			}
			if len(items[0].MediaURLs) != 1 || items[0].MediaURLs[0] != tc.want {
				t.Fatalf("mediaUrls = %v, want exactly [%s]", items[0].MediaURLs, tc.want)
			}
		})
	}
}

// thumbnails: [] is a real possibility for a private or not-yet-processed video.
// The post must still be stored — with no media, which the page renders as no
// thumbnail — rather than dropped over a missing optional array. And the media
// list must be EMPTY, not nil: it is stored and served as a JSON array, where
// null and [] are different things to every reader downstream.
func TestParseYouTubeSurvivesEmptyOrMissingThumbnails(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(map[string]any)
	}{
		{"empty array", func(r map[string]any) { r["thumbnails"] = []any{} }},
		{"missing key", func(r map[string]any) { delete(r, "thumbnails") }},
		{"entries with no usable url", func(r map[string]any) {
			r["thumbnails"] = []any{map[string]any{"width": 1920, "height": 1080}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := ytFixture(t)
			tc.apply(row)
			items, err := ytRowsFrom(t, row, ytWindow())
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(items) != 1 {
				t.Fatalf("items = %d, want 1: a video with no thumbnail is still a post", len(items))
			}
			if items[0].MediaURLs == nil {
				t.Fatal("mediaUrls = nil, want an empty non-nil list")
			}
			if len(items[0].MediaURLs) != 0 {
				t.Fatalf("mediaUrls = %v, want empty", items[0].MediaURLs)
			}
		})
	}
}

// A row with no id is malformed and must say so rather than storing a post under
// an empty key. Silently dropping it would hide a broken actor behind a
// plausible-looking count.
func TestParseYouTubeRowWithNoIDIsAnError(t *testing.T) {
	if _, err := ytRowsFrom(t, map[string]any{"title": "orphan", "views": 1}, ytWindow()); err == nil {
		t.Fatal("a row with no id is malformed and must error, not be dropped silently")
	}
}

// An error row is an error, so the caller logs it and moves on — one broken row
// must not stop the well-formed ones beside it.
func TestParseYouTubeErrorRowIsAnErrorNotAPost(t *testing.T) {
	if _, err := ytRowsFrom(t, map[string]any{"error": "no_items"}, ytWindow()); err == nil {
		t.Fatal("an error row must error rather than ingest as a post")
	}
}

// A row whose status is not OK did not resolve to a playable video. It carries a
// real id, so without this check it would land in the post table as junk, and a
// silent skip is right: it is not a broken row, it is a row we do not want.
func TestParseYouTubeNonOKStatusYieldsNoItems(t *testing.T) {
	for _, status := range []string{"ERROR", "UNAVAILABLE", "PRIVATE"} {
		row := ytFixture(t)
		row["status"] = status
		items, err := ytRowsFrom(t, row, ytWindow())
		if err != nil {
			t.Fatalf("status %q: err = %v, want a silent skip", status, err)
		}
		if len(items) != 0 {
			t.Fatalf("status %q: items = %d, want 0", status, len(items))
		}
	}
}

// YouTube publishes no share and no save count for a video, and this actor sends
// neither key. A stored `shares: 0` or `collects: 0` would be read by the
// snapshot writer as a real measurement — every YouTube post ranking last behind
// posts that genuinely have none. The keys must not exist on any code path.
func TestParseYouTubeInventsNoShareOrSaveCount(t *testing.T) {
	items, err := parseYouTubeItems(ytRow(t), ytWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(mustJSON(t, items[0].Metrics), &got); err != nil {
		t.Fatalf("metrics json: %v", err)
	}
	for _, k := range []string{"shares", "shareCount", "collects", "bookmarks", "saves"} {
		if v, ok := got[k]; ok {
			t.Errorf("metrics[%q] = %v — YouTube reports no such number; absent reads as 0 downstream, "+
				"a stored 0 reads as a measured zero", k, v)
		}
	}
	// The three keys that ARE real must be present with their captured values.
	raw := mustJSON(t, items[0].Metrics)
	checkMetric(t, raw, "views", ytViews)
	checkMetric(t, raw, "likes", ytLikes)
	checkMetric(t, raw, "comments", ytComments)
}

// The subscriber count is a STRING with the unit baked in ("21.2K subscribers")
// and there is no canonical metric key for a subscriber count on a post.
// Parsing "21.2K" into 21200 would store a rounded approximation as if it were
// exact, so nothing from it may reach the metrics blob or the handle.
func TestParseYouTubeSubscriberCountIsNotAMetric(t *testing.T) {
	items, err := parseYouTubeItems(ytRow(t), ytWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(mustJSON(t, items[0].Metrics), &got); err != nil {
		t.Fatalf("metrics json: %v", err)
	}
	for _, k := range []string{"subscribers", "subscriberCount", "followers"} {
		if v, ok := got[k]; ok {
			t.Errorf("metrics[%q] = %v, want it absent: \"21.2K subscribers\" is not a number", k, v)
		}
	}
	// And the handle is the handle — not the display name, not the subscriber
	// string, and not doubled up on its "@".
	if items[0].AuthorHandle != ytHandle {
		t.Errorf("authorHandle = %q, want %q", items[0].AuthorHandle, ytHandle)
	}
	if strings.HasPrefix(items[0].AuthorHandle, "@@") {
		t.Errorf("authorHandle = %q, the row's leading @ must not be re-applied", items[0].AuthorHandle)
	}
}

// channel is a nested object on every captured row. Its absence must not cost us
// the post: the video, its text, its metrics and its date are the point, and the
// ingestor substitutes "unknown" for an empty handle anyway.
func TestParseYouTubeSurvivesMissingChannel(t *testing.T) {
	row := ytFixture(t)
	delete(row, "channel")
	items, err := ytRowsFrom(t, row, ytWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1: the post and its date are the point, not the optional object", len(items))
	}
	if items[0].AuthorHandle != "unknown" {
		t.Errorf("authorHandle = %q, want unknown when the row carries no channel", items[0].AuthorHandle)
	}
	// The post's own id is the last resort, not an empty author id.
	if items[0].AuthorID != ytID {
		t.Errorf("authorID = %q, want the video id %s as the last resort", items[0].AuthorID, ytID)
	}
	if items[0].Text == "" {
		t.Error("text = empty; a missing channel must not take the title with it")
	}
}

// A row whose channel omits `handle` still has a display name, which is a usable
// handle for display and better than "unknown" while a real name sits on the row.
func TestParseYouTubeFallsBackToChannelNameForHandle(t *testing.T) {
	row := ytFixture(t)
	row["channel"] = map[string]any{
		"id": ytChannelID, "name": "Rasyid Maulana",
		"url":             "https://www.youtube.com/channel/" + ytChannelID,
		"subscriberCount": "21.2K subscribers",
	}
	items, err := ytRowsFrom(t, row, ytWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if items[0].AuthorHandle != "Rasyid Maulana" {
		t.Errorf("authorHandle = %q, want the display name when the handle is absent", items[0].AuthorHandle)
	}
	if items[0].AuthorID != ytChannelID {
		t.Errorf("authorID = %q, want %q", items[0].AuthorID, ytChannelID)
	}
}

// An omitted counter is absent, not zero: the keys are different things
// downstream (a stored 0 is a measurement, an absent field is a gap).
func TestParseYouTubeOmittedCountersAreAbsent(t *testing.T) {
	row := ytFixture(t)
	delete(row, "views")
	delete(row, "likes")
	row["comments"] = 0 // present and zero: a video with no comments genuinely has none
	items, err := ytRowsFrom(t, row, ytWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(mustJSON(t, items[0].Metrics), &got); err != nil {
		t.Fatalf("metrics json: %v", err)
	}
	if _, ok := got["views"]; ok {
		t.Errorf("metrics.views = %v, want absent: the row omitted the field", got["views"])
	}
	if _, ok := got["likes"]; ok {
		t.Errorf("metrics.likes = %v, want absent: the row omitted the field", got["likes"])
	}
	if v, ok := got["comments"]; !ok || v != float64(0) {
		t.Errorf("metrics.comments = %v (present=%v), want a stored 0: the row said 0", v, ok)
	}
}

// The permalink has no column in the ingestor, exactly as X's does not. It is not
// stored; this pins the precedence so a future field has something correct to
// read, and asserts that a row carrying only embedUrl is not left with nothing.
func TestYouTubePermalinkPrefersURLThenEmbedURL(t *testing.T) {
	items, err := parseYouTubeItems(ytRow(t), ytWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// The parser does NOT store the permalink — mediaUrls and metrics are the
	// only row-derived string/blob fields, and neither is a permalink.
	for _, u := range items[0].MediaURLs {
		if strings.Contains(u, "watch?v=") || strings.Contains(u, "/embed/") {
			t.Errorf("mediaUrls = %v, want thumbnails only — the permalink has no column", items[0].MediaURLs)
		}
	}

	var src youTubeVideo
	if err := json.Unmarshal(ytRow(t), &src); err != nil {
		t.Fatalf("unmarshal into youTubeVideo: %v", err)
	}
	if got := youTubePermalink(src); got != "https://www.youtube.com/watch?v="+ytID {
		t.Errorf("youTubePermalink = %q, want the row's `url`", got)
	}
	src.URL = ""
	if got := youTubePermalink(src); got != src.EmbedURL {
		t.Errorf("youTubePermalink = %q, want the embedUrl fallback %q", got, src.EmbedURL)
	}
	src.EmbedURL = ""
	if got := youTubePermalink(src); got != "" {
		t.Errorf("youTubePermalink = %q, want empty when neither field is present", got)
	}
}

// streamingData and captions are large nested blobs whose only URLs are
// short-lived signed ones (expiresInSeconds ≈ 6 hours) — storing anything from
// them yields a dead link by the time the dashboard reads it. They are removed
// from the fixture on purpose, so this asserts the removal is honest AND that a
// row re-adding them changes nothing about the parse.
func TestParseYouTubeIgnoresStreamingDataAndCaptions(t *testing.T) {
	row := ytFixture(t)
	row["streamingData"] = map[string]any{
		"expiresInSeconds": "21540",
		"formats": []any{map[string]any{
			"url": "https://redirector.googlevideo.com/videoplayback?expire=1790697757&sig=dead-on-arrival",
		}},
	}
	row["captions"] = map[string]any{
		"playerCaptionsTracklistRenderer": map[string]any{
			"captionTracks": []any{map[string]any{
				"baseUrl": "https://www.youtube.com/api/timedtext?v=" + ytID + "&expire=1790697757",
			}},
		},
	}
	items, err := ytRowsFrom(t, row, ytWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	for _, u := range items[0].MediaURLs {
		if strings.Contains(u, "googlevideo") || strings.Contains(u, "timedtext") {
			t.Errorf("mediaUrls = %v, want no signed or expiring URL", items[0].MediaURLs)
		}
	}
	var got map[string]any
	if err := json.Unmarshal(mustJSON(t, items[0].Metrics), &got); err != nil {
		t.Fatalf("metrics json: %v", err)
	}
	for _, k := range []string{"streamingData", "captions", "expiresInSeconds"} {
		if v, ok := got[k]; ok {
			t.Errorf("metrics[%q] = %v, want it absent", k, v)
		}
	}
}

// The keyword path's end-to-end contract: a YouTube batch's payload reaches the
// post table with the platform, id and author the row carries. The parser is only
// reachable in production through parseItems, so a parser that works and is not
// dispatched would look identical in every other test here.
func TestParseItemsDispatchesYouTube(t *testing.T) {
	items, err := parseItems(domain.PlatformYouTube, ytRow(t), ytWindow(), "", nil)
	if err != nil {
		t.Fatalf("parseItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].ExternalID != ytID || items[0].Platform != "youtube" {
		t.Fatalf("item = %+v, want the youtube video %s", items[0], ytID)
	}
}
