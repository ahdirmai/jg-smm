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

// realLinkedInRow is one row captured verbatim from a live
// harvestapi/linkedin-post-search run (Apify id buIWk2uOUzTmcLsuB; keyword
// "digital marketing", 2026-09-29). It is checked in whole, not paraphrased and
// not re-marshalled, because every load-bearing fact about this actor is a
// nesting level or an exact key — engagement.likes vs reactions[], postedAt.date
// beside postedAt.timestamp, author.universalName arriving null — and a
// hand-built fixture would have flattened exactly those. A test written against
// an invented row asserts the invention; this one fails when the actor changes.
//
// It matches /tmp/real_rows.json key `linkedin` verbatim.
const realLinkedInRow = "testdata/linkedin_post_search_row.json"

// The activity id, in the three places the row carries it (id, entityId, and
// the linkedinUrl permalink).
const (
	liID        = "7510340765928185856"
	liAuthorID  = "ACoAAAADIeQB-8Nfgcgdv144BnR4iqhgrt0F1lI"
	liHandle    = "miketemkin"
	liPermalink = "https://www.linkedin.com/posts/miketemkin_thoughtsandobservations-ai-jobmarket-activity-7510340765928185856-XgqS"

	// The post's own publish instant, from postedAt.date — and the same instant
	// in milliseconds as postedAt.timestamp (1790604773981).
	liPostedAtMillis int64 = 1790604773981
)

// liRow loads the captured row. The fixture is the ground truth, so a parse
// failure here is a fixture problem, not a parser one.
func liRow(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(realLinkedInRow)
	if err != nil {
		t.Fatalf("read captured row: %v", err)
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("captured row is not valid json: %v", err)
	}
	// The assertions below are only meaningful if the fixture still carries the
	// keys they are about. Without this, a fixture that lost `engagement` would
	// make the metrics test pass by asserting nothing.
	for _, k := range []string{
		"id", "entityId", "type", "content", "linkedinUrl", "author",
		"postedAt", "engagement", "postImages", "socialContent",
	} {
		if _, ok := probe[k]; !ok {
			t.Fatalf("captured row lost key %q — the fixture no longer resembles the actor's output", k)
		}
	}
	return raw
}

// liWindow brackets the captured post (2026-09-28) on both sides.
func liWindow() port.KeywordTimeWindow {
	return port.KeywordTimeWindow{
		From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC),
	}
}

// liWindowFarAfter places the captured post before the window, for the drop test.
func liWindowFarAfter() port.KeywordTimeWindow {
	return port.KeywordTimeWindow{
		From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC),
	}
}

// liRowsFrom parses an in-memory row (the fixture, edited) — for the cases that
// need a field the captured row does not have.
func liRowsFrom(t *testing.T, row map[string]any, w port.KeywordTimeWindow) ([]scrapeItem, error) {
	t.Helper()
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal row: %v", err)
	}
	return parseLinkedInItems(raw, w)
}

// liFixture decodes the captured row into a map so a test can edit one field
// and keep every other nesting level exactly as the actor sent it.
func liFixture(t *testing.T) map[string]any {
	t.Helper()
	var row map[string]any
	if err := json.Unmarshal(liRow(t), &row); err != nil {
		t.Fatalf("decode captured row: %v", err)
	}
	return row
}

