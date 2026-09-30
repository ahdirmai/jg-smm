package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// realXRows are two rows captured verbatim from a live
// apidojo/twitter-scraper-lite run (keyword "batulicin", 2026-09-29). Every
// key, every nesting level and every value is exactly what the actor returned;
// nothing here is hand-invented. The point of pasting the real payload rather
// than a convenient one is that a parser bug which agrees with a made-up
// fixture — a renamed metric, a handle with an "@", a date layout that does not
// exist — has to survive contact with these rows.
//
// The fields the parser must read are the awkward ones: the nested author
// (userName without "@"), the flat counts, and createdAt in Twitter's legacy
// format, which is the one field no other platform's parser can handle.
const realXRows = `[
  {
    "type": "tweet",
    "id": "2104200157558685805",
    "url": "https://x.com/kntngsy/status/2104200157558685805",
    "twitterUrl": "https://twitter.com/kntngsy/status/2104200157558685805",
    "text": "yang orang batulicin, info info kosan dong daerah batulicin yang full furniture\n#wtb #batulicin #kalsel",
    "fullText": "yang orang batulicin, info info kosan dong daerah batulicin yang full furniture\n#wtb #batulicin #kalsel",
    "source": "Twitter for iPhone",
    "retweetCount": 0,
    "replyCount": 0,
    "likeCount": 0,
    "quoteCount": 0,
    "viewCount": 46,
    "createdAt": "Sun Sep 27 13:23:06 +0000 2026",
    "lang": "in",
    "bookmarkCount": 0,
    "isReply": false,
    "conversationId": "2104200157558685805",
    "isPinned": false,
    "author": {
      "type": "user",
      "userName": "kntngsy",
      "url": "https://x.com/kntngsy",
      "twitterUrl": "https://twitter.com/kntngsy",
      "id": "1073889613385588736",
      "name": "kayal",
      "isVerified": false,
      "isBlueVerified": false,
      "profilePicture": "https://pbs.twimg.com/profile_images/1564887692303044608/eEV1xPJM_normal.jpg",
      "coverPicture": "https://pbs.twimg.com/profile_banners/1073889613385588736/1584677613",
      "description": "!!!!",
      "location": "surga",
      "followers": 1031,
      "following": 1249,
      "protected": false,
      "status": "",
      "canDm": false,
      "canMediaTag": false,
      "createdAt": "Sat Dec 15 10:36:50 +0000 2018",
      "entities": { "description": {} },
      "favouritesCount": 4659,
      "isTranslator": false,
      "mediaCount": 51,
      "statusesCount": 2240,
      "translatorTypeEnum": "none",
      "affiliatesHighlightedLabel": {},
      "possiblySensitive": false,
      "pinnedTweetIds": ["2047400168581173586"]
    },
    "extendedEntities": {},
    "card": {},
    "place": {},
    "entities": {
      "hashtags": [
        { "indices": [80, 84], "text": "wtb" },
        { "indices": [85, 95], "text": "batulicin" },
        { "indices": [96, 103], "text": "kalsel" }
      ]
    },
    "isRetweet": false,
    "isQuote": false,
    "media": [],
    "videos": [],
    "isConversationControlled": false
  },
  {
    "type": "tweet",
    "id": "2104092671589621902",
    "url": "https://x.com/wantufree/status/2104092671589621902",
    "twitterUrl": "https://twitter.com/wantufree/status/2104092671589621902",
    "text": "Sepanas panas nya kotabaru, aku bersyukur ngga sepanas mantewe atau batulicin. Pantasan rasanya pengen nabok orang kalo kepanasan karena emang se stress itu rasanya, kalo mau tau panasnya mantewe cari aja tungku orang hajatan.",
    "fullText": "Sepanas panas nya kotabaru, aku bersyukur ngga sepanas mantewe atau batulicin. Pantasan rasanya pengen nabok orang kalo kepanasan karena emang se stress itu rasanya, kalo mau tau panasnya mantewe cari aja tungku orang hajatan.",
    "source": "Twitter for Android",
    "retweetCount": 0,
    "replyCount": 0,
    "likeCount": 0,
    "quoteCount": 0,
    "viewCount": 48,
    "createdAt": "Sun Sep 27 06:15:59 +0000 2026",
    "lang": "in",
    "bookmarkCount": 0,
    "isReply": false,
    "conversationId": "2104092671589621902",
    "isPinned": false,
    "author": {
      "type": "user",
      "userName": "wantufree",
      "url": "https://x.com/wantufree",
      "twitterUrl": "https://twitter.com/wantufree",
      "id": "955107131014828033",
      "name": "Irwansyah",
      "isVerified": false,
      "isBlueVerified": false,
      "profilePicture": "https://pbs.twimg.com/profile_images/2098543587202064384/pc9Jq7KC_normal.jpg",
      "coverPicture": "https://pbs.twimg.com/profile_banners/955107131014828033/1788191556",
      "description": "recovery",
      "location": "gunung jambangan",
      "followers": 555,
      "following": 257,
      "protected": false,
      "status": "",
      "canDm": false,
      "canMediaTag": false,
      "createdAt": "Sun Jan 21 15:57:39 +0000 2018",
      "entities": { "description": {}, "url": { "urls": [] } },
      "favouritesCount": 14948,
      "isTranslator": false,
      "mediaCount": 102,
      "statusesCount": 15075,
      "translatorTypeEnum": "none",
      "affiliatesHighlightedLabel": {},
      "possiblySensitive": false,
      "pinnedTweetIds": ["2103495389626429822"]
    },
    "extendedEntities": {},
    "card": {},
    "place": {},
    "entities": {},
    "isRetweet": false,
    "isQuote": false,
    "media": [],
    "videos": [],
    "isConversationControlled": false
  }
]`

