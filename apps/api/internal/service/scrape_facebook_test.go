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

// realFacebookRows is testdata/facebook_row.json: two rows captured verbatim
// from a live danek/facebook-search-ppr run (keyword search over a Batulicin
// group, 2026-09-29). Every key, every nesting level and every value is exactly
// what the actor returned; nothing here is hand-invented. The point of using
// the real payload rather than a convenient one is that a parser bug which
// agrees with a made-up fixture — a renamed metric, a handle taken from
// `author.name` instead of the profile URL, a timestamp read as milliseconds —
// has to survive contact with these rows.
//
// The fields the parser must read are the awkward ones: the nested `author`
// object, the nested `image`, the flat counts, and `timestamp` as Unix SECONDS.
func realFacebookRows(t *testing.T) []map[string]any {
	t.Helper()
	b, err := os.ReadFile("testdata/facebook_row.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("unmarshal captured rows: %v", err)
	}
	return rows
}

// facebookWindow is the operator window the captured rows were fetched inside.
// Both rows are 2026, so both are in-window (row 0 is 2026-06-18, row 1 is
// 2026-09-27 — the fixture's timestamps read as seconds, not milliseconds).
func facebookWindow() port.KeywordTimeWindow {
	return port.KeywordTimeWindow{
		From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
	}
}

// realFacebookRow parses one captured row back into a map so a test can tweak a
// single field without rewriting the fixture.
func realFacebookRow(t *testing.T, i int) map[string]any {
	t.Helper()
	return realFacebookRows(t)[i]
}

func facebookParse(t *testing.T, row map[string]any) ([]scrapeItem, error) {
	t.Helper()
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal row: %v", err)
	}
	return parseFacebookItems(raw, facebookWindow())
}

// facebookFixtureRowRaw is the captured fixture as a JSON ARRAY — the shape the
// Apify dataset has. The parser reads one dataset ITEM, so a test that wants the
// whole dataset must iterate it; passing the array straight in is exactly the
// mistake a fixture with two rows invites.
func facebookFixtureRowRaw(t *testing.T, i int) []byte {
	t.Helper()
	rows := realFacebookRows(t)
	raw, err := json.Marshal(rows[i])
	if err != nil {
		t.Fatalf("marshal captured row %d: %v", i, err)
	}
	return raw
}

