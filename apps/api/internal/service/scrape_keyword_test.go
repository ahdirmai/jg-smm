package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// fakeRawStorage is a port.RawStorage over a map; the test preloads the dataset
// items the "actor" would have written.
type fakeRawStorage struct{ items map[string][]byte }

func (s *fakeRawStorage) Put(ctx context.Context, key string, payload []byte) (int64, error) {
	s.items[key] = payload
	return int64(len(payload)), nil
}
func (s *fakeRawStorage) Get(ctx context.Context, key string) ([]byte, error) {
	b, ok := s.items[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return b, nil
}

func keywordSvcFor(t *testing.T, storage port.RawStorage) (*KeywordScrapeService, *fakeScrapeStore, *fakeRunner) {
	t.Helper()
	store := newFakeScrapeStore()
	runner := &fakeRunner{out: port.ApifyOutput{Status: "SUCCEEDED", RunID: "r1"}}
	svc := NewKeywordScrapeService(
		store,
		runner,
		func(p domain.Platform) string {
			switch p {
			case domain.PlatformInstagram:
				return "apify/instagram-scraper"
			case domain.PlatformThreads:
				return "futurizerush/meta-threads-scraper"
			case domain.PlatformTikTok:
				return "xmolodtsov/tiktok-search-scraper"
			}
			return ""
		},
		storage,
		nil,
	)
	svc.SetBatchStore(newFakeKeywordBatchStore())
	return svc, store, runner
}

// checkMetric asserts a metrics JSON blob carries want under key.
func checkMetric(t *testing.T, metrics json.RawMessage, key string, want float64) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(metrics, &got); err != nil {
		t.Fatalf("metrics %s: bad json: %v", key, err)
	}
	v, ok := got[key]
	if !ok {
		t.Fatalf("metrics %s: missing; got %v", key, got)
	}
	n, ok := v.(float64)
	if !ok || n != want {
		t.Fatalf("metrics %s: want %v, got %v", key, want, v)
	}
}

func TestScrapeKeywordsValidates(t *testing.T) {
	svc, _, _ := keywordSvcFor(t, &fakeRawStorage{items: map[string][]byte{}})

	// Facebook is still a deferred slot and must stay rejected.
	if _, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{Platform: domain.PlatformFacebook, Keywords: []string{"x"}}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("deferred platform: want ErrValidation, got %v", err)
	}
	// TikTok is a real target now. A TikTok batch must get PAST the platform
	// gate — whatever fails after that is a different problem, and reporting
	// it as "unsupported platform" would send the operator looking in the wrong
	// place entirely.
	if _, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{Platform: domain.PlatformTikTok, Keywords: []string{"x"}}); errors.Is(err, domain.ErrValidation) {
		t.Fatalf("tiktok must no longer be rejected as an unsupported platform: %v", err)
	}
	if _, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{Platform: domain.PlatformInstagram}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("no keywords: want ErrValidation, got %v", err)
	}
	if _, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{"a", "b", "c", "d", "e", "f"},
	}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf(">5 keywords: want ErrValidation, got %v", err)
	}
	// A platform that passes the gate but has no actor configured is still
	// rejected. Built inline with an empty resolver: keywordSvcFor supplies an
	// actor for every scrapable platform, so it cannot express this case.
	noActor := NewKeywordScrapeService(
		newFakeScrapeStore(),
		&fakeRunner{out: port.ApifyOutput{Status: "SUCCEEDED", RunID: "r1"}},
		func(domain.Platform) string { return "" },
		&fakeRawStorage{items: map[string][]byte{}},
		nil,
	)
	noActor.SetBatchStore(newFakeKeywordBatchStore())
	if _, err := noActor.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformThreads,
		Keywords: []string{"x"},
	}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("no actor for platform: want ErrValidation, got %v", err)
	}
}