// xWindow is the operator window the captured rows were fetched inside. Both
// rows are 2026-09-27, so both are in-window.
func xWindow() port.KeywordTimeWindow {
	return port.KeywordTimeWindow{
		From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC),
	}
}

// realXRow parses one of the captured rows back into a map so a test can tweak
// a single field without rewriting the fixture.
func realXRow(t *testing.T, i int) map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal([]byte(realXRows), &rows); err != nil {
		t.Fatalf("unmarshal captured rows: %v", err)
	}
	return rows[i]
}

func xParse(t *testing.T, row map[string]any) ([]scrapeItem, error) {
	t.Helper()
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal row: %v", err)
	}
	return parseXItems(raw, xWindow())
}

// The captured rows are the ground truth this parser is written against. Each
// assertion names a field the actor really sends; a rename, a re-nesting or a
// type change on the wire fails here rather than turning into a quietly
// empty column on the dashboard.
func TestParseXRealActorRows(t *testing.T) {
	raw := realXRowsFixture(t)
	for i, want := range []struct {
		id     string
		handle string
		at     time.Time
		views  float64
	}{
		{"2104200157558685805", "kntngsy", time.Date(2026, 9, 27, 13, 23, 6, 0, time.UTC), 46},
		{"2104092671589621902", "wantufree", time.Date(2026, 9, 27, 6, 15, 59, 0, time.UTC), 48},
	} {
		// One actor row per parse, exactly as the ingestor calls it: the dataset
		// item is a single object, not the array.
		items, err := parseXItems(raw[i], xWindow())
		if err != nil {
			t.Fatalf("row %d: parse captured row: %v", i, err)
		}
		if len(items) != 1 {
			t.Fatalf("row %d: items = %d, want 1", i, len(items))
		}
		it := items[0]
		if it.Platform != "x" {
			t.Errorf("row %d: platform = %q, want x", i, it.Platform)
		}
		if it.ExternalID != want.id {
			t.Errorf("row %d: externalID = %q, want %q", i, it.ExternalID, want.id)
		}
		// author is a nested object and userName carries NO leading "@". A
		// parser that prepends one stores "@kntngsy", which the batch page then
		// renders as "@@kntngsy".
		if it.AuthorHandle != want.handle {
			t.Errorf("row %d: authorHandle = %q, want %q (author.userName, no @)", i, it.AuthorHandle, want.handle)
		}
		// createdAt is Twitter's legacy format: weekday, month name,
		// space-padded day, zone BEFORE the year.
		if it.PublishedAt == nil || !it.PublishedAt.Equal(want.at) {
			t.Errorf("row %d: publishedAt = %v, want %s — legacy createdAt is not being read", i, it.PublishedAt, want.at)
		}
		// media/videos are empty arrays on a text-only tweet: an empty list, not
		// nil, because a null reads as "no media" to some readers and "unknown"
		// to others.
		if it.MediaURLs == nil || len(it.MediaURLs) != 0 {
			t.Errorf("row %d: mediaUrls = %v, want an empty non-nil list", i, it.MediaURLs)
		}
		checkMetric(t, mustJSON(t, it.Metrics), "views", want.views)
	}

	// Both captured rows report 0 on every engagement count, and 0 is a real
	// measurement: the actor sent the field, so it is stored. This test used to
	// assert the opposite — that a zero-valued key must be absent — which is how
	// X came to serialise "0 likes" differently from the other six parsers. The
	// absent case is the one that must stay absent, and it is covered by the
	// nil-field test below.
	items, err := parseXItems(raw[0], xWindow())
	if err != nil {
		t.Fatalf("parse captured row: %v", err)
	}
	for _, k := range []string{"likes", "comments", "shares", "views"} {
		if _, ok := items[0].Metrics[k]; !ok {
			t.Errorf("metrics = %v, want a %q key: the actor reported it as 0", items[0].Metrics, k)
		}
	}
}