// The captured rows are the ground truth this parser is written against. Each
// assertion names a field the actor really sends; a rename, a re-nesting or a
// type change on the wire fails here rather than turning into a quietly empty
// column on the dashboard.
func TestParseFacebookRealActorRows(t *testing.T) {
	var items []scrapeItem
	for i := range realFacebookRows(t) {
		got, err := parseFacebookItems(facebookFixtureRowRaw(t, i), facebookWindow())
		if err != nil {
			t.Fatalf("parse captured row %d: %v", i, err)
		}
		items = append(items, got...)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}

	first := items[0]
	if first.Platform != "facebook" {
		t.Errorf("platform = %q, want facebook", first.Platform)
	}
	if first.ExternalID != "1501360315021356" {
		t.Errorf("externalID = %q, want 1501360315021356 (post_id)", first.ExternalID)
	}
	// author is a nested object. The handle is the LAST PATH SEGMENT of
	// author.url, not the display name "Rudi" — matching the sibling parsers.
	if first.AuthorHandle != "rudi.andika.182" {
		t.Errorf("authorHandle = %q, want rudi.andika.182 (last segment of author.url)", first.AuthorHandle)
	}
	if first.AuthorID != "100005218995447" {
		t.Errorf("authorID = %q, want 100005218995447 (author.id)", first.AuthorID)
	}
	if !strings.Contains(first.Text, "TOWN HOUSE") {
		t.Errorf("text = %q, want the post body from `message`", first.Text)
	}
	// TIMESTAMP IS UNIX SECONDS. time.Unix(1781773187) is 2026-06-18; reading
	// it as milliseconds would land in 1970 and the window would drop the row.
	want := time.Unix(1781773187, 0).UTC()
	if first.PublishedAt == nil || !first.PublishedAt.Equal(want) {
		t.Fatalf("publishedAt = %v, want %s — timestamp %d is not being read as Unix seconds",
			first.PublishedAt, want, 1781773187)
	}
	if first.PublishedAt.Year() != 2026 {
		t.Fatalf("publishedAt year = %d, want 2026 (a millisecond read lands in 1970)", first.PublishedAt.Year())
	}
	if len(first.MediaURLs) != 1 || !strings.Contains(first.MediaURLs[0], "725678516_3204725556378046") {
		t.Errorf("mediaUrls = %v, want the single `image.uri`", first.MediaURLs)
	}
	// reactions_count → likes. The platform's own name is kept beside it; the
	// canonical key is what the snapshot writer reads.
	checkMetric(t, mustJSON(t, first.Metrics), "likes", 18)
	checkMetric(t, mustJSON(t, first.Metrics), "comments", 17)
	checkMetric(t, mustJSON(t, first.Metrics), "shares", 0)
	// Facebook has NO view count. A `views` key here would be a fabricated
	// measurement on a platform that never reported one.
	if _, ok := first.Metrics["views"]; ok {
		t.Errorf("metrics = %v, want NO views key — Facebook publishes no view count", first.Metrics)
	}

	second := items[1]
	if second.ExternalID != "28431151943173433" {
		t.Errorf("externalID = %q, want 28431151943173433", second.ExternalID)
	}
	// A Page's author.url is /people/<Display>/<pfbid>. The pfbid IS author.id,
	// so storing it as the handle gave the reader an opaque token and nothing
	// else. The handle is the segment before it, the Page's display name.
	if second.AuthorHandle != "Muhammad-Saidi" {
		t.Errorf("authorHandle = %q, want the segment before the pfbid, not the pfbid itself", second.AuthorHandle)
	}
	wantSecond := time.Unix(1790475093, 0).UTC()
	if second.PublishedAt == nil || !second.PublishedAt.Equal(wantSecond) {
		t.Fatalf("publishedAt = %v, want %s", second.PublishedAt, wantSecond)
	}
	checkMetric(t, mustJSON(t, second.Metrics), "likes", 1)
	checkMetric(t, mustJSON(t, second.Metrics), "comments", 0)
	// image is null on this row and there are no video fields either: an empty
	// non-nil list, not nil, so the page renders "no media" rather than unknown.
	if second.MediaURLs == nil || len(second.MediaURLs) != 0 {
		t.Errorf("mediaUrls = %v, want an empty non-nil list (image is null)", second.MediaURLs)
	}
}

// A photo/link-only post has message: null (or "") and only message_rich holds
// the text. Treating an empty message as a parse failure, or storing the empty
// string, loses the caption on every such post — the UI renders a bare image.
func TestParseFacebookEmptyMessageFallsBackToMessageRich(t *testing.T) {
	row := realFacebookRow(t, 0)
	row["message"] = nil
	row["message_rich"] = "harga sewa 2 juta/bulan"

	items, err := facebookParse(t, row)
	if err != nil {
		t.Fatalf("an empty message is not a parse failure: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].Text != "harga sewa 2 juta/bulan" {
		t.Fatalf("text = %q, want the message_rich fallback", items[0].Text)
	}
}

// Both text fields absent: the post is still stored (an image-only post is a
// post), with empty text rather than a dropped row or an error.
func TestParseFacebookBothTextFieldsEmptyIsStillStored(t *testing.T) {
	row := realFacebookRow(t, 0)
	row["message"] = nil
	row["message_rich"] = nil

	items, err := facebookParse(t, row)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1: a photo-only post is still a post", len(items))
	}
	if items[0].Text != "" {
		t.Fatalf("text = %q, want empty", items[0].Text)
	}
}

// The date drives the only filter this actor gets, since its payload cannot
// express a window. An out-of-window post must be dropped, and dropping one
// must not fail the run.
func TestParseFacebookFiltersByWindow(t *testing.T) {
	items, err := parseFacebookItems([]byte(mustJSON(t, realFacebookRow(t, 0))), port.KeywordTimeWindow{
		From: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
	})
	if err != nil || len(items) != 1 {
		t.Fatalf("in-window row: items=%d err=%v, want 1 and no error", len(items), err)
	}

	row := realFacebookRow(t, 0)
	row["timestamp"] = 1750000000 // 2025-06-15
	items, err = facebookParse(t, row)
	if err != nil {
		t.Fatalf("out-of-window row must not error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %d, want 0 for a pre-window post", len(items))
	}
}

