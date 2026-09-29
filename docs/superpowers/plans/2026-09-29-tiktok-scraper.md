# TikTok Scraper Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Promote TikTok from a deferred platform slot to a working **keyword**-scraping target on `clockworks/tiktok-scraper`, with server-side date filtering and the post's real publication date stored.

**Scope:** keyword search only — `searchQueries[]`, the path the dashboard's keyword-batch feature drives today. The actor's `profiles[]`, `postURLs[]` and `hashtags[]` modes, the per-post scrape, and the follower add-on (`connectedTo`) are **not** in this plan; `PLATFORM_MATRIX.md` keeps their rows ⬜. Add them as a second plan if the per-post scrape is wanted — the parser built in Task 1 is reusable for `hashtags[]` unchanged.

**Tech Stack:** Go 1.24, sqlc, encoding/json, testify-style stdlib tests, `encoding/json` fixture rows, Next.js/React for the platform picker.

**Spec:** Actor input/output schema pasted from the Apify store listing for `clockworks/tiktok-scraper` (store name `clockworks/tiktok-scraper`). Platform brief to be written as `docs/platforms/tiktok.md` per `docs/platforms/_TEMPLATE.md` §5.

## Global Constraints

- The actor is addressed as `clockworks/tiktok-scraper` — a full id, never derived from `APIFY_ACTOR_PREFIX`, exactly like the other three. It goes in `APIFY_SEARCH_ACTOR_TIKTOK` in `compose.yaml`.
- **Pricing is pay-per-event, from $1.70 per 1000 results.** A single video is one result. No run may be started against the live actor before the operator has been shown the projected cost.
- **Chargeable add-ons must be absent from the payload, not merely left at their defaults**: `shouldDownloadVideos`, `shouldDownloadMusic`, `shouldDownloadSubtitles`, `shouldDownloadCoverImages`, `shouldDownloadProfilePhotos`, `downloadSummarizedVideoDescription`, `downloadAIVideoSummaries`, `downloadSubtitlesOptions`. The AI summary fields are billed **per video second** and dwarf the per-result charge.
- **Date bounds are only honoured when `profileSorting` is `latest` or `oldest`.** With `profileSorting: popular` the actor accepts the date fields and silently ignores them — the exact silent-wrong-answer failure mode `runner.go:94-96` warns about. `profileSorting: latest` is therefore mandatory in this payload, not a preference.
- Date fields are `YYYY-MM-DD` (`oldestPostDateUnified`, `newestPostDate`). Format with `.UTC().Format("2006-01-02")`.
- `keywords` in the service is 1..5 plain words or `#hashtags`; the actor takes both in `searchQueries` and a bare word and `#tag` are **not** equivalent to it — it also matches bio/content. Pass terms through unchanged; do not strip `#`.
- A wrong field name on this actor is **silent** — it falls back to its own default. Every field sent is pinned by a test in `runner_test.go`.
- The window filter still applies downstream in the ingestor regardless of what the actor was asked for. A server-side bound reduces paid waste; it is not a replacement.
- `PublishedAt` is `*time.Time` and must be **nil** when the actor gave no parseable date. Never substitute `scrapedAt`.
- Docs are Indonesian for `docs/platforms/*.md` and English for `CHANGELOG.md` / `README.md`.
- One commit per task. Run `make sqlc` only if a `.sql` query changes (it does not in this plan).

## Review Focus

The failure modes the actor's schema implies that no task below would otherwise test — most dangerous first. Each line is pinned by the test named beside it.