// The other half of the zero/absent contract: a count the actor did not send
// stays out of the map entirely, so it renders as "—" rather than as a real 0.
// This is the case the `!= 0` guard was accidentally getting right for the
// wrong reason, and the one that must keep working after the pointer change.
func TestParseXOmitsCountsTheActorDidNotSend(t *testing.T) {
	raw := []byte(`{"id":"1","createdAt":"Sun Sep 27 13:23:06 +0000 2026",
		"likeCount":0,"text":"hi",
		"author":{"id":"7","userName":"someone"}}`)
	items, err := parseXItems(raw, xWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	// The value is stored as int64, not int, so compare through a type
	// assertion: `got != 0` on an `any` holding int64(0) is always true.
	if got, ok := items[0].Metrics["likes"]; !ok || got != int64(0) {
		t.Errorf("likes = %v (present=%v), want a stored int64(0) — the actor sent likeCount:0", got, ok)
	}
	for _, k := range []string{"comments", "shares", "views"} {
		if _, ok := items[0].Metrics[k]; ok {
			t.Errorf("metrics = %v, want no %q key: the actor never sent it", items[0].Metrics, k)
		}
	}
}

// realXRowsFixture decodes the captured fixture into per-row raw JSON, so each
// parse call sees the single-object shape the ingestor actually feeds it.
func realXRowsFixture(t *testing.T) [][]byte {
	t.Helper()
	var rows []json.RawMessage
	if err := json.Unmarshal([]byte(realXRows), &rows); err != nil {
		t.Fatalf("unmarshal captured rows: %v", err)
	}
	out := make([][]byte, len(rows))
	for i, r := range rows {
		out[i] = r
	}
	return out
}

// The legacy layout is exactly the kind of thing that looks fine until a day
// number is a single digit: "2" in the layout pads with a space, so dates like
// Sep  7 parse while the zero-padded "Sep 07" would not. Pin both.
func TestParseXTimeLegacyLayout(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Time
	}{
		{"Sun Sep 27 13:23:06 +0000 2026", time.Date(2026, 9, 27, 13, 23, 6, 0, time.UTC)},
		// Single-digit day, space-padded by the actor — the format the layout's
		// "2" exists for.
		{"Mon Sep 7 05:04:03 +0000 2026", time.Date(2026, 9, 7, 5, 4, 3, 0, time.UTC)},
		// A non-UTC zone must be converted, not dropped: +0700 is 06:23:06Z.
		{"Sun Sep 27 13:23:06 +0700 2026", time.Date(2026, 9, 27, 6, 23, 6, 0, time.UTC)},
	} {
		got := parseXTime(tc.in)
		if !got.Equal(tc.want) {
			t.Errorf("parseXTime(%q) = %s, want %s", tc.in, got.Format(time.RFC3339), tc.want.Format(time.RFC3339))
		}
	}
}