func TestScrapeKeywordsDedupesAndCaps(t *testing.T) {
	svc, _, runner := keywordSvcFor(t, &fakeRawStorage{items: map[string][]byte{}})
	_, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{" kamu ", "Kamu", "", "kamu", "gacor"},
	})
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if len(runner.search) == 0 {
		t.Fatal("no search run")
	}
	// Every attempt carries the deduped list (this storage is empty, so the
	// empty-result retry may add a second attempt — dedupe must hold for all).
	want := []string{"kamu", "gacor"}
	for i, run := range runner.search {
		got := run.Keywords
		if len(got) != len(want) {
			t.Fatalf("run %d keywords: want %v, got %v", i, want, got)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("run %d keywords[%d]: want %q, got %q", i, j, want[j], got[j])
			}
		}
	}
}

// The Instagram hashtag actor intermittently returns a related hashtag's
// metadata row instead of posts (measured ~1 run in 3), which the ingestor
// reports as zero posts on a SUCCEEDED run. The batch retries once rather than
// recording an empty success.
func TestScrapeKeywordsRetriesEmptyResult(t *testing.T) {
	store := newFakeScrapeStore()
	storage := &fakeRawStorage{items: map[string][]byte{}}
	// Attempt 1's dataset: only a metadata row, no posts.
	meta, _ := json.Marshal(map[string]any{
		"searchTerm": "batulicin", "searchSource": "google", "name": "peturing", "posts": "824",
	})
	storage.items["run/meta-0.json"] = meta
	// Attempt 2's dataset: a real post inside the window.
	post, _ := json.Marshal(mkIGPostRow("Dretry", "2026-09-10T02:28:41.000Z"))
	storage.items["run/post-0.json"] = post

	runner := &fakeRunner{outs: []port.ApifyOutput{
		{Status: "SUCCEEDED", RunID: "r1", Items: []port.RawItem{{Key: "run/meta-0.json"}}},
		{Status: "SUCCEEDED", RunID: "r2", Items: []port.RawItem{{Key: "run/post-0.json"}}},
	}}
	svc := NewKeywordScrapeService(store, runner,
		func(domain.Platform) string { return "apify/instagram-scraper" }, storage, nil)
	svc.SetBatchStore(newFakeKeywordBatchStore())

	res, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{"batulicin"},
		Window:   KeywordWindow{From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if len(runner.search) != 2 {
		t.Fatalf("want 2 actor attempts (metadata-only then posts), got %d", len(runner.search))
	}
	// The retry's page delivered one postable row; the metadata-only first
	// attempt delivered none. So 1 read, 1 stored — the count reflects the
	// actor's rows, not the number of HTTP attempts.
	if res.ItemsRead != 1 || len(res.Posts) != 1 || res.Posts[0].ExternalID != "Dretry" {
		t.Fatalf("want 1 delivered / 1 stored (Dretry), got items=%d posts=%+v", res.ItemsRead, res.Posts)
	}
}

// A metadata-only row (a related hashtag's formatted count, no posts[]) must
// yield zero items instead of failing the whole payload: `posts` is a string
// there, and unmarshalling it into a slice used to kill the run's ingest.
func TestScrapeKeywordsMetadataRowIsNotAnError(t *testing.T) {
	svc, _, _, _ := keywordRun(t, map[string]any{
		"searchTerm": "batulicin", "searchSource": "google",
		"name": "peturing", "posts": "824", "postsCount": 824,
	})

	res, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{"batulicin"},
	})
	if err != nil {
		t.Fatalf("metadata-only row must not error, got %v", err)
	}
	// A metadata row carries no posts, so the actor delivered nothing storable.
	if res.ItemsRead != 0 || len(res.Posts) != 0 {
		t.Fatalf("want 0 delivered / 0 stored from a metadata row, got %d / %d", res.ItemsRead, len(res.Posts))
	}
}

// mkIGSearchPage builds one ~smm/instagram-search-scraper dataset row: a
// resolved page with the posts nested under posts[] (the wire shape the real
// actor emits — see /tmp/bs.json).
func mkIGSearchPage(term string, posts ...map[string]any) map[string]any {
	return map[string]any{
		"searchTerm":   term,
		"searchSource": "hashtag",
		"name":         term,
		"postsCount":   len(posts),
		"posts":        posts,
	}
}

