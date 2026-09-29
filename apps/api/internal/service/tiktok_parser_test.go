package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// mkTikTokPost builds one dataset row as clockworks/tiktok-scraper emits it.
// The nested authorMeta/videoMeta/musicMeta objects and the twin createTime
// fields are the whole reason this platform needs its own parser.
func mkTikTokPost(id string, createTimeISO string, createTime int64) map[string]any {
	// The two date fields are always consistent here. A post whose ISO says
	// 3 August and whose createTime says 25 July would silently fall out of the
	// window the moment the ISO parse regressed, turning a date bug into a
	// "the row vanished" failure that points nowhere.
	if createTime == 0 {
		if t, err := time.Parse(time.RFC3339, createTimeISO); err == nil {
			createTime = t.Unix()
		}
	}
	return map[string]any{
		"id":            id,
		"text":          "viral dance " + id,
		"createTimeISO": createTimeISO,
		"createTime":    createTime,
		"webVideoUrl":   "https://www.tiktok.com/@owner/video/" + id,
		"diggCount":     1200,
		"commentCount":  34,
		"shareCount":    7,
		"collectCount":  3,
		"playCount":     9000,
		// Empty, exactly as the actor's own documented output sample shows.
		// A previous version of this fixture put a URL here, which the actor
		// only ever emits when a billed shouldDownload* flag is on — so the
		// test asserted a shape no default run produces and the parser shipped
		// storing zero media for every TikTok post. The cover below is what
		// a default run actually returns.
		"mediaUrls": []string{},
		"hashtags":  []string{"fyp", "dance"},
		"isAd":      false,
		"authorMeta": map[string]any{
			// The actor has shipped this as both a number and a string, so the
			// parser keeps it untyped. A concretely-typed field fails to
			// unmarshal the whole row.
			"id": 67123, "name": "owner_" + id, "nickName": "Owner " + id,
			"signature": "bio", "fans": 1200, "heart": 34000, "verified": false,
		},
		"videoMeta": map[string]any{
			"duration": 15,
			// Both spellings the actor emits. The parser prefers the
			// original (uncropped) one, matching the Instagram path.
			"coverUrl":         "https://p16.tiktokcdn.com/cover-" + id + ".jpg",
			"originalCoverUrl": "https://p16.tiktokcdn.com/orig-" + id + ".jpg",
			"format":           "mp4", "width": 576, "height": 1024,
		},
		"musicMeta": map[string]any{
			"id": "77", "musicName": "original sound", "authorName": "owner_" + id, "original": true,
		},
	}
}

// mkTikTokErrorRow is the actor's interleaved error item. It carries errorCode
// and NO id — the shape that would trip an external-id guard.
func mkTikTokErrorRow(code string) map[string]any {
	return map[string]any{
		"errorCode":    code,
		"errorMessage": "search query not found",
		"query":        "#doesnotexist",
	}
}

func tiktokWindow() port.KeywordTimeWindow {
	return port.KeywordTimeWindow{
		From: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
	}
}

func tiktokParse(t *testing.T, row map[string]any) ([]scrapeItem, error) {
	t.Helper()
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal row: %v", err)
	}
	return parseTikTokItems(raw, tiktokWindow())
}

// The actor interleaves error items with real results and says so in its own
// docs. A row carrying errorCode has no id, so an unhandled one trips the
// external-id guard and the error propagates: the ENTIRE run's ingest dies,
// taking the valid rows beside it. This row must yield nothing and no error.
func TestParseTikTokErrorRowIsSkippedNotFatal(t *testing.T) {
	for _, code := range []string{"PROFILE_PRIVATE", "SEARCH_QUERY_NOT_FOUND", "INVALID_INPUT", "FILTER_NO_PASS"} {
		items, err := tiktokParse(t, mkTikTokErrorRow(code))
		if err != nil {
			t.Fatalf("%s: one error row must not fail the run: %v", code, err)
		}
		if len(items) != 0 {
			t.Fatalf("%s: got %d items, want 0 (an error row is not a post)", code, len(items))
		}
	}
}

