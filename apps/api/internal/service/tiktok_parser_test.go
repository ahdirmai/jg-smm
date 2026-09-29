package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// mkTikTokPost builds one dataset row in xmolodtsov/tiktok-search-scraper's
// shape: flat metrics, a nested channel, a nested video, and twin upload date
// fields. Use realActorRow below instead wherever the point of the test is
// that the parser matches the actor.
func mkTikTokPost(id string, uploadedAtFormatted string, uploadedAt int64) map[string]any {
	// The two date fields are always consistent here. A row whose ISO says
	// 3 August and whose unix says 25 July would silently fall out of the
	// window the moment the ISO parse regressed, turning a date bug into a
	// "the row vanished" failure that points nowhere.
	if uploadedAt == 0 {
		if t, err := time.Parse(time.RFC3339, uploadedAtFormatted); err == nil {
			uploadedAt = t.Unix()
		}
	}
	return map[string]any{
		"id":                  id,
		"title":               "viral dance " + id,
		"uploadedAtFormatted": uploadedAtFormatted,
		"uploadedAt":          uploadedAt,
		"postPage":            "https://www.tiktok.com/@owner/video/" + id,
		"views":               9000,
		"likes":               1200,
		"comments":            34,
		"shares":              7,
		"bookmarks":           3,
		"channel": map[string]any{
			"id": "6" + id, "name": "Owner " + id, "username": "owner_" + id,
			"avatar":   "https://cdn.tiktok/avatar-" + id + ".jpg",
			"verified": false, "followers": 1200, "following": 12, "videos": 40, "likes": 27500,
		},
		"video": map[string]any{
			"width": 576, "height": 1024, "ratio": "540p", "duration": 15,
			"url":       "https://cdn.tiktok/video-" + id + ".mp4",
			"cover":     "https://cdn.tiktok/cover-" + id + ".jpg",
			"thumbnail": "https://cdn.tiktok/thumb-" + id + ".jpg",
		},
	}
}

// realActorRow is one row captured verbatim from a live
// xmolodtsov/tiktok-search-scraper run (keyword "Batulicin", 2026-09-29) with
// only the signed CDN query strings elided — every key, every nesting level
// and every value that is not a URL is exactly what the actor returned.
//
// It exists because the last TikTok parser was written against a hand-invented
// row and shipped a bug for months: the fixture asserted a `mediaUrls` entry
// the actor only emits behind a billed flag, so the suite agreed with the bug
// instead of catching it. A test that cannot fail on a renamed field is not a
// test of that field.
const realActorRow = `{
  "inputSource": "Batulicin",
  "id": "7689500671057399047",
  "title": "Ada yg baru lg di batulicin #batulicin #tanahbumbu #fypp #platinumcineplex ",
  "views": 75200,
  "likes": 1351,
  "comments": 65,
  "shares": 1352,
  "bookmarks": 148,
  "hashtags": [null, "batulicin", "tanahbumbu", "fypp", "fypppppppppppppp", "platinumcineplex"],
  "channel": {
    "id": "246423893891063808",
    "name": "CiciSuciii",
    "username": "sucirachmadiana",
    "avatar": "https://p16-common-sign.tiktokcdn-eu.com/avatar...jpeg",
    "verified": false,
    "url": "https://www.tiktok.com/@sucirachmadiana",
    "followers": 3737,
    "following": 163,
    "videos": 597,
    "likes": 27500
  },
  "collabInfo": null,
  "uploadedAt": 1790351396,
  "uploadedAtFormatted": "2026-09-25T15:49:56.000Z",
  "video": {
    "width": 576,
    "height": 1024,
    "ratio": "540p",
    "duration": 95,
    "url": "https://v16-webapp-prime.tiktok.com/video/tos/...mp4",
    "cover": "https://p16-sign.tiktokcdn.com/ogQItLV8SCXf....jpeg",
    "thumbnail": "https://p16-sign.tiktokcdn.com/oom2rfAbeAQY....jpeg"
  },
  "song": {
    "id": "7335499320058547000",
    "title": "original sound - Spotify",
    "artist": "Dylan",
    "album": null,
    "duration": 144,
    "cover": "https://p16-sign.tiktokcdn.com/537c5b69219f....jpeg"
  },
  "postPage": "https://www.tiktok.com/@sucirachmadiana/video/7689500671057399047",
  "poi": {
    "poiName": "PLATINUM CINEPLEX BATULICIN",
    "address": "Jl. Transmigrasi Plajau No.2, Kabupaten Tanah Bumbu, Kalimantan Selatan 72212, Indonesia",
    "cityCode": "9780988",
    "cityName": null,
    "latitude": null,
    "longitude": null,
    "regionCode": null
  },
  "keyword": "Batulicin"
}`