// parseActorTime cannot read this format, and the consequence of believing it
// can is worse than an error: it returns the zero time, inWindow KEEPS a zero
// time, so every X post would bypass the date filter and be stored undated.
// This asserts the helper's limitation is real, so the next person reaching for
// it finds out here.
func TestParseActorTimeCannotReadTwitterLegacyDate(t *testing.T) {
	if got := parseActorTime("Sun Sep 27 13:23:06 +0000 2026"); !got.IsZero() {
		t.Fatalf("parseActorTime read %q as %s: if this now parses, parseXItems must prefer it and this test is obsolete",
			"Sun Sep 27 13:23:06 +0000 2026", got.Format(time.RFC3339))
	}
}

// The date drives the only filter this actor gets, since its payload cannot
// express a window. An out-of-window post must be dropped, and dropping one
// must not fail the run.
func TestParseXFiltersByWindow(t *testing.T) {
	row := realXRow(t, 0)
	row["createdAt"] = "Sat Aug 15 10:00:00 +0000 2026"
	items, err := xParse(t, row)
	if err != nil {
		t.Fatalf("out-of-window row must not error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %d, want 0 for a pre-window post", len(items))
	}

	row = realXRow(t, 0)
	row["createdAt"] = "Mon Sep 28 09:00:00 +0000 2026"
	items, err = xParse(t, row)
	if err != nil || len(items) != 1 {
		t.Fatalf("in-window row: items=%d err=%v, want 1 and no error", len(items), err)
	}
}

// A date we cannot read is not a date outside the window: the post is KEPT and
// its published_at is NULL, never the zero time dressed up as a real value.
func TestParseXUnreadableDateIsKeptWithNoPublishDate(t *testing.T) {
	row := realXRow(t, 0)
	row["createdAt"] = "27/09/2026 13:23:06" // rejected by every layout tried
	items, err := xParse(t, row)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want the row kept — an unreadable date is not outside the window", len(items))
	}
	if items[0].PublishedAt != nil {
		t.Fatalf("publishedAt = %v, want nil — no date may be invented", items[0].PublishedAt)
	}
}

// A row the actor sent no date for is likewise kept and left undated.
func TestParseXUndatedRowIsKeptWithNoPublishDate(t *testing.T) {
	row := realXRow(t, 0)
	delete(row, "createdAt")
	items, err := xParse(t, row)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want the undated row kept", len(items))
	}
	if items[0].PublishedAt != nil {
		t.Fatalf("publishedAt = %v, want nil", items[0].PublishedAt)
	}
}

// The captured rows are all-zero on every engagement count, so they cannot tell
// `retweetCount` from `likeCount`. This one does: the four flat counts must
// land on their canonical keys and nowhere else.
func TestParseXMapsFlatCountsToCanonicalMetricKeys(t *testing.T) {
	row := realXRow(t, 0)
	row["likeCount"] = 1200
	row["retweetCount"] = 34
	row["replyCount"] = 7
	row["viewCount"] = 45678
	// Both of these are real fields with no canonical key. They must be ignored,
	// not given a new one: a `quotes`/`bookmarks` key is stored and read by
	// nobody, so it looks populated while contributing to no ranking or KPI.
	row["quoteCount"] = 91
	row["bookmarkCount"] = 55

	items, err := xParse(t, row)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	m := mustJSON(t, items[0].Metrics)
	checkMetric(t, m, "likes", 1200)
	checkMetric(t, m, "shares", 34)   // retweetCount
	checkMetric(t, m, "comments", 7)  // replyCount
	checkMetric(t, m, "views", 45678) // viewCount

	var got map[string]any
	if err := json.Unmarshal(m, &got); err != nil {
		t.Fatalf("metrics json: %v", err)
	}
	for _, k := range []string{"quotes", "bookmarks", "quoteCount", "bookmarkCount", "retweets", "replies", "retweetCount", "replyCount"} {
		if _, ok := got[k]; ok {
			t.Errorf("metrics %v: key %q must not exist — no canonical key for it, so it is read by nobody", got, k)
		}
	}
}