// A row the actor sent no timestamp for is KEPT — a missing field is the
// actor's gap, not a date outside the window — but no date may be invented for
// it: published_at stays NULL rather than becoming 1970.
func TestParseFacebookUndatedRowIsKeptWithNoPublishDate(t *testing.T) {
	row := realFacebookRow(t, 0)
	delete(row, "timestamp")

	items, err := facebookParse(t, row)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want the undated row kept", len(items))
	}
	if items[0].PublishedAt != nil {
		t.Fatalf("publishedAt = %v, want nil — no date may be invented", items[0].PublishedAt)
	}
}

// A row with no post_id is genuinely malformed. Dropping it silently would hide
// a broken actor behind a plausible-looking count, so it must error — but only
// after the permalink has been tried, since a video row may omit post_id.
func TestParseFacebookRowWithNoIDIsAnError(t *testing.T) {
	row := realFacebookRow(t, 0)
	row["post_id"] = nil
	row["url"] = "https://www.facebook.com/"

	if _, err := facebookParse(t, row); err == nil {
		t.Fatal("a row with neither post_id nor a usable permalink is malformed and must error")
	}
}

// A video row may omit post_id; the id must then come from the permalink, and
// it must be the STABLE post id (the numeric segment), not the video's own
// volatile watch parameter. /videos/<id>/ is the stable shape.
func TestParseFacebookFallsBackToPermalinkForID(t *testing.T) {
	row := realFacebookRow(t, 0)
	row["post_id"] = nil
	row["type"] = "video"
	row["url"] = "https://www.facebook.com/watch/videos/1501360315021356/"
	row["image"] = nil
	row["video_files"] = []any{map[string]any{"uri": "https://video.fbcdn.net/v/abc.mp4"}}
	row["video_thumbnail"] = map[string]any{"uri": "https://scontent.fbcdn.net/thumb.jpg"}

	items, err := facebookParse(t, row)
	if err != nil {
		t.Fatalf("a video row with a permalink has an id: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].ExternalID != "1501360315021356" {
		t.Fatalf("externalID = %q, want the id from /videos/<id>/", items[0].ExternalID)
	}
	// The playable file is the media; the thumbnail is only the frame.
	if len(items[0].MediaURLs) != 1 || items[0].MediaURLs[0] != "https://video.fbcdn.net/v/abc.mp4" {
		t.Fatalf("mediaUrls = %v, want the video_files entry", items[0].MediaURLs)
	}
}

// /watch/?v=<id> puts the id in the query, not the path. A path-only parser
// finds nothing and would drop the post as id-less.
func TestParseFacebookReadsWatchQueryID(t *testing.T) {
	if got := facebookIDFromURL("https://www.facebook.com/watch/?v=1501360315021356"); got != "1501360315021356" {
		t.Fatalf("facebookIDFromURL = %q, want the v= query id", got)
	}
	// A group post: the id is the segment after /posts/.
	if got := facebookIDFromURL("https://www.facebook.com/groups/537030974787633/posts/1501360315021356/"); got != "1501360315021356" {
		t.Fatalf("facebookIDFromURL = %q, want the /posts/ id", got)
	}
}

// A video post with no video_files must still get the thumbnail as media rather
// than an empty list — the alternative renders as "no media" on a post that has
// a frame.
func TestParseFacebookVideoFallsBackToThumbnail(t *testing.T) {
	row := realFacebookRow(t, 0)
	row["type"] = "video"
	row["image"] = nil
	row["video_files"] = nil
	row["video_thumbnail"] = map[string]any{"uri": "https://scontent.fbcdn.net/thumb.jpg"}

	items, err := facebookParse(t, row)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items[0].MediaURLs) != 1 || items[0].MediaURLs[0] != "https://scontent.fbcdn.net/thumb.jpg" {
		t.Fatalf("mediaUrls = %v, want the video thumbnail", items[0].MediaURLs)
	}
}

// An actor error row ("no_items", "not_found", …) carries no post. It must
// error so the caller skips it with a warning, rather than being stored as a
// post keyed by an error message.
func TestParseFacebookErrorRowIsSkippedNotFatal(t *testing.T) {
	if _, err := parseFacebookItems([]byte(`{"error":"no_items"}`), facebookWindow()); err == nil {
		t.Fatal("an error row must error, not produce a post")
	}
}