- **The actor exposes two date mechanisms and the brief does not say which one governs `searchQueries`.** `oldestPostDateUnified`/`newestPostDate` are documented for profile and hashtag scrapes; `videoSearchDateFilter` sits next to `videoSearchSorting` for video search. If the search path reads only the second, a payload carrying only the first returns 200 with unfiltered results — and `TestSearchInputTikTokSendsBothDateBounds` **still passes**, because it asserts the payload, not the actor's behaviour. The payload is not proof; only Task 5 Step 4 is. This is the same class of bug as the Instagram yield, and it is the one most likely to be shipped believing it is fixed.
- **A dataset row carrying `errorCode` instead of `id`.** The actor interleaves these with real results and says so in its own docs. Unhandled, the row has no `id`, the parser's external-id guard fires, and the **entire run's ingest dies** — including the 49 valid rows beside it. → Task 1, `TestParseTikTokErrorRowIsSkippedNotFatal`.
- **`createTimeISO` with no timezone offset** (e.g. `2026-08-03T07:06:51`). `time.Parse(time.RFC3339, …)` rejects it, so the post is kept with a **zero** publish date that looks like "no date" rather than "8 March". → Task 1, `TestParseTikTokOffsetlessISOIsReadAsUTC`.
- **`createTime` present as Unix seconds and `createTimeISO` absent or empty.** Picking the ISO unconditionally yields a zero time on exactly the rows that have usable data. → Task 1, `TestParseTikTokFallsBackToUnixCreateTime`.
- **`profileSorting` drifting to `popular`** — a config-level slip, not a code bug: the run still succeeds and returns the newest-and-hottest videos across all time, none of them in the window. → Task 2, `TestSearchInputTikTokSortPinsDateFilter`.
- **`searchQueries` sent where the actor wants `hashtags[]`** — a `#tag` is a hashtag query and a bare word is a full-text query; the wrong array yields posts matching nothing, with a 200 response. → Task 2, `TestSearchInputTikTokUsesSearchQueries`.
- **A `resultsPerPage` value read as a total cap.** It is a page size. 500 posts requested with `resultsPerPage: 100` bills 500 results, not 100. → Task 2, `TestSearchInputTikTokDoesNotMistakeResultsPerPageForACap`.

---

### Task 1: TikTok dataset parser

**Files:**
- Create: `apps/api/internal/service/tiktok_parser_test.go`
- Modify: `apps/api/internal/service/scrape_ingestor.go` (add `case domain.PlatformTikTok:` to `parseItems` at :272, and add the parser + `tiktokPost` types after `parseIGFlatPostItems` ends at :421)

**Interfaces:**
- Consumes: `scrapeItem` (scrape_ingestor.go:231), `parseActorTime(s string) time.Time` (:654), `port.KeywordTimeWindow`, `domain.PlatformTikTok`.
- Produces: `parseTikTokItems(raw []byte, window port.KeywordTimeWindow) ([]scrapeItem, error)` — called from `parseItems`. No other package references it.

- [ ] **Step 1: Write the failing tests**

Create `apps/api/internal/service/tiktok_parser_test.go`. Use the existing `keywordRun` helper for end-to-end reach and direct calls to the parser for the shape tests. The fixture helper, added in this file:

```go
// mkTikTokPost builds one dataset row as the actor emits it. Note the nested
// authorMeta object and the twin createTime fields.
func mkTikTokPost(id string, createTimeISO string, createTime int64) map[string]any {
	return map[string]any{
		"id":                id,
		"text":              "viral dance " + id,
		"createTimeISO":     createTimeISO,
		"createTime":        createTime,
		"webVideoUrl":       "https://www.tiktok.com/@owner/video/" + id,

		"diggCount":         1200,
		"commentCount":      34,
		"shareCount":        7,
		"collectCount":      3,
		"playCount":         9000,
		"mediaUrls":         []string{"https://p16.tiktokcdn.com/" + id + ".jpg"},
		"hashtags":          []string{"fyp", "dance"},
		"isAd":              false,
		"authorMeta":        map[string]any{"id": "6" + id, "name": "owner_" + id, "nickName": "Owner " + id, "signature": "bio", "fans": 1200, "heart": 34000, "verified": false, "uniqueId": "owner_" + id},
		"videoMeta":         map[string]any{"duration": 15, "coverUrl": "https://p16.tiktokcdn.com/cover-" + id + ".jpg", "format": "mp4", "width": 576, "height": 1024, "videoUrl": "https://v16.tiktokcdn.com/" + id + ".mp4"},
		"musicMeta":         map[string]any{"id": "77", "musicName": "original sound", "authorName": "owner_" + id, "original": true},
	}
}
```