// A media tweet: `media[]` carries the images and `videos[]` the video tweets.
// Both are read, in order, and a media object with no usable URL contributes
// nothing rather than an empty string.
func TestParseXCollectsMediaAndVideoURLs(t *testing.T) {
	row := realXRow(t, 0)
	row["media"] = []any{
		map[string]any{"type": "photo", "media_url_https": "https://pbs.twimg.com/media/first.jpg"},
		map[string]any{"type": "photo", "media_url_https": "https://pbs.twimg.com/media/second.jpg"},
		map[string]any{"type": "photo"}, // no URL at all
	}
	row["videos"] = []any{
		map[string]any{"url": "https://video.twimg.com/clip.mp4", "previewImageUrl": "https://pbs.twimg.com/poster.jpg"},
	}

	items, err := xParse(t, row)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{
		"https://pbs.twimg.com/media/first.jpg",
		"https://pbs.twimg.com/media/second.jpg",
		"https://video.twimg.com/clip.mp4",
	}
	if len(items[0].MediaURLs) != len(want) {
		t.Fatalf("mediaUrls = %v, want %v", items[0].MediaURLs, want)
	}
	for i := range want {
		if items[0].MediaURLs[i] != want[i] {
			t.Errorf("mediaUrls[%d] = %q, want %q", i, items[0].MediaURLs[i], want[i])
		}
	}
}

// A video tweet whose video object carries only a poster frame must still get
// media — the alternative is a post that visibly has a video rendering as
// "no media".
func TestParseXFallsBackToVideoPoster(t *testing.T) {
	row := realXRow(t, 0)
	delete(row, "media")
	row["videos"] = []any{map[string]any{"previewImageUrl": "https://pbs.twimg.com/poster-only.jpg"}}
	items, err := xParse(t, row)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items[0].MediaURLs) != 1 || items[0].MediaURLs[0] != "https://pbs.twimg.com/poster-only.jpg" {
		t.Errorf("mediaUrls = %v, want the poster frame", items[0].MediaURLs)
	}
}

// media and videos are absent entirely on some rows. The post must still be
// stored, with an empty non-nil list, rather than dropped over a missing
// optional array.
func TestParseXSurvivesMissingMediaArrays(t *testing.T) {
	row := realXRow(t, 0)
	delete(row, "media")
	delete(row, "videos")
	items, err := xParse(t, row)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1: a row without media is still a post", len(items))
	}
	if items[0].MediaURLs == nil || len(items[0].MediaURLs) != 0 {
		t.Errorf("mediaUrls = %v, want an empty non-nil list", items[0].MediaURLs)
	}
}

// fullText is the fallback when `text` is absent or blank.
func TestParseXFallsBackToFullText(t *testing.T) {
	row := realXRow(t, 0)
	delete(row, "text")
	items, err := xParse(t, row)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(items[0].Text, "full furniture") {
		t.Errorf("text = %q, want the body from `fullText`", items[0].Text)
	}
}

// A row with no id is genuinely malformed and must say so — silently dropping
// it would hide a broken actor behind a plausible-looking count.
func TestParseXRowWithNoIDIsAnError(t *testing.T) {
	row := realXRow(t, 0)
	delete(row, "id")
	if _, err := xParse(t, row); err == nil {
		t.Fatal("a row with no id is malformed and must error, not be dropped silently")
	}
}

// A row whose author object is absent entirely still stores a post. The handle
// degrades to "unknown", the same placeholder every other parser uses, rather
// than an empty string that renders as a bare "@".
func TestParseXMissingAuthorDegradesToUnknown(t *testing.T) {
	row := realXRow(t, 0)
	delete(row, "author")
	items, err := xParse(t, row)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if items[0].AuthorHandle != "unknown" {
		t.Errorf("authorHandle = %q, want unknown", items[0].AuthorHandle)
	}
}