// The valid rows on either side of an error row must all survive.
func TestParseTikTokKeepsTheValidRowsAroundAnErrorRow(t *testing.T) {
	rows := []map[string]any{
		mkTikTokErrorRow("SEARCH_QUERY_NOT_FOUND"),
		mkTikTokPost("7123", "2026-08-03T07:06:51Z", 1785740811),
		mkTikTokErrorRow("PROFILE_PRIVATE"),
		mkTikTokPost("7124", "2026-08-04T07:06:51Z", 1785827211),
		mkTikTokErrorRow("FILTER_NO_PASS"),
	}
	var kept []string
	for _, row := range rows {
		items, err := tiktokParse(t, row)
		if err != nil {
			t.Fatalf("scrape: %v", err)
		}
		for _, it := range items {
			kept = append(kept, it.ExternalID)
		}
	}
	if len(kept) != 2 || kept[0] != "7123" || kept[1] != "7124" {
		t.Fatalf("kept = %v, want [7123 7124] — the error rows must not cost us the posts", kept)
	}
}

// authorMeta is a nested OBJECT, not a string like the other platforms'
// author field. If the parser reads it flat it silently yields an empty handle.
func TestParseTikTokNestedAuthorAndMetrics(t *testing.T) {
	items, err := tiktokParse(t, mkTikTokPost("7123", "2026-08-03T07:06:51Z", 1785740811))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	it := items[0]
	if it.Platform != "tiktok" {
		t.Errorf("platform = %q, want tiktok", it.Platform)
	}
	if it.ExternalID != "7123" {
		t.Errorf("externalID = %q, want 7123", it.ExternalID)
	}
	if it.AuthorHandle != "owner_7123" {
		t.Errorf("authorHandle = %q, want owner_7123 (from nested authorMeta.name)", it.AuthorHandle)
	}
	if it.AuthorID != "67123" {
		t.Errorf("authorID = %q, want 67123 (from nested authorMeta.id)", it.AuthorID)
	}
	// mediaUrls is empty on a default run, so the cover is the only media the
	// actor gave us. Storing an empty list here is what made every scraped
	// TikTok post render with no thumbnail.
	if len(it.MediaURLs) != 1 || it.MediaURLs[0] != "https://p16.tiktokcdn.com/orig-7123.jpg" {
		t.Errorf("mediaUrls = %v, want the videoMeta original cover", it.MediaURLs)
	}
	// The counter names are TikTok's own (digg/share/collect); the stored keys
	// are the cross-platform vocabulary the dashboard already reads.
	checkMetric(t, mustJSON(t, it.Metrics), "likes", 1200)
	checkMetric(t, mustJSON(t, it.Metrics), "comments", 34)
	checkMetric(t, mustJSON(t, it.Metrics), "shares", 7)
	checkMetric(t, mustJSON(t, it.Metrics), "views", 9000)
	checkMetric(t, mustJSON(t, it.Metrics), "collects", 3)
}

// A createTimeISO that is present but in a layout parseActorTime does not
// accept is a different path from an empty string, and it is the one the actor
// is most likely to produce. The post must be KEPT — the ingestor's window
// treats a zero time as in-window, matching every other parser here — and the
// date must be stored as NULL rather than as the zero time.
func TestParseTikTokUnparseableISOIsKeptWithNoPublishDate(t *testing.T) {
	// "08/03/2026 07:06:51" — the US-slashed shape some serializers emit. NOT
	// "…07:06:51.123456": parseActorTime accepts fractional seconds through
	// RFC3339, so that string parses and would prove nothing here.
	items, err := tiktokParse(t, mkTikTokPost("7123", "08/03/2026 07:06:51", 0))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want the post kept — a date we cannot read is not a date outside the window", len(items))
	}
	if items[0].PublishedAt != nil {
		t.Fatalf("publishedAt = %v, want nil — an unparseable date must never be stored as something", items[0].PublishedAt)
	}
}