Tests, each asserting on `res.Posts` from `keywordRun` with
`Window: KeywordWindow{From: time.Date(2026, 8, 1, 0,0,0,0, time.UTC), To: time.Date(2026, 9, 24, 0,0,0,0, time.UTC)}` and `Keywords: []string{"fyp"}`:

```go
func TestParseTikTokErrorRowIsSkippedNotFatal(t *testing.T) {
	// The actor interleaves error items with real ones and says so in its docs.
	// A row with errorCode and no id must be dropped, NOT treated as a post and
	// NOT allowed to fail the whole dataset: the 1 valid row beside it must
	// still be ingested.
	svc, _, _, _ := keywordRun(t,
		map[string]any{"errorCode": "SEARCH_QUERY_NOT_FOUND", "errorMessage": "no results for #nope", "query": "#nope"},
		mkTikTokPost("7123", "2026-08-03T07:06:51Z", 1785001611),
	)
	res, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformTikTok, Keywords: []string{"fyp"},
		Window:   KeywordWindow{From: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)},
	})
	if err != nil { t.Fatalf("one error row must not fail the run: %v", err) }
	if len(res.Posts) != 1 || res.Posts[0].ExternalID != "7123" { /* fail */ }
}

func TestParseTikTokKeepsTheValidRowsAroundAnErrorRow(t *testing.T)  // error row FIRST and LAST in the slice, 2 posts survive
func TestParseTikTokNestedAuthorAndMetrics(t *testing.T)              // authorHandle "owner_<id>" from authorMeta.name, mediaUrls == mediaUrls, checkMetric likes/diggCount->"likes", comments, shares, plays
func TestParseTikTokOffsetlessISOIsReadAsUTC(t *testing.T)            // createTimeISO "2026-08-03T07:06:51" (no Z) -> PublishedAt == 2026-08-03T07:06:51Z
func TestParseTikTokFallsBackToUnixCreateTime(t *testing.T)           // createTimeISO "" and createTime 1785001611 -> PublishedAt non-nil and equal to that unix second in UTC
func TestParseTikTokUndatedRowIsKeptWithNoPublishDate(t *testing.T)    // both create fields empty -> post kept, PublishedAt == nil (never invented)
func TestParseTikTokFiltersByWindow(t *testing.T)                     // one in-window, one 2026-07-01 -> 1 kept
```

The metric key mapping, asserted by `TestParseTikTokNestedAuthorAndMetrics` via `checkMetric`: `diggCount`→`likes`, `commentCount`→`comments`, `shareCount`→`shares`, `playCount`→`plays`, `collectCount`→`collects`.

- [ ] **Step 2: Run the tests to verify they fail**
Run: `go -C /Users/ahdirmai/MVP-1-SMM/apps/api test ./internal/service/ -run TestParseTikTok -count=1 -v`
Expected: all FAIL. `keywordRun` returns an error for `domain.PlatformTikTok` ("keyword scrape supports instagram and threads only") until Task 4 — that is a valid failure for the reach tests, but the direct-parser shape tests must be written against `parseTikTokItems` directly so they fail on `undefined: parseTikTokItems` rather than on the gate.

- [ ] **Step 3: Implement**
Add to `parseItems` (scrape_ingestor.go:272):
```go
case domain.PlatformTikTok:
    return parseTikTokItems(raw, window)
```

Add `parseTikTokItems` and its row type. The wire shape the tests pin:

```go
type tiktokPost struct {
	ID            string `json:"id"`
	Text          string `json:"text"`
	CreateTimeISO string `json:"createTimeISO"`
	CreateTime    int64  `json:"createTime"`
	WebVideoURL   string `json:"webVideoUrl"`
	DiggCount     int    `json:"diggCount"`
	CommentCount  int    `json:"commentCount"`
	ShareCount    int    `json:"shareCount"`
	CollectCount  int    `json:"collectCount"`
	PlayCount     int    `json:"playCount"`
	MediaURLs     []string `json:"mediaUrls"`
	AuthorMeta struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"authorMeta"`
	ErrorCode string `json:"errorCode"`
}
```

Parser body, four decisions the tests pin and the implementer should not re-litigate:
1. Probe `errorCode` **first**, before any other use of the row. Non-empty ⇒ `return nil, nil` (skip). Never reach the external-id guard for an error row: that guard returning an error is what kills the run.
2. `ExternalID = p.ID`; if empty after the error branch, return an error (a real row with no id is genuinely malformed).
3. Date: `t := parseActorTime(p.CreateTimeISO)`; if `t.IsZero() && p.CreateTime != 0` then `t = time.Unix(p.CreateTime, 0).UTC()`. Set `PublishedAt` via the existing `publishedAtPtr(t)` helper — nil when `t.IsZero()`.
4. `AuthorHandle = p.AuthorMeta.Name`; metrics only for non-zero counters, matching how the IG parsers skip empty ones.

- [ ] **Step 4: Run the tests to verify they pass**
Run: `go -C /Users/ahdirmai/MVP-1-SMM/apps/api test ./internal/service/ -run TestParseTikTok -count=1 -v`
Expected: the direct-parser tests PASS. The `keywordRun` reach tests still FAIL with the platform-gate error — that is expected and is what Task 4 flips.

- [ ] **Step 5: Commit**
```bash
git add apps/api/internal/service/tiktok_parser_test.go apps/api/internal/service/scrape_ingestor.go
git commit -m "feat(scrape): parse the TikTok actor's nested dataset rows"
```

---

### Task 2: TikTok search payload

**Files:**
- Modify: `apps/api/internal/adapter/apify/runner.go` (`searchInput` :97-122, and its doc comment :82-96)
- Modify: `apps/api/internal/adapter/apify/runner_test.go` (append after `TestSearchInputPerActor`)

**Interfaces:**
- Consumes: `port.ApifySearchInput`, `domain.PlatformTikTok`.
- Produces: a `case domain.PlatformTikTok:` branch in `(*Runner).searchInput`.

- [ ] **Step 1: Write the failing tests**

Append to `runner_test.go`:
```go
func TestSearchInputTikTokSendsBothDateBounds(t *testing.T) {
	r := &Runner{}
	in := port.ApifySearchInput{Platform: domain.PlatformTikTok,
		Keywords: []string{"fyp", "jadibully"},
		Window:   port.KeywordTimeWindow{From: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)},
		MaxPosts: 30}
	got := r.searchInput(in, 30)
	// searchQueries is the actor's array, not `keywords`.
	if _, ok := got["keywords"]; ok { t.Error("actor takes searchQueries, not keywords") }
	if q, ok := got["searchQueries"].([]string); !ok || len(q) != 2 || q[0] != "fyp" {
		t.Errorf("searchQueries = %v, want the terms passed through unchanged (no # stripping)", got["searchQueries"])
	}
	if v, want := got["oldestPostDateUnified"], "2026-08-01"; v != want { /* fail */ }
	if v, want := got["newestPostDate"], "2026-09-24"; v != want { /* fail */ }
	// The other side of the IG lesson: this actor CAN filter by date, unlike
	// crawlerbros, so the window must be sent rather than paid for and dropped.
	if v, want := got["profileSorting"], "latest"; v != want { /* fail */ }
}

func TestSearchInputTikTokSortPinsDateFilter(t *testing.T) {
	// profileSorting latest|oldest is the ONLY condition under which the actor
	// honours the date fields. popular silently ignores them and returns 200.
	// This test fails if anyone ever changes the sort to popular.
	/* assert got["profileSorting"] == "latest" */
}

func TestSearchInputTikTokOmitsChargeableAddons(t *testing.T) {
	// PPE: $1.70/1000 results, but the download flags are extra events and the
	// AI summary fields bill PER VIDEO SECOND. Absent, not false-by-default.
	for _, k := range []string{"shouldDownloadVideos", "shouldDownloadMusic", "shouldDownloadSubtitles",
		"shouldDownloadCoverImages", "shouldDownloadProfilePhotos",
		"downloadSummarizedVideoDescription", "downloadAIVideoSummaries", "downloadSubtitlesOptions"} {
		if _, ok := got[k]; ok { t.Errorf("%q is a chargeable add-on; it must not be in the payload", k) }
	}
}