// mkIGSearchPost builds one element of a search page row's posts[]: raw Instagram
// media-dict field names (code, taken_at, caption.text, image_versions2).
func mkIGSearchPost(code string, takenAt int64, inWindow bool) map[string]any {
	media := "https://scontent.cdn.instagram.com/" + code + ".jpg"
	return map[string]any{
		"code":          code,
		"pk":            "99" + code[len(code)-3:],
		"taken_at":      takenAt,
		"caption":       map[string]any{"text": code + " caption", "created_at": takenAt},
		"like_count":    10,
		"comment_count": 2,
		"play_count":    77,
		"user":          map[string]any{"username": "someone_" + code, "pk": "42"},
		"image_versions2": map[string]any{
			"candidates": []map[string]any{
				{"url": media, "width": 1080, "height": 1080},
				{"url": media + "?w=750", "width": 750, "height": 750},
			},
		},
		"_inWindow": inWindow,
	}
}

// mkIGPostRow builds one flat post row: apify/instagram-scraper's shape with
// searchType:hashtag (also apify/instagram-post-scraper's). Field names taken
// from a live run — shortCode/ownerUsername/timestamp RFC3339.
func mkIGPostRow(code, timestamp string) map[string]any {
	return map[string]any{
		"id":             "31" + code,
		"shortCode":      code,
		"type":           "Image",
		"caption":        code + " caption",
		"ownerUsername":  "owner_" + code,
		"ownerId":        "42",
		"displayUrl":     "https://scontent.cdn.instagram.com/" + code + ".jpg",
		"likesCount":     11,
		"commentsCount":  3,
		"videoViewCount": 88,
		"timestamp":      timestamp,
		"hashtags":       []string{"batulicin"},
	}
}

// mkThreadsPost builds one futurizerush/meta-threads-scraper row in search mode.
func mkThreadsPost(code, createdAt string) map[string]any {
	return map[string]any{
		"record_type":  "post",
		"post_code":    code,
		"post_url":     "https://www.threads.com/@owner_" + code + "/post/" + code,
		"text_content": code + " text",
		"created_at":   createdAt,
		"like_count":   7,
		"reply_count":  2,
		"repost_count": 1,
		"username":     "owner_" + code,
		"user_id":      "42",
	}
}

// keywordRun stores pages under the keys the runner reports and returns the
// service wired over them.
func keywordRun(t *testing.T, pages ...map[string]any) (*KeywordScrapeService, *fakeScrapeStore, *fakeRunner, *fakeRawStorage) {
	t.Helper()
	store := newFakeScrapeStore()
	storage := &fakeRawStorage{items: map[string][]byte{}}
	keys := make([]string, len(pages))
	for i, page := range pages {
		key := fmt.Sprintf("run/page-%d.json", i)
		body, err := json.Marshal(page)
		if err != nil {
			t.Fatalf("marshal page: %v", err)
		}
		storage.items[key] = body
		keys[i] = key
	}
	runner := &fakeRunner{out: port.ApifyOutput{Status: "SUCCEEDED", RunID: "r1", Items: rawItems(keys)}}
	svc := NewKeywordScrapeService(
		store,
		runner,
		func(p domain.Platform) string {
			switch p {
			case domain.PlatformInstagram:
				return "apify/instagram-scraper"
			case domain.PlatformThreads:
				return "futurizerush/meta-threads-scraper"
			}
			return ""
		},
		storage,
		nil,
	)
	svc.SetBatchStore(newFakeKeywordBatchStore())
	return svc, store, runner, storage
}