func tiktokWindow() port.KeywordTimeWindow {
	return port.KeywordTimeWindow{
		From: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC),
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

// The captured row is the ground truth this parser is written against. Every
// assertion here is a field the actor actually sends; if the actor renames one,
// or nests one differently, or starts sending it as a number, this fails.
func TestParseTikTokRealActorRow(t *testing.T) {
	items, err := parseTikTokItems([]byte(realActorRow), tiktokWindow())
	if err != nil {
		t.Fatalf("parse captured row: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	it := items[0]
	if it.Platform != "tiktok" {
		t.Errorf("platform = %q, want tiktok", it.Platform)
	}
	if it.ExternalID != "7689500671057399047" {
		t.Errorf("externalID = %q, want 7689500671057399047", it.ExternalID)
	}
	// channel is a nested object, not a flat string like the other platforms.
	if it.AuthorHandle != "sucirachmadiana" {
		t.Errorf("authorHandle = %q, want sucirachmadiana (channel.username)", it.AuthorHandle)
	}
	if it.AuthorID != "246423893891063808" {
		t.Errorf("authorID = %q, want 246423893891063808 (channel.id)", it.AuthorID)
	}
	if !strings.Contains(it.Text, "batulicin") {
		t.Errorf("text = %q, want the caption from `title`", it.Text)
	}
	if len(it.MediaURLs) != 1 || !strings.Contains(it.MediaURLs[0], "ogQItLV8SCXf") {
		t.Errorf("mediaUrls = %v, want exactly video.cover", it.MediaURLs)
	}
	want := time.Date(2026, 9, 25, 15, 49, 56, 0, time.UTC)
	if it.PublishedAt == nil || !it.PublishedAt.Equal(want) {
		t.Fatalf("publishedAt = %v, want %s", it.PublishedAt, want)
	}
	checkMetric(t, mustJSON(t, it.Metrics), "views", 75200)
	checkMetric(t, mustJSON(t, it.Metrics), "likes", 1351)
	checkMetric(t, mustJSON(t, it.Metrics), "comments", 65)
	checkMetric(t, mustJSON(t, it.Metrics), "shares", 1352)
	// bookmarks is the new actor's name for what the old one called
	// collectCount; the stored key is unchanged so stored posts keep their value.
	checkMetric(t, mustJSON(t, it.Metrics), "collects", 148)
}

// A date present but in a layout parseActorTime does not accept is a different
// path from an empty string. The post must be KEPT — the window treats a zero
// time as in-window, matching every other parser here — and the date stored as
// NULL rather than as the zero time.
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

// An offsetless ISO is rejected by RFC3339, so a parser using time.Parse alone
// stores a ZERO date that reads as "no date" rather than "3 August" — the same
// class of silent date loss as the Instagram pub_date bug.
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

// The reverse case: no usable ISO, but a Unix upload time. Reading the ISO
// unconditionally yields a zero date on exactly the rows that have data.
func TestParseTikTokFallsBackToUnixUploadTime(t *testing.T) {
	items, err := tiktokParse(t, mkTikTokPost("7123", "", 1785740811))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 || items[0].PublishedAt == nil {
		t.Fatalf("publishedAt = nil; a Unix upload time is a usable date")
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

// This actor has no date input, so the ingestor's window is the ONLY filter
// between what we paid for and what we store. Out-of-window rows must be
// dropped, and dropping one must not fail the run.
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

// A post row with no id is genuinely malformed and must say so. Silently
// dropping it would hide a broken actor behind a plausible-looking count.
func TestParseTikTokRowWithNoIDIsAnError(t *testing.T) {
	if _, err := tiktokParse(t, map[string]any{"title": "orphan", "views": 1}); err == nil {
		t.Fatal("a row with no id is malformed and must error, not be dropped silently")
	}
}

// cover is the frame, thumbnail the smaller variant, and both are free on a
// default run. A row carrying only the thumbnail must still get media — the
// alternative is the empty list that renders as "no media" on a post that has it.
func TestParseTikTokFallsBackToThumbnailWhenCoverMissing(t *testing.T) {
	row := mkTikTokPost("7123", "2026-08-03T07:06:51Z", 1785740811)
	row["video"].(map[string]any)["cover"] = ""
	items, err := tiktokParse(t, row)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items[0].MediaURLs) != 1 || !strings.Contains(items[0].MediaURLs[0], "thumb-7123") {
		t.Errorf("mediaUrls = %v, want the thumbnail when the cover is absent", items[0].MediaURLs)
	}
}

// video is absent on some rows. The post must still be stored — with no media,
// which the page renders as no thumbnail — rather than dropped over a missing
// optional object.
func TestParseTikTokSurvivesMissingVideo(t *testing.T) {
	row := mkTikTokPost("7123", "2026-08-03T07:06:51Z", 1785740811)
	delete(row, "video")
	items, err := tiktokParse(t, row)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1: a row without video is still a post", len(items))
	}
	if items[0].MediaURLs == nil || len(items[0].MediaURLs) != 0 {
		t.Errorf("mediaUrls = %v, want an empty non-nil list when the actor sent no cover", items[0].MediaURLs)
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