func TestSearchInputTikTokDoesNotMistakeResultsPerPageForACap(t *testing.T) {
	// resultsPerPage is a PAGE SIZE. 500 posts at resultsPerPage 100 bills 500
	// results. The only bound on this actor is commentsPerPost, which must be 0.
	if v, ok := got["resultsPerPage"].(int); !ok || v <= 0 { /* fail */ }
	if v := got["commentsPerPost"]; v != 0 { /* fail */ }
}
```

- [ ] **Step 2: Run the tests to verify they fail**
Run: `go -C /Users/ahdirmai/MVP-1-SMM/apps/api test ./internal/adapter/apify/ -run TestSearchInputTikTok -count=1 -v`
Expected: FAIL — `searchInput` falls through to the Instagram branch, so `searchQueries` is absent and `keywords` is present.

- [ ] **Step 3: Implement**
Add to `searchInput` before the Threads branch:
```go
if in.Platform == domain.PlatformTikTok {
	payload := map[string]any{
		"searchQueries":    in.Keywords,
		"profileSorting":   "latest", // load-bearing: only latest|oldest honour the date fields
		"resultsPerPage":   maxItems,
		"commentsPerPost":  0,        // the actor bills per comment; the keyword path stores none
		"profileScrapeSections": []string{"videos"},
	}
	if !in.Window.From.IsZero() {
		payload["oldestPostDateUnified"] = in.Window.From.UTC().Format("2006-01-02")
	}
	if !in.Window.To.IsZero() {
		payload["newestPostDate"] = in.Window.To.UTC().Format("2006-01-02")
	}
	return payload
}
```
Extend the doc comment at :82-96 with a TikTok bullet stating the `profileSorting`-gates-the-date-filter rule, so the next reader does not "optimise" the sort.

- [ ] **Step 4: Run the tests to verify they pass**
Run: `go -C /Users/ahdirmai/MVP-1-SMM/apps/api test ./internal/adapter/apify/ -count=1 -v`
Expected: PASS, including the pre-existing `TestSearchInputPerActor`.

- [ ] **Step 5: Commit**
```bash
git add apps/api/internal/adapter/apify/runner.go apps/api/internal/adapter/apify/runner_test.go
git commit -m "feat(apify): send the TikTok actor a server-side date window"
```

---

### Task 3: Cost gate and platform brief

**Files:**
- Modify: `compose.yaml` (add `APIFY_SEARCH_ACTOR_TIKTOK: "clockworks/tiktok-scraper"` to the `api` service env, beside the other three)
- Create: `docs/platforms/tiktok.md` (from `docs/platforms/_TEMPLATE.md` §5)
- Modify: `README.md` (add the TikTok row to the actor table at :147-152)

**Interfaces:**
- Consumes: the actor schema in the spec.
- Produces: no code. This task exists so the first live run is preceded by a written cost number and a written record of what the actor does and does not do.

- [ ] **Step 1: Do the work**

`compose.yaml`: one env line, and **nothing else** — no `shouldDownload*` toggles, because their absence is the control.

`docs/platforms/tiktok.md`, Indonesian, mirroring the shape of `docs/platforms/instagram.md`, covering:
- §4.1 keyword scrape: actor `clockworks/tiktok-scraper`, env `APIFY_SEARCH_ACTOR_TIKTOK`, **input yang benar-benar dikirim** (the exact payload from Task 2), run mode sync, the `profileSorting`-gates-the-date rule in bold, and the `resultsPerPage`-is-not-a-cap warning.
- Output row: **nested** — `authorMeta.name` → `author_handle`, `authorMeta.id` → `author_id`, `mediaUrls[]` → `media_urls`, `diggCount`/`commentCount`/`shareCount`/`playCount`/`collectCount` → `Post.metrics` + `MetricSnapshot`, `createTimeISO`→`published_at` with `createTime` unix fallback, `webVideoUrl` → the permalink.
- `errorCode` rows: list the codes the actor emits (`PROFILE_PRIVATE`, `SEARCH_QUERY_NOT_FOUND`, `INVALID_INPUT`, `FILTER_NO_PASS`, …) and state that the parser drops them by design so one bad row cannot fail a 500-result run.
- **Tidak mengambil comment bodies.** The actor parks them in a second dataset behind `commentsDatasetURL`; this integration does not fetch it. `commentsPerPost: 0` keeps it unbilled. `ponytail:` comment naming the ceiling — keyword batches show posts, not comment threads, exactly as the Instagram keyword path behaves (`instagram.md:70`).
- **Biaya:** $1.70/1000 results PPE; add-ons billed extra and left off. State the arithmetic for one operator run.

`README.md`: add `| TikTok keyword search | clockworks/tiktok-scraper | APIFY_SEARCH_ACTOR_TIKTOK |`, and extend the "no date bound" note with its converse — this actor **does** take one, so not sending the window would be paying the Instagram mistake twice.

- [ ] **Step 2: Verify**
Run: `docker compose config | grep -A 2 APIFY_SEARCH_ACTOR_TIKTOK`
Expected: `APIFY_SEARCH_ACTOR_TIKTOK: clockworks/tiktok-scraper` present in the `api` service.

- [ ] **Step 3: Commit**
```bash
git add compose.yaml docs/platforms/tiktok.md README.md
git commit -m "docs(tiktok): platform brief, actor env, and the cost note"
```

---

### Task 4: Enable the platform

**Files:**
- Modify: `apps/api/internal/service/scrape_keyword.go:105` (validation switch)
- Modify: `apps/api/internal/domain/platform.go:29` (`MVPPlatforms`)
- Modify: `apps/api/internal/service/scrape_keyword_test.go:68-74` (`TestScrapeKeywordsValidates`)
- Modify: `packages/shared/src/constants.ts:14` (verify `tiktok` is already in the list; change only if the FE filters on `MVP`)
- Modify: `docs/PLATFORM_MATRIX.md` (§1 status `deferred` → `AKTIF` for TikTok, §2.1 scrape capability cells, §3 rollout)

**Interfaces:**
- Consumes: `parseTikTokItems` (Task 1), the TikTok payload branch (Task 2).
- Produces: a TikTok keyword batch that can be created, run, and ingested.

- [ ] **Step 1: Write the failing test**

Rewrite the TikTok half of `TestScrapeKeywordsValidates`, which today asserts the *rejection* that this task removes:

```go
func TestScrapeKeywordsValidates(t *testing.T) {
	svc, _, _ := keywordSvcFor(t, &fakeRawStorage{items: map[string][]byte{}})
	// Facebook is still a deferred slot and must stay rejected.
	if _, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{Platform: domain.PlatformFacebook, Keywords: []string{"x"}}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("deferred platform must be rejected: want ErrValidation, got %v", err)
	}
	if _, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{Platform: domain.PlatformInstagram}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("empty keywords: want ErrValidation, got %v", err)
	}
	// TikTok is now a real target: a TikTok batch must get past validation and
	// fail later (no fake search actor) rather than at the platform gate.
	if _, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{Platform: domain.PlatformTikTok, Keywords: []string{"x"}}); errors.Is(err, domain.ErrValidation) {
		t.Fatalf("tiktok must no longer be rejected as an unsupported platform: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**
Run: `go -C /Users/ahdirmai/MVP-1-SMM/apps/api test ./internal/service/ -run TestScrapeKeywordsValidates -count=1 -v`
Expected: FAIL — the TikTok assertion trips on the current gate.

- [ ] **Step 3: Implement**
- `scrape_keyword.go:105`: accept `domain.PlatformTikTok` alongside Instagram and Threads; update the error string to name all three.
- `platform.go:29`: add `PlatformTikTok` to `MVPPlatforms`.
- `packages/shared/src/constants.ts`: add `'tiktok'` to the `MVP`/active list if it is separate from the all-platforms list.
- `docs/PLATFORM_MATRIX.md`: §1 TikTok → `AKTIF`, slug `platforms/tiktok.md`; §2.1 flip the scrape rows this plan delivers (post-by-URL stays ⬜ — the keyword path does not implement it) and leave the rest; §3 note the third platform.

- [ ] **Step 4: Run the tests to verify they pass**
Run: `go -C /Users/ahdirmai/MVP-1-SMM/apps/api test ./... -count=1 && pnpm -C /Users/ahdirmai/MVP-1-SMM/apps/web exec tsc --noEmit`
Expected: both exit 0. The Task 1 `keywordRun` reach tests now go green too — confirm `TestParseTikTok*` all pass.

- [ ] **Step 5: Commit**
```bash
git add apps/api/internal/service/scrape_keyword.go apps/api/internal/domain/platform.go \
  apps/api/internal/service/scrape_keyword_test.go packages/shared/src/constants.ts docs/PLATFORM_MATRIX.md
git commit -m "feat(tiktok): enable the platform for keyword scraping"
```

---

### Task 5: First paid run — operator gate

**Files:** none. This is a manual step that is deliberately last.

- [ ] **Step 1: Rebuild and apply**
Run: `docker compose build api && docker compose up -d api`
Expected: `smm-api-1` healthy.

- [ ] **Step 2: State the cost before running**
Projected charge = (posts requested ÷ 1000) × $1.70, with the add-ons off. Show the operator the number and the fact that this is a real charge. Do not proceed on an estimate.

- [ ] **Step 3: Run a deliberately small batch**
One keyword, a 3-day window, `max_posts` = 10. This is the smallest run that still exercises the date filter and proves the yield story.

- [ ] **Step 4: Verify the four things that could silently be wrong**
```sql
SELECT external_id, published_at FROM post WHERE platform = 'TIKTOK' ORDER BY scraped_at DESC LIMIT 10;
```
- **The date bound actually bound.** Every `published_at` must fall inside the 3-day window. If any row is outside it, the actor ignored `oldestPostDateUnified`/`newestPostDate` for `searchQueries` — almost certainly because the search path reads `videoSearchDateFilter` instead. The payload tests cannot see this. **Do not record the feature as done here.** Add `videoSearchDateFilter` to the Task 2 branch with a payload test, re-run, and only then continue. This is the check that distinguishes "we sent a date filter" from "the date filter worked".
- `published_at` is non-null and **varies** across rows. If every row shows the scrape time, the date path is broken — the same defect as `87e4bab`, and the `TestParseTikTokFallsBackToUnixCreateTime` case.
- `raw_payload` row count for the run is ≥ the stored post count, proving error rows were dropped rather than ingested. If it is **equal**, the actor returned no error rows and that path is still unexercised — say so in the changelog rather than claiming error handling is proven.
- Compute the yield: stored ÷ raw items. On Instagram the comparable run was 18%. If this is not meaningfully higher, the date bound is not working regardless of what the payload says.
```sql
SELECT external_id, published_at FROM post WHERE platform = 'TIKTOK' ORDER BY scraped_at DESC LIMIT 10;
```
- **The date bound actually bound.** Every `published_at` must fall inside the 3-day window. If any row is outside it, the actor ignored `oldestPostDateUnified`/`newestPostDate` for `searchQueries` — almost certainly because the search path reads `videoSearchDateFilter` instead. The payload tests cannot see this. **Do not record the feature as done here.** Add `videoSearchDateFilter` to the Task 2 branch with a payload test, re-run, and only then continue. This is the check that separates "we sent a date filter" from "the date filter worked".
- `published_at` is non-null and **varies** across rows. If every row shows the scrape time, the date path is broken — the same defect as `87e4bab`, and the `TestParseTikTokFallsBackToUnixCreateTime` case.
- `raw_payload` row count for the run is ≥ the stored post count, proving error rows were dropped rather than ingested. If it is **equal**, the actor returned no error rows and that path is still unexercised — say so in the changelog rather than claiming error handling is proven.
- Compute the yield: stored ÷ raw items. On Instagram the comparable run was 18%. If this is not meaningfully higher, the date bound is not working regardless of what the payload says.

- [ ] **Step 5: Update CHANGELOG.md**
One `### Added` bullet under `## Unreleased` naming the actor, the server-side date filter, and the measured yield from Step 4. Commit:
```bash
git add CHANGELOG.md
git commit -m "chore: record the first TikTok run's measured yield"
```