func TestScrapeKeywordsRunsAndIngests(t *testing.T) {
	svc, _, _, _ := keywordRun(t,
		mkIGSearchPage("kopi susu",
			mkIGSearchPost("Cabc123", 1789063634, true), // 2026-09-10
		),
	)

	res, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{"kamu"},
		Window:   KeywordWindow{From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if res.ItemsRead != 1 {
		t.Fatalf("itemsRead: want 1, got %d", res.ItemsRead)
	}
	if len(res.Posts) != 1 || res.Posts[0].ExternalID != "Cabc123" {
		t.Fatalf("posts: got %+v", res.Posts)
	}
	post := res.Posts[0]
	if post.AuthorHandle != "someone_Cabc123" {
		t.Fatalf("author handle: %q", post.AuthorHandle)
	}
	if post.Text == nil || *post.Text != "Cabc123 caption" {
		t.Fatalf("text: %v", post.Text)
	}
	if len(post.MediaURLs) != 1 || post.MediaURLs[0] != "https://scontent.cdn.instagram.com/Cabc123.jpg" {
		t.Fatalf("media: %v", post.MediaURLs)
	}
	// The first candidate is the largest variant; the ingestor takes that one.
	checkMetric(t, post.Metrics, "likes", float64(10))
	checkMetric(t, post.Metrics, "views", float64(77)) // play_count fallback
	if res.Keywords[0] != "kamu" || res.ActorID != "apify/instagram-scraper" {
		t.Fatalf("result meta: %+v", res)
	}
}

// The stored itemsRead must be the count the parser yielded, not the count
// that survived dedup/upsert — and it must differ visibly from PostsCount when
// the window drops rows. This pin is what surfaced a real batch reporting
// "31 items read" from a 50-item dataset: every window drop was invisible
// because the reported number was the stored count.
func TestScrapeKeywordsItemsReadCountsDeliveredRows(t *testing.T) {
	svc, _, _, _ := keywordRun(t,
		mkIGPostRow("Din", "2026-09-10T02:28:41.000Z"),  // in window
		mkIGPostRow("Ddup", "2026-09-12T02:28:41.000Z"), // in window, same batch
		mkIGPostRow("Dout", "2026-08-01T04:08:08.000Z"), // before window
	)

	res, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{"batulicin"},
		Window:   KeywordWindow{From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if len(res.Posts) != 2 {
		t.Fatalf("posts: window should drop only Dout, got %d", len(res.Posts))
	}
	// itemsRead is what the actor delivered (all three rows); the window, not
	// the parser, is what reduced the stored set. Conflating the two made the
	// dashboard's "N posts · N items read" read as "the window dropped nothing".
	if res.ItemsRead != 3 {
		t.Fatalf("itemsRead: want 3 delivered, got %d", res.ItemsRead)
	}
}

// The search actor ignores the since/until we send, so the ingestor has to drop
// posts outside the operator's window itself.
func TestScrapeKeywordsFiltersByWindow(t *testing.T) {
	svc, _, _, _ := keywordRun(t,
		mkIGSearchPage("kopi susu",
			mkIGSearchPost("Cin", 1789063634, true),   // 2026-09-10
			mkIGSearchPost("Cout", 1785607634, false), // 2026-08-01
		),
	)

	res, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{"kopi susu"},
		Window:   KeywordWindow{From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if res.ItemsRead != 2 {
		t.Fatalf("itemsRead: want 2 delivered (window drops Cout), got %d", res.ItemsRead)
	}
	if len(res.Posts) != 1 || res.Posts[0].ExternalID != "Cin" {
		t.Fatalf("posts: got %+v", res.Posts)
	}
}

// A hashtag search returns one FLAT post row per post (apify/instagram-scraper
// with searchType:hashtag) — the post-scraper shape, not the nested search-page
// row. Those rows carry the only date bound the actor applies as a lower bound,
// so the window's upper bound is enforced here.
func TestScrapeKeywordsFlatPostRows(t *testing.T) {
	svc, _, _, _ := keywordRun(t,
		mkIGPostRow("Din", "2026-09-10T02:28:41.000Z"),   // in window
		mkIGPostRow("Dout", "2026-08-01T04:08:08.000Z"),  // before window
		mkIGPostRow("Dtail", "2026-10-05T09:00:34.000Z"), // after window
	)

	res, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{"batulicin"},
		Window: KeywordWindow{
			From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			To:   time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if res.ItemsRead != 3 {
		t.Fatalf("itemsRead: want 3 delivered (only Din is inside the window), got %d", res.ItemsRead)
	}
	if len(res.Posts) != 1 || res.Posts[0].ExternalID != "Din" {
		t.Fatalf("posts: got %+v", res.Posts)
	}
	if h := res.Posts[0].AuthorHandle; h != "owner_Din" {
		t.Fatalf("author handle: %q", h)
	}
}

// crawlerbros/instagram-keyword-search-scraper returns a THIRD shape: one flat
// post row per item, but snake_case. The two IG flat shapes (apify's camelCase
// post row and this one) are indistinguishable by shortcode alone, because Go's
// unmarshal is case-insensitive — only the underscore tells them apart, and it is
// exactly the part a camelCase struct drops. Reading this row through the
// post-scraper struct yielded `likes: null, comments: null` and author `unknown`,
// so the batch dashboard showed no counts at all for a scrape that had them.
//
// Field names below are copied from a real run of this actor (batch
// d816d824-875d-4392-a42d-8dd38753fe44), not from its input schema.
func mkIGCrawlerbrosRow(code, pubDate string) map[string]any {
	return map[string]any{
		"post_url":    "https://www.instagram.com/p/" + code + "/",
		"shortcode":   code,
		"post_id":     "3" + code,
		"username":    "owner_" + code,
		"author_meta": map[string]any{"id": "477269714", "username": "owner_" + code},
		"caption":     code + " caption",
		"pub_date":    pubDate,
		"media_type":  "Reel",
		"like_count":  73,
		// The whole point of this fixture: the actor reports a comment COUNT but
		// never the comment bodies, so the count must survive parsing.
		"comment_count":       13,
		"likes_hidden":        false,
		"comments_disabled":   false,
		"input_keyword":       "batulicin",
		"media_items":         []any{map[string]any{"index": 1, "type": "Reel", "url": "https://scontent.cdninstagram.com/" + code + ".mp4"}},
		"thumbnail_url":       "https://scontent.cdninstagram.com/" + code + ".jpg",
		"scraped_at":          "2026-08-03T07:06:51",
		"is_paid_partnership": false,
	}
}

func TestScrapeKeywordsCrawlerbrosFlatRows(t *testing.T) {
	svc, _, _, _ := keywordRun(t,
		mkIGCrawlerbrosRow("DbkaUUqxYkg", "2026-08-03T07:06:51"), // in window
		mkIGCrawlerbrosRow("Dout", "2026-07-01T04:08:08"),        // before window
	)

	res, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{"batulicin"},
		Window: KeywordWindow{
			From: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			To:   time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if len(res.Posts) != 1 || res.Posts[0].ExternalID != "DbkaUUqxYkg" {
		t.Fatalf("posts: want 1 (only the in-window row), got %+v", res.Posts)
	}
	p := res.Posts[0]
	if p.AuthorHandle != "owner_DbkaUUqxYkg" {
		t.Errorf("author handle: got %q, want owner_DbkaUUqxYkg (snake_case username)", p.AuthorHandle)
	}
	// The regression this pins: these came back as JSON null before the flat
	// parser existed, and metricsLine skips non-numbers, so the row rendered bare.
	checkMetric(t, p.Metrics, "likes", float64(73))
	checkMetric(t, p.Metrics, "comments", float64(13))
	if len(p.MediaURLs) != 1 || p.MediaURLs[0] != "https://scontent.cdninstagram.com/DbkaUUqxYkg.mp4" {
		t.Errorf("media: got %v, want the single media_items url", p.MediaURLs)
	}
}

// The regression this pins: the window filter read pub_date to decide the row
// was in range and then dropped the value on the floor, so a post ingested on
// 2026-09-29 was stored with no date at all and the UI rendered scrapedAt in
// its place — every post in a batch read as the day the batch ran. The kept
// row must carry the very date it was filtered on.
func TestScrapeKeywordsStoresThePublishDateItFilteredOn(t *testing.T) {
	svc, _, _, _ := keywordRun(t,
		mkIGCrawlerbrosRow("DbkaUUqxYkg", "2026-08-03T07:06:51"),
	)

	res, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{"batulicin"},
		Window: KeywordWindow{
			From: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			To:   time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if len(res.Posts) != 1 {
		t.Fatalf("posts: want 1, got %d", len(res.Posts))
	}
	got := res.Posts[0].PublishedAt
	if got == nil {
		t.Fatal("publishedAt is nil — the date the window filtered on was discarded")
	}
	want := time.Date(2026, 8, 3, 7, 6, 51, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("publishedAt = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// A row the actor gave no parseable date for is KEPT (a missing field is the
// actor's gap, not a date outside the window) but must not be given an invented
// publish date. NULL here is the honest answer; scrapedAt remains the labelled
// fallback.
func TestScrapeKeywordsUndatedRowIsKeptWithNoPublishDate(t *testing.T) {
	svc, _, _, _ := keywordRun(t, mkIGCrawlerbrosRow("Dnodate", ""))

	res, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{"batulicin"},
		Window: KeywordWindow{
			From: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			To:   time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if len(res.Posts) != 1 {
		t.Fatalf("posts: want the undated row kept, got %d", len(res.Posts))
	}
	if res.Posts[0].PublishedAt != nil {
		t.Fatalf("publishedAt = %v, want nil — no date was supplied, so none may be stored", res.Posts[0].PublishedAt)
	}
}

// The Threads search actor returns a flat post row with an RFC3339 created_at;
// the window drops the rows outside it.
func TestScrapeKeywordsThreadsWindow(t *testing.T) {
	svc, _, _, _ := keywordRun(t,
		mkThreadsPost("Pin", "2026-09-10T11:30:53+00:00"),  // in window
		mkThreadsPost("Pout", "2026-08-20T11:11:27+00:00"), // before window
	)
	svc.searchActorFor = func(p domain.Platform) string {
		if p == domain.PlatformThreads {
			return "futurizerush/meta-threads-scraper"
		}
		return ""
	}

	res, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformThreads,
		Keywords: []string{"batulicin"},
		Window: KeywordWindow{
			From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			To:   time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if res.ItemsRead != 2 {
		t.Fatalf("itemsRead: want 2 delivered (window drops Pout), got %d", res.ItemsRead)
	}
	if len(res.Posts) != 1 || res.Posts[0].ExternalID != "Pin" {
		t.Fatalf("posts: got %+v", res.Posts)
	}
}

// A page that resolved but holds no posts (a small hashtag, postsCount: 0) is a
// successful empty search: zero items, not an error. See /tmp/bs2.json.
func TestScrapeKeywordsEmptyPageIsNotAnError(t *testing.T) {
	svc, _, _, _ := keywordRun(t, mkIGSearchPage("carnision"))

	res, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{"carnision"},
	})
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if res.ItemsRead != 0 || len(res.Posts) != 0 {
		t.Fatalf("empty page: want 0 items, got %d / %d posts", res.ItemsRead, len(res.Posts))
	}
}

// A carousel post carries one image per carousel_media entry; the ingestor
// collects them in order.
func TestScrapeKeywordsCarouselMedia(t *testing.T) {
	store := newFakeScrapeStore()
	storage := &fakeRawStorage{items: map[string][]byte{}}
	page := mkIGSearchPage("kopi", map[string]any{
		"code":            "Ccar",
		"pk":              "1",
		"taken_at":        1789063634,
		"caption":         map[string]any{"text": "carousel"},
		"user":            map[string]any{"username": "car", "pk": "7"},
		"image_versions2": map[string]any{"candidates": []map[string]any{{"url": "https://x/cover.jpg"}}},
		"carousel_media": []map[string]any{
			{"image_versions2": map[string]any{"candidates": []map[string]any{{"url": "https://x/a.jpg"}}}},
			{"video_versions": []map[string]any{{"url": "https://x/b.mp4"}}},
		},
	})
	body, _ := json.Marshal(page)
	storage.items["run/page-0.json"] = body
	runner := &fakeRunner{out: port.ApifyOutput{Status: "SUCCEEDED", RunID: "r1", Items: []port.RawItem{{Key: "run/page-0.json"}}}}
	svc := NewKeywordScrapeService(store, runner, func(domain.Platform) string { return "apify/instagram-scraper" }, storage, nil)
	svc.SetBatchStore(newFakeKeywordBatchStore())

	res, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{"kopi"},
	})
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if res.ItemsRead != 1 {
		t.Fatalf("itemsRead: want 1, got %d", res.ItemsRead)
	}
	want := []string{"https://x/a.jpg", "https://x/b.mp4"}
	if len(res.Posts[0].MediaURLs) != len(want) {
		t.Fatalf("media: want %v, got %v", want, res.Posts[0].MediaURLs)
	}
	for i := range want {
		if res.Posts[0].MediaURLs[i] != want[i] {
			t.Fatalf("media[%d]: want %q, got %q", i, want[i], res.Posts[0].MediaURLs[i])
		}
	}
}

func TestScrapeKeywordsFailsOnActorFailure(t *testing.T) {
	svc, _, runner := keywordSvcFor(t, &fakeRawStorage{items: map[string][]byte{}})
	runner.out = port.ApifyOutput{Status: "FAILED", Error: "boom"}
	if _, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{"kamu"},
	}); err == nil {
		t.Fatal("want error on FAILED actor run")
	}
}

// mkThreadsScraperPost builds one themineworks/threads-scraper post row, the
// actor the per-post and keyword Threads scrapes now point at. Shape copied from
// the actor's documented output: raw Threads/snake_case field names (code /
// text / posted_at / media_urls), numeric post_id, and separate reply rows
// rather than replies[] nested under the post.
func mkThreadsScraperPost(code, postedAt string) map[string]any {
	return map[string]any{
		"post_id":      "3" + code,
		"code":         code,
		"text":         code + " text",
		"posted_at":    postedAt,
		"username":     "owner_" + code,
		"like_count":   12,
		"reply_count":  2,
		"repost_count": 1,
		"quote_count":  3,
		"has_media":    true,
		"media_urls":   []any{"https://scontent.cdninstagram.com/" + code + ".jpg"},
		"media_type":   "image",
		"is_reply":     false,
		"is_repost":    false,
	}
}

// mkThreadsReply builds one reply row for parent. This actor emits replies as
// their OWN top-level dataset rows, flagged is_reply and naming the parent by
// numeric parent_post_id — it does not nest them the way the previous actor did.
// That numeric id is the whole reason the ingestor keeps a per-run post-id map:
// the parent post row is keyed by shortcode, so the two only join if the run
// remembered the parent it already emitted.
func mkThreadsReply(parentCode, code, text, postedAt string) map[string]any {
	return map[string]any{
		"post_id":        "3" + code,
		"code":           code,
		"text":           text,
		"posted_at":      postedAt,
		"username":       "replier_" + code,
		"like_count":     3,
		"reply_count":    0,
		"repost_count":   0,
		"quote_count":    0,
		"media_urls":     []any{},
		"is_reply":       true,
		"parent_post_id": "3" + parentCode,
	}
}

// The current Threads actor reports snake_case names (code, text, posted_at)
// and emits replies as SEPARATE rows linked by numeric parent_post_id. Neither
// the field names nor the link shape existed in the legacy fixture, so without
// this test a shape regression silently yields a post with no text, no media
// and no comments. The reply rows are passed AFTER the post on purpose: that is
// the actor's order, and the only order in which the run's post-id map can
// resolve their parent.
func TestScrapeKeywordsThreadsShape(t *testing.T) {
	svc, store, _, _ := keywordRun(t,
		mkThreadsScraperPost("DdGJIzFiWkJ", "2026-09-10T10:00:00+00:00"),
		mkThreadsReply("DdGJIzFiWkJ", "RdGJIzFiWk1", "first reply", "2026-09-10T10:05:00+00:00"),
		mkThreadsReply("DdGJIzFiWkJ", "RdGJIzFiWk2", "second reply", "2026-09-10T10:06:00+00:00"),
	)
	svc.searchActorFor = func(p domain.Platform) string {
		if p == domain.PlatformThreads {
			return "themineworks/threads-scraper"
		}
		return ""
	}

	res, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformThreads,
		Keywords: []string{"batulicin"},
		Window: KeywordWindow{
			From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			To:   time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	// Only the post is a post: the two reply rows must not become posts of their
	// own, which is what would happen if the is_reply branch were dropped.
	if len(res.Posts) != 1 {
		t.Fatalf("posts: want 1 (replies are comments, not posts), got %+v", res.Posts)
	}
	p := res.Posts[0]
	if p.ExternalID != "DdGJIzFiWkJ" {
		t.Errorf("external id: got %q, want DdGJIzFiWkJ (parsed from `code`)", p.ExternalID)
	}
	if p.AuthorHandle != "owner_DdGJIzFiWkJ" {
		t.Errorf("author: got %q", p.AuthorHandle)
	}
	if p.Text == nil || *p.Text != "DdGJIzFiWkJ text" {
		t.Errorf("text: got %v, want the `text` field", p.Text)
	}
	if len(p.MediaURLs) != 1 {
		t.Fatalf("media: want 1 url from media_urls[], got %v", p.MediaURLs)
	}
	checkMetric(t, p.Metrics, "likes", float64(12))
	checkMetric(t, p.Metrics, "comments", float64(2))

	// The reply rows must attach to the post as its comments — this is the
	// behaviour the dashboard's "Fetch comments" button depends on, and it only
	// works if the run remembered the parent's post_id -> shortcode mapping.
	if res.Comments != 2 {
		t.Fatalf("comments: want 2 replies ingested, got %d", res.Comments)
	}
	comments, err := store.ListCommentsByPost(context.Background(), p.ID, nil, nil)
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("stored comments: want 2, got %d", len(comments))
	}
	for _, c := range comments {
		if c.Text != "first reply" && c.Text != "second reply" {
			t.Errorf("comment text: unexpected %q", c.Text)
		}
	}
}

// A reply whose parent post is not in the run is stored as a top-level post,
// not dropped and not stored against a guessed parent.
//
// The actor's keyword mode flags `is_reply` on a row while leaving
// `replied_to_post_code` empty and never delivering the parent: a real batch
// arrived with 19 of 50 rows in exactly this shape. Dropping them silently
// halved the scrape's value, so the orphan falls back to being a post — its
// metrics and text survive; only the parent link is lost.
func TestScrapeKeywordsThreadsReplyWithoutParentFallsBackToPost(t *testing.T) {
	svc, _, _, _ := keywordRun(t,
		mkThreadsReply("DdGJIzFiWkJ", "RdGJIzFiWk1", "orphan reply", "2026-09-10T10:05:00+00:00"),
	)
	svc.searchActorFor = func(p domain.Platform) string {
		if p == domain.PlatformThreads {
			return "themineworks/threads-scraper"
		}
		return ""
	}

	res, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformThreads,
		Keywords: []string{"batulicin"},
		Window: KeywordWindow{
			From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			To:   time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if len(res.Posts) != 1 || res.Comments != 0 {
		t.Fatalf("want the orphan reply kept as 1 post / 0 comments, got %d / %d", len(res.Posts), res.Comments)
	}
	if res.Posts[0].ExternalID != "RdGJIzFiWk1" {
		t.Fatalf("post code: want RdGJIzFiWk1, got %q", res.Posts[0].ExternalID)
	}
}

// The Instagram and Threads parsers store a *int64 in scrapeItem.Metrics to
// distinguish "the actor reported 0" from "the actor reported nothing". Until
// numeric() learned to follow that pointer, every one of those posts stored a
// snapshot row of zeros and ranked last in TopPostsByMetric, while post.metrics
// itself looked perfectly correct — so no test caught it, because the jsonb
// column is marshalled directly and only the snapshot path goes through here.
// These cases are the whole contract: a real zero survives, an absent metric
// stays absent.
func TestNumericFollowsMetricPointers(t *testing.T) {
	zero, five := int64(0), int64(5)
	for _, tc := range []struct {
		name  string
		in    any
		want  int64
		wantK bool
	}{
		{"reported zero is a measurement", &zero, 0, true},
		{"reported value", &five, 5, true},
		{"absent stays absent", (*int64)(nil), 0, false},
		{"plain zero", 0, 0, true},
	} {
		got, ok := numeric(tc.in)
		if got != tc.want || ok != tc.wantK {
			t.Errorf("numeric(%s) = (%d, %v), want (%d, %v)", tc.name, got, ok, tc.want, tc.wantK)
		}
	}
	// The end-to-end shape: a parser that reports 0 comments must yield a
	// pointer to zero, not nil, or the snapshot stores NULL where a real
	// measurement exists.
	it := scrapeItem{Metrics: map[string]any{"comments": &zero}}
	if p := it.metricsIntPtr("comments"); p == nil || *p != 0 {
		t.Errorf("metricsIntPtr(comments) = %v, want a pointer to 0", p)
	}
	it = scrapeItem{Metrics: map[string]any{}}
	if p := it.metricsIntPtr("comments"); p != nil {
		t.Errorf("metricsIntPtr(comments) on an absent key = %v, want nil", p)
	}
}