// A non-post type is a row we do not want, not a broken one: dropped silently
// (like the Threads run summary), never stored as a post. "video" is NOT such a
// type — a video post is still a post — so the probe uses a value the actor
// genuinely emits for something else.
func TestParseFacebookNonPostTypeYieldsNoItems(t *testing.T) {
	for _, typ := range []string{"comment", "page", "profile"} {
		row := realFacebookRow(t, 0)
		row["type"] = typ

		items, err := facebookParse(t, row)
		if err != nil {
			t.Fatalf("type %q must not error: %v", typ, err)
		}
		if len(items) != 0 {
			t.Fatalf("type %q: items = %d, want 0 for a non-post row", typ, len(items))
		}
	}
}

// A video post is still a post: dropping it on `type` would lose every video a
// keyword search returns, which is a large share of them.
func TestParseFacebookVideoTypeIsAPost(t *testing.T) {
	row := realFacebookRow(t, 0)
	row["type"] = "video"

	items, err := facebookParse(t, row)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1: a video is a post", len(items))
	}
}

// The handle comes from author.url. A row with no author.url keeps the display
// name rather than "unknown", which is only the last-resort fallback.
func TestParseFacebookHandleFallsBackToName(t *testing.T) {
	if got := facebookHandle("", "Rudi"); got != "Rudi" {
		t.Errorf("facebookHandle = %q, want the display name when no URL is present", got)
	}
	if got := facebookHandle("https://www.facebook.com/rudi.andika.182", "Rudi"); got != "rudi.andika.182" {
		t.Errorf("facebookHandle = %q, want the last path segment", got)
	}
	if got := facebookHandle("", ""); got != "unknown" {
		t.Errorf("facebookHandle = %q, want unknown when nothing is present", got)
	}
	// A profile URL with a trailing slash must not yield an empty segment.
	if got := facebookHandle("https://www.facebook.com/rudi.andika.182/", "Rudi"); got != "rudi.andika.182" {
		t.Errorf("facebookHandle = %q, want the last non-empty segment", got)
	}
}

// A handle that is a copy of the id column is worth nothing to a reader.
// Facebook renders a Page's permalink with the member id as the last segment,
// in two spellings seen on live rows: a numeric id and a "pfbid…" token. Both
// must yield the segment before them (the Page's name), never the id.
func TestFacebookHandleNeverReturnsAnOpaqueIdSegment(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"https://www.facebook.com/people/Endhiet-Chodetz/pfbid02bgQHbkh3mrAjxXH9jfhk6LtGAVAsaWcLrWM5YDEFnFJ7BiUscffQv8RXzsziBMPtl", "Endhiet-Chodetz"},
		{"https://www.facebook.com/people/Info-Batulicin-Simpang-Empat/61573191947639/", "Info-Batulicin-Simpang-Empat"},
		{"https://www.facebook.com/junaidi.ahmad.564758", "junaidi.ahmad.564758"},
		{"https://www.facebook.com/bgusdwifr.bgusdwifr", "bgusdwifr.bgusdwifr"},
	} {
		if got := facebookHandle(tc.url, "Some Name"); got != tc.want {
			t.Errorf("facebookHandle(%s) = %q, want %q", tc.url, got, tc.want)
		}
	}
	// A pfbid with nothing usable before it falls back to the name rather than
	// storing the id as the handle.
	if got := facebookHandle("https://www.facebook.com/pfbid02bgQHbkh3mrAjxXH9jfhk6LtGAVAsaWcLrWM", "Rudi"); got != "Rudi" {
		t.Errorf("facebookHandle(bare pfbid) = %q, want the display name", got)
	}
}

// The parser must be reachable through the ingestor's dispatch, keyed on the
// facebook platform — a parser nobody calls stores nothing.
func TestParseItemsRoutesFacebook(t *testing.T) {
	rows := realFacebookRows(t)
	raw, err := json.Marshal(rows[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	items, err := parseItems(domain.PlatformFacebook, raw, facebookWindow(), "", nil)
	if err != nil {
		t.Fatalf("parseItems(facebook): %v", err)
	}
	if len(items) != 1 || items[0].ExternalID != "1501360315021356" {
		t.Fatalf("items = %+v, want the facebook parser's output", items)
	}
	if items[0].Platform != string(domain.PlatformFacebook) {
		t.Fatalf("platform = %q, want facebook", items[0].Platform)
	}
}