// The captured row is the ground truth this parser is written against: every
// nesting level, every key and the metric mapping are asserted here. If the
// actor renames or re-nests one, this fails instead of storing an empty field.
func TestParseLinkedInRealActorRow(t *testing.T) {
	items, err := parseLinkedInItems(liRow(t), liWindow())
	if err != nil {
		t.Fatalf("parse captured row: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	it := items[0]
	if it.Platform != "linkedin" {
		t.Errorf("platform = %q, want linkedin", it.Platform)
	}
	if it.ExternalID != liID {
		t.Errorf("externalID = %q, want %s (the actor's own id, as its linkedinUrl spells it)", it.ExternalID, liID)
	}
	// author is a nested object; the ingest keys the post's author on the vanity
	// handle and needs the opaque member id beside it.
	if it.AuthorHandle != liHandle {
		t.Errorf("authorHandle = %q, want %q (author.publicIdentifier)", it.AuthorHandle, liHandle)
	}
	if it.AuthorID != liAuthorID {
		t.Errorf("authorID = %q, want %q (author.id)", it.AuthorID, liAuthorID)
	}
	if !strings.Contains(it.Text, "harness artificial intelligence") {
		t.Errorf("text = %q, want the post body from `content`", truncateForLog(it.Text))
	}
	// postImages is [] on the captured row. The post is media-less, which is
	// stored as an empty list rather than null.
	if it.MediaURLs == nil {
		t.Error("mediaUrls = nil, want an empty non-nil list for a media-less post")
	}
	if len(it.MediaURLs) != 0 {
		t.Errorf("mediaUrls = %v, want none: postImages is [] on the captured row", it.MediaURLs)
	}
	// The date comes from postedAt.date, not from the milliseconds beside it —
	// and 1790604773981 ms is the SAME instant, so the fallback agrees.
	want := time.Date(2026, 9, 28, 14, 12, 53, 981000000, time.UTC)
	if it.PublishedAt == nil || !it.PublishedAt.Equal(want) {
		t.Fatalf("publishedAt = %v, want %s (postedAt.date)", it.PublishedAt, want.Format(time.RFC3339Nano))
	}
	checkMetric(t, mustJSON(t, it.Metrics), "likes", 16)
	checkMetric(t, mustJSON(t, it.Metrics), "comments", 10)
	// shares is a REAL zero on this row and must be stored as a zero: the field
	// was present and said 0.
	checkMetric(t, mustJSON(t, it.Metrics), "shares", 0)
}

// engagement.reactions[] is a per-type breakdown of the SAME likes number
// (LIKE 15 + EMPATHY 1 = likes 16). Turning it into a metric would double the
// post's engagement under a key no other platform has, and reading it as the
// like count would lose the one type that is not a LIKE.
func TestParseLinkedInReactionsBreakdownIsNotAMetric(t *testing.T) {
	items, err := parseLinkedInItems(liRow(t), liWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(mustJSON(t, items[0].Metrics), &got); err != nil {
		t.Fatalf("metrics json: %v", err)
	}
	for _, k := range []string{"reactions", "reactionCount", "reactionIds"} {
		if v, ok := got[k]; ok {
			t.Errorf("metrics[%q] = %v, want it absent: reactions[] re-counts the same likes", k, v)
		}
	}
}

// LinkedIn publishes no view count anywhere in the row, and there is no
// socialContent flag that hides one we could still measure. A `views: 0` would
// be read by the snapshot writer as a measurement — every LinkedIn post ranking
// last behind posts whose views are genuinely zero — so the key must not exist
// on any path.
func TestParseLinkedInInventsNoViewCount(t *testing.T) {
	items, err := parseLinkedInItems(liRow(t), liWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(mustJSON(t, items[0].Metrics), &got); err != nil {
		t.Fatalf("metrics json: %v", err)
	}
	for _, k := range []string{"views", "viewCount", "impressions", "reach"} {
		if v, ok := got[k]; ok {
			t.Errorf("metrics[%q] = %v — LinkedIn reports no such number; absent reads as 0 downstream, "+
				"a stored 0 reads as a measured zero", k, v)
		}
	}
	// The snapshot writer reads these three keys and must find the real values.
	raw := mustJSON(t, items[0].Metrics)
	checkMetric(t, raw, "likes", 16)
	checkMetric(t, raw, "comments", 10)
}

// date and timestamp are the same instant on a real row, so the fallback can
// only be tested by removing the date. The timestamp is in MILLISECONDS: reading
// it as Unix seconds dates the post to 1970, which the window then drops —
// a paid-for post silently disappearing, which is what this pins.
func TestParseLinkedInFallsBackToMillisecondTimestamp(t *testing.T) {
	row := liFixture(t)
	row["postedAt"] = map[string]any{
		"timestamp": liPostedAtMillis,
		// postedAgo* deliberately present: they are human-readable and must be
		// ignored rather than parsed.
		"postedAgoShort": "20h",
		"postedAgoText":  "20 hours ago • Edited • Visible to anyone on or off LinkedIn",
	}
	items, err := liRowsFrom(t, row, liWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1 — a millisecond timestamp is a usable date, and one read as "+
			"seconds would put this post in 1970 and out of the window", len(items))
	}
	if items[0].PublishedAt == nil {
		t.Fatal("publishedAt = nil, want the instant from postedAt.timestamp")
	}
	want := time.UnixMilli(liPostedAtMillis).UTC()
	if !items[0].PublishedAt.Equal(want) {
		t.Fatalf("publishedAt = %s, want %s (timestamp is milliseconds, not seconds)",
			items[0].PublishedAt.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
	// 2026, not 1970 — stated explicitly, because "in the window" would also be
	// satisfied by a date read as seconds if the window were wide enough.
	if got := items[0].PublishedAt.Year(); got != 2026 {
		t.Fatalf("publishedAt year = %d, want 2026 (a millisecond value read as seconds yields 1970)", got)
	}
}

// date wins when both are present and disagree: it is the exact instant the
// actor rendered, and the fallback must not override it.
func TestParseLinkedInPrefersDateOverTimestamp(t *testing.T) {
	row := liFixture(t)
	row["postedAt"] = map[string]any{
		"date":      "2026-09-15T08:30:00.000Z",
		"timestamp": liPostedAtMillis,
	}
	items, err := liRowsFrom(t, row, liWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := time.Date(2026, 9, 15, 8, 30, 0, 0, time.UTC)
	if items[0].PublishedAt == nil || !items[0].PublishedAt.Equal(want) {
		t.Fatalf("publishedAt = %v, want %s (postedAt.date)", items[0].PublishedAt, want.Format(time.RFC3339))
	}
}

// An unreadable date string falls through to the timestamp rather than being
// stored as the zero time: the ms value is a full date, and a post stored
// undated has no publish date at all.
func TestParseLinkedInUnreadableDateFallsThroughToTimestamp(t *testing.T) {
	row := liFixture(t)
	row["postedAt"] = map[string]any{
		"date":      "28/09/2026 14:12:53",
		"timestamp": liPostedAtMillis,
	}
	items, err := liRowsFrom(t, row, liWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := time.UnixMilli(liPostedAtMillis).UTC()
	if items[0].PublishedAt == nil || !items[0].PublishedAt.Equal(want) {
		t.Fatalf("publishedAt = %v, want %s", items[0].PublishedAt, want.Format(time.RFC3339Nano))
	}
}

// A row with neither a readable date nor a timestamp is KEPT — a missing field
// is the actor's gap, not a date outside the window — and must not be given an
// invented one.
func TestParseLinkedInUndatedRowIsKeptWithNoPublishDate(t *testing.T) {
	row := liFixture(t)
	row["postedAt"] = map[string]any{}
	items, err := liRowsFrom(t, row, liWindow())
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

// This actor takes no date input, so the ingestor's window is the ONLY filter
// between what we paid for and what we store. An out-of-window post must be
// dropped, and dropping one must not fail the run.
func TestParseLinkedInFiltersByWindow(t *testing.T) {
	items, err := parseLinkedInItems(liRow(t), liWindow())
	if err != nil || len(items) != 1 {
		t.Fatalf("in-window row: items=%d err=%v, want 1 and no error", len(items), err)
	}
	items, err = parseLinkedInItems(liRow(t), liWindowFarAfter())
	if err != nil {
		t.Fatalf("out-of-window row must not error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %d, want 0 for a post before the window", len(items))
	}
}

// id and entityId are the same string on a real row. entityId is the fallback
// for a build that omits id — an empty external id is rejected by the ingestor,
// so the post would be dropped over which of two identical fields came first.
func TestParseLinkedInFallsBackToEntityID(t *testing.T) {
	row := liFixture(t)
	delete(row, "id")
	items, err := liRowsFrom(t, row, liWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].ExternalID != liID {
		t.Fatalf("externalID = %q, want %s from entityId", items[0].ExternalID, liID)
	}
}

// A row with neither id is malformed and must say so rather than storing a post
// under an empty key. This is the shape of the actor's error rows.
func TestParseLinkedInRowWithNoIDIsAnError(t *testing.T) {
	if _, err := liRowsFrom(t, map[string]any{"type": "post", "content": "orphan"}, liWindow()); err == nil {
		t.Fatal("a row with no id is malformed and must error, not be dropped silently")
	}
}

// An error row (no id, an error message) is an error, so the caller logs it and
// moves on — one broken row must not stop the well-formed ones beside it.
func TestParseLinkedInErrorRowIsAnErrorNotAPost(t *testing.T) {
	if _, err := liRowsFrom(t, map[string]any{"error": "no_items"}, liWindow()); err == nil {
		t.Fatal("an error row must error rather than ingest as a post")
	}
}

// The actor interleaves non-post rows with the posts. They carry no id, so
// without the type check they would either error (noisy, and one bad row beside
// 50 good ones is a warning the operator learns to ignore) or — worse, if they
// ever gain an id — land in the post table as junk.
func TestParseLinkedInNonPostRowYieldsNoItems(t *testing.T) {
	for _, typ := range []string{"comment", "job", "company", "info"} {
		items, err := liRowsFrom(t, map[string]any{"type": typ, "id": "123", "content": "x"}, liWindow())
		if err != nil {
			t.Fatalf("type %q: err = %v, want a silent skip", typ, err)
		}
		if len(items) != 0 {
			t.Fatalf("type %q: items = %d, want 0", typ, len(items))
		}
	}
}

// author.universalName is null on the captured row, and a null must survive
// unmarshalling. Any type mismatch anywhere in the row fails the WHOLE row:
// a member whose universalName arrives as a number (older actor builds sent
// ids as numbers) would take the post's text, metrics and date down with it.
func TestParseLinkedInToleratesNullAndNumericAuthorFields(t *testing.T) {
	row := liFixture(t)
	row["author"] = map[string]any{
		"id":               12345, // number, not the string the captured row sends
		"universalName":    nil,
		"publicIdentifier": nil,
	}
	items, err := liRowsFrom(t, row, liWindow())
	if err != nil {
		t.Fatalf("parse: %v (a null universalName must not fail the row)", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	// No public identifier: the opaque id is the fallback, and the post must not
	// be stored with a handle of "unknown" while its real id sits right there.
	if items[0].AuthorHandle != "12345" {
		t.Errorf("authorHandle = %q, want the numeric author.id %q", items[0].AuthorHandle, "12345")
	}
	if items[0].AuthorID != "12345" {
		t.Errorf("authorID = %q, want %q", items[0].AuthorID, "12345")
	}
}

// hideReactionsCount is a real flag the actor sends. Nothing maps it — a hidden
// counter arrives as 0 and a hidden 0 is indistinguishable from a real 0, so the
// honest value is the number we were given. What must NOT happen is a flag
// costing us the post, or a hidden count turning into a fabricated one.
func TestParseLinkedInHiddenCountsDoNotChangeTheStoredNumbers(t *testing.T) {
	row := liFixture(t)
	row["socialContent"] = map[string]any{
		"hideCommentsCount":        true,
		"hideReactionsCount":       true,
		"hideSocialActivityCounts": true,
		"hideRepostsCount":         true,
	}
	row["engagement"] = map[string]any{
		"id": "7510340764174995458", "likes": 0, "comments": 0, "shares": 0,
		"reactions": []any{},
	}
	items, err := liRowsFrom(t, row, liWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want the post stored despite hidden counters", len(items))
	}
	// Zeros that the actor reported as fields are stored as zeros. They are not
	// invented away, and no flag-derived metric is added on top.
	for k, v := range map[string]float64{"likes": 0, "comments": 0, "shares": 0} {
		checkMetric(t, mustJSON(t, items[0].Metrics), k, v)
	}
}

// An omitted counter is absent, not zero: the keys are different things
// downstream (a stored 0 is a measurement, an absent field is a gap).
func TestParseLinkedInOmittedCountersAreAbsent(t *testing.T) {
	row := liFixture(t)
	row["engagement"] = map[string]any{"id": "7510340764174995458", "shares": 0}
	items, err := liRowsFrom(t, row, liWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(mustJSON(t, items[0].Metrics), &got); err != nil {
		t.Fatalf("metrics json: %v", err)
	}
	if _, ok := got["likes"]; ok {
		t.Errorf("metrics.likes = %v, want absent: the row omitted the field", got["likes"])
	}
	if _, ok := got["comments"]; ok {
		t.Errorf("metrics.comments = %v, want absent: the row omitted the field", got["comments"])
	}
	if v, ok := got["shares"]; !ok || v != float64(0) {
		t.Errorf("metrics.shares = %v (present=%v), want a stored 0: the row said 0", v, ok)
	}
}

// socialContent is an object on the real row. Nothing in it is read, and its
// absence from a row must not cost the post either.
func TestParseLinkedInSurvivesMissingNestedObjects(t *testing.T) {
	row := liFixture(t)
	delete(row, "socialContent")
	delete(row, "postImages")
	delete(row, "engagement")
	delete(row, "author")
	items, err := liRowsFrom(t, row, liWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1: the post and its date are the point, not the optional objects", len(items))
	}
	if items[0].AuthorHandle != "unknown" {
		t.Errorf("authorHandle = %q, want unknown when the row carries no author", items[0].AuthorHandle)
	}
	if items[0].AuthorID != liID {
		t.Errorf("authorID = %q, want the post's own id as the last resort", items[0].AuthorID)
	}
	if items[0].MediaURLs == nil || len(items[0].MediaURLs) != 0 {
		t.Errorf("mediaUrls = %v, want an empty non-nil list", items[0].MediaURLs)
	}
	if string(mustJSON(t, items[0].Metrics)) == "null" {
		t.Error("metrics = null, want {} — a null metrics payload is not the same as an empty one")
	}
}

// The captured row has postImages: []. No image-bearing row was available, so
// the entry shape follows the actor's documented media object (url/width/height)
// — which is exactly the kind of guess that shipped the TikTok media bug. Rather
// than assert a field the actor was never seen to send, the parser accepts the
// plausible spellings AND hunts for the first URL anywhere in the entry, and
// each path is pinned here.
func TestParseLinkedInPostImageURLShapes(t *testing.T) {
	cases := []struct {
		name  string
		entry map[string]any
	}{
		{"url", map[string]any{"url": "https://media.licdn.com/img-a.jpg", "width": 1200, "height": 627}},
		{"nested image", map[string]any{"image": map[string]any{"url": "https://media.licdn.com/img-b.jpg"}}},
		{"fullImage", map[string]any{"fullImage": "https://media.licdn.com/img-c.jpg"}},
		{"unknown key", map[string]any{"displayUrl": "https://media.licdn.com/img-d.jpg"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := liFixture(t)
			row["postImages"] = []any{tc.entry}
			items, err := liRowsFrom(t, row, liWindow())
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(items[0].MediaURLs) != 1 {
				t.Fatalf("mediaUrls = %v, want exactly one image URL", items[0].MediaURLs)
			}
			if !strings.HasPrefix(items[0].MediaURLs[0], "https://media.licdn.com/") {
				t.Errorf("mediaUrls[0] = %q, want the image URL", items[0].MediaURLs[0])
			}
		})
	}
}

// An entry carrying no URL at all must contribute nothing rather than an empty
// string, which would be a broken image in the dashboard.
func TestParseLinkedInUnusablePostImageIsSkipped(t *testing.T) {
	row := liFixture(t)
	row["postImages"] = []any{
		map[string]any{"width": 800, "height": 800},
		map[string]any{"url": "https://media.licdn.com/img-good.jpg"},
	}
	items, err := liRowsFrom(t, row, liWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items[0].MediaURLs) != 1 || !strings.Contains(items[0].MediaURLs[0], "img-good") {
		t.Fatalf("mediaUrls = %v, want only the usable entry", items[0].MediaURLs)
	}
}

// The actor truncates long posts itself, mid-word, and appends "…". It does not
// tell us it did (there is no truncated flag), so the only safe thing is to
// store the text as given: content is not empty, and nothing is invented to fill
// the gap. The captured row is a real, already-truncated LinkedIn post.
func TestParseLinkedInContentIsStoredUpToTheActorsTruncation(t *testing.T) {
	var row map[string]any
	if err := json.Unmarshal(liRow(t), &row); err != nil {
		t.Fatalf("decode: %v", err)
	}
	content, _ := row["content"].(string)
	items, err := parseLinkedInItems(liRow(t), liWindow())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if items[0].Text != content {
		t.Fatalf("text = %q, want `content` verbatim — the actor's own truncation is not ours to alter",
			truncateForLog(items[0].Text))
	}
}

// The parser is only reachable in production through parseItems, so a parser
// that works perfectly and is never dispatched would look identical in every
// test above. This test goes through the same door the ingestor uses.
func TestParseLinkedInThroughTheIngestDispatch(t *testing.T) {
	items, err := parseLinkedInViaDispatch(t, liRow(t), liWindow())
	if err != nil {
		t.Fatalf("parseItems(linkedin): %v — the parser is not dispatched for %s, so every "+
			"LinkedIn batch would ingest zero posts", err, domain.PlatformLinkedIn)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].ExternalID != liID || items[0].Platform != "linkedin" {
		t.Fatalf("item = %+v, want the linkedin post %s", items[0], liID)
	}
}

// parseLinkedInViaDispatch calls the real parseItems for the LinkedIn platform
// through an explicit function variable.
//
// The indirection exists because this package is edited concurrently: naming
// parseItems directly would make this file fail to compile — all 21 tests, not
// just this one — against a tree where its signature has moved. A function
// variable with the signature spelled out still binds to the production
// function, and a signature change is then one compile error, right here,
// naming the contract that moved.
var parseLinkedInViaDispatch = func(t *testing.T, raw []byte, w port.KeywordTimeWindow) ([]scrapeItem, error) {
	t.Helper()
	// The scrapeItem input contract for a dataset row: which platform's shape,
	// the raw row, the operator's window, the single-code allowlist (empty = the
	// keyword path), and the per-run state only Threads uses.
	var dispatch func(domain.Platform, []byte, port.KeywordTimeWindow, string, *threadsRun) ([]scrapeItem, error) = parseItems
	return dispatch(domain.PlatformLinkedIn, raw, w, "", nil)
}

func truncateForLog(s string) string {
	if len(s) <= 80 {
		return s
	}
	return s[:80] + "…"
}
