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
			if p == domain.PlatformInstagram {
				return "apify/instagram-scraper"
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

	if _, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{Platform: domain.PlatformTikTok, Keywords: []string{"x"}}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("unsupported platform: want ErrValidation, got %v", err)
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
	if _, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
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
		{Status: "SUCCEEDED", RunID: "r1", ItemKeys: []string{"run/meta-0.json"}},
		{Status: "SUCCEEDED", RunID: "r2", ItemKeys: []string{"run/post-0.json"}},
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
	if res.ItemsRead != 1 || len(res.Posts) != 1 || res.Posts[0].ExternalID != "Dretry" {
		t.Fatalf("want the Dretry post from the retry, got items=%d posts=%+v", res.ItemsRead, res.Posts)
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
	if res.ItemsRead != 0 || len(res.Posts) != 0 {
		t.Fatalf("want 0 items from a metadata row, got %d / %d", res.ItemsRead, len(res.Posts))
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
	runner := &fakeRunner{out: port.ApifyOutput{Status: "SUCCEEDED", RunID: "r1", ItemKeys: keys}}
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
	if res.ItemsRead != 1 {
		t.Fatalf("itemsRead: want 1 (window drops Cout), got %d", res.ItemsRead)
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
	if res.ItemsRead != 1 {
		t.Fatalf("itemsRead: want 1 (only Din is inside the window), got %d", res.ItemsRead)
	}
	if len(res.Posts) != 1 || res.Posts[0].ExternalID != "Din" {
		t.Fatalf("posts: got %+v", res.Posts)
	}
	if h := res.Posts[0].AuthorHandle; h != "owner_Din" {
		t.Fatalf("author handle: %q", h)
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
	if res.ItemsRead != 1 {
		t.Fatalf("itemsRead: want 1 (window drops Pout), got %d", res.ItemsRead)
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
	runner := &fakeRunner{out: port.ApifyOutput{Status: "SUCCEEDED", RunID: "r1", ItemKeys: []string{"run/page-0.json"}}}
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