// createTimeISO frequently arrives with no timezone marker. RFC3339 rejects
// it, so a parser using time.Parse alone stores a ZERO date that reads as
// "no date" rather than "3 August" — the same class of silent date loss as
// the Instagram pub_date bug.
func TestParseTikTokOffsetlessISOIsReadAsUTC(t *testing.T) {
	items, err := tiktokParse(t, mkTikTokPost("7123", "2026-08-03T07:06:51", 0))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 || items[0].PublishedAt == nil {
		t.Fatalf("publishedAt = nil; an offsetless ISO must still be read as UTC")
	}
	want := time.Date(2026, 8, 3, 7, 6, 51, 0, time.UTC)
	if !items[0].PublishedAt.Equal(want) {
		t.Fatalf("publishedAt = %s, want %s", items[0].PublishedAt.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// The reverse case: no usable ISO, but a Unix createTime. Reading the ISO
// unconditionally yields a zero date on exactly the rows that have data.
func TestParseTikTokFallsBackToUnixCreateTime(t *testing.T) {
	items, err := tiktokParse(t, mkTikTokPost("7123", "", 1785740811))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 || items[0].PublishedAt == nil {
		t.Fatalf("publishedAt = nil; a Unix createTime is a usable date")
	}
	want := time.Unix(1785740811, 0).UTC()
	if !items[0].PublishedAt.Equal(want) {
		t.Fatalf("publishedAt = %s, want %s", items[0].PublishedAt.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// A row the actor gave no date for is KEPT — a missing field is the actor's
// gap, not a date outside the window — but must not be given an invented one.
func TestParseTikTokUndatedRowIsKeptWithNoPublishDate(t *testing.T) {
	items, err := tiktokParse(t, mkTikTokPost("7123", "", 0))
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

// The window drops the rows outside it. The actor is asked for a date range
// too, so this is a second line of defence, not the only one.
func TestParseTikTokFiltersByWindow(t *testing.T) {
	items, err := tiktokParse(t, mkTikTokPost("7123", "2026-08-03T07:06:51Z", 1785740811))
	if err != nil || len(items) != 1 {
		t.Fatalf("in-window row: items=%d err=%v, want 1 and no error", len(items), err)
	}
	items, err = tiktokParse(t, mkTikTokPost("out", "2026-07-01T04:08:08Z", 1782000000))
	if err != nil {
		t.Fatalf("out-of-window row must not error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %d, want 0 for a pre-window post", len(items))
	}
}

// A post row with no id at all and no errorCode is genuinely malformed and
// must say so — silently dropping it would hide a broken actor behind a
// plausible-looking count.
func TestParseTikTokRowWithNoIDIsAnError(t *testing.T) {
	if _, err := tiktokParse(t, map[string]any{"text": "orphan", "diggCount": 1}); err == nil {
		t.Fatal("a row with neither id nor errorCode is malformed and must error, not be dropped silently")
	}
}

func mustJSON(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal metrics: %v", err)
	}
	return b
}

// A run with a billed shouldDownload* flag set populates mediaUrls. That
// download is the higher-fidelity asset, so it must win over the cover and the
// cover must not be appended as a second, redundant slide.
func TestParseTikTokPrefersDownloadedMediaOverCover(t *testing.T) {
	row := mkTikTokPost("7123", "2026-08-03T07:06:51Z", 1785740811)
	row["mediaUrls"] = []string{
		"https://p16.tiktokcdn.com/video-7123.mp4",
		"https://p16.tiktokcdn.com/poster-7123.jpg",
	}
	items, err := tiktokParse(t, row)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := items[0].MediaURLs
	if len(got) != 2 {
		t.Fatalf("mediaUrls = %v, want exactly the 2 downloaded assets", got)
	}
	for _, u := range got {
		if strings.Contains(u, "orig-") || strings.Contains(u, "cover-") {
			t.Errorf("mediaUrls = %v, want no cover fallback mixed in", got)
		}
	}
}

// videoMeta is absent on a photomode row in some actor versions. The post must
// still be stored — with no media, which the page renders as no thumbnail —
// rather than dropped over a missing optional object.
func TestParseTikTokSurvivesMissingVideoMeta(t *testing.T) {
	row := mkTikTokPost("7123", "2026-08-03T07:06:51Z", 1785740811)
	delete(row, "videoMeta")
	items, err := tiktokParse(t, row)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1: a row without videoMeta is still a post", len(items))
	}
	if len(items[0].MediaURLs) != 0 {
		t.Errorf("mediaUrls = %v, want none when the actor sent no cover", items[0].MediaURLs)
	}
}
