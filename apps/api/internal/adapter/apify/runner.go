// Package apify talks to the Apify v2 API to run scrape actors (P2-03).
//
// The adapter implements port.ApifyRunner. It is the ONLY place in the codebase
// that knows Apify's HTTP shape: the scheduler and the ingestor work purely in
// domain terms. A run's dataset items are streamed into object storage; only the
// S3 keys are persisted (RawPayload), never the payload body.
package apify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// Runner runs Apify actors over HTTP. It is safe to call concurrently: every
// run is a fresh request and the client holds no per-run state.
type Runner struct {
	client   *http.Client
	baseURL  string
	token    string
	storage  port.RawStorage
	actorFor func(domain.Platform) string
	log      func(msg string, args ...any)
}

// Config wires the runner. Storage is required: a run's dataset items are
// downloaded straight into object storage so the raw payload never lives in
// the API process (and never lands in the DB).
type Config struct {
	BaseURL  string
	Token    string
	Timeout  time.Duration
	Storage  port.RawStorage
	ActorFor func(domain.Platform) string
	Log      func(msg string, args ...any)
}

// New validates config and returns the runner. A missing token is an error:
// scraping is the runner's only job, so it has no degraded mode to fall into.
func New(cfg Config) (*Runner, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("apify: base url is required")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("apify: token is required")
	}
	if cfg.Storage == nil {
		return nil, errors.New("apify: raw storage is required")
	}
	if cfg.ActorFor == nil {
		return nil, errors.New("apify: actor lookup is required")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	log := cfg.Log
	if log == nil {
		log = func(string, ...any) {}
	}
	return &Runner{
		client:   &http.Client{Timeout: timeout},
		baseURL:  strings.TrimRight(cfg.BaseURL, "/"),
		token:    strings.TrimSpace(cfg.Token),
		storage:  cfg.Storage,
		actorFor: cfg.ActorFor,
		log:      log,
	}, nil
}

var _ port.ApifyRunner = (*Runner)(nil)

// searchInput builds the actor input for a keyword search. Each search actor
// has its own field names and its own idea of a date filter, so the payload is
// per-platform:
//
//   - Instagram (crawlerbros/instagram-keyword-search-scraper): `keywords` is an
//     ARRAY of terms (plain words or #hashtags, mixed freely) and `maxPosts` is
//     the per-keyword result cap. It has NO date input at all — every field it
//     accepts is a keyword/limit/cookie — so the window is applied downstream by
//     the ingestor, not requested here.
//   - Threads (futurizerush/meta-threads-scraper): `mode:search` with a
//     `keywords[]` array, and both `start_date`/`end_date` bounds.
//   - TikTok (xmolodtsov/tiktok-search-scraper): `keywords[]` of search terms,
//     plus `maxItems`, which this actor honours as a hard cap on rows returned
//     and so is the billed ceiling too. It has NO date input at all, so the
//     window is applied downstream by the ingestor and nowhere else.
//     `sortType: DATE_POSTED` is documented as sorting newest-first but was
//     measured on 2026-09 returning the same unsorted pool as the default, so
//     it is not sent; a field that reads as a filter and is not one is exactly
//     the silent-wrong-answer shape this function exists to prevent.
//
// A wrong field name is silent here: the actor falls back to its own default
// and returns unrelated posts, so these names are pinned against the live input
// schemas in runner_test.go.
// This is a switch, not a chain of ifs, and its default returns nil. The old
// form fell through to the Instagram payload, so any platform not yet listed
// here was silently POSTed `keywords`/`maxPosts` — actors that spell it
// differently ignore those keys and answer 200 with whatever their own default
// returns. A wrong field name is invisible; a missing branch is not.
func (r *Runner) searchInput(in port.ApifySearchInput, maxItems int) map[string]any {
	switch in.Platform {
	case domain.PlatformInstagram:
		return instagramSearchInput(in, maxItems)
	case domain.PlatformThreads:
		return threadsSearchInput(in, maxItems)
	case domain.PlatformTikTok:
		return tiktokSearchInput(in, maxItems)
	case domain.PlatformYouTube:
		return youTubeSearchInput(in, maxItems)
	case domain.PlatformLinkedIn:
		return linkedInSearchInput(in, maxItems)
	case domain.PlatformX:
		return xSearchInput(in, maxItems)
	case domain.PlatformFacebook:
		return facebookSearchInput(in, maxItems)
	}
	return nil
}

// instagramSearchInput builds the payload for
// sasky/instagram-keyword-posts-urls-scraper.
//
// This actor returns post URLs ONLY — no caption, no metrics. That is why the
// runner enriches each URL with a per-post scrape (see runSearchInstagram): the
// search stage is discovery, the post actor is the data. The previous actor
// (crawlerbros/instagram-keyword-search-scraper) returned full posts in one
// call but the publisher has been removed from the Store, so every batch 403'd.
//
// No date keys. The actor accepts `date`/`from`/`to` but FAILS at runtime
// ("Context: Parsing date..") for every combination tried, so a window cannot be
// enforced here — the ingestor applies it instead (inWindow), which is the same
// contract the previous actor had.
//
// `limit` is a STRING in this actor's schema, not a number: sending an int is
// silently ignored and the run falls back to an enormous default.
func instagramSearchInput(in port.ApifySearchInput, maxItems int) map[string]any {
	return map[string]any{
		"keywords": in.Keywords,
		"limit":    strconv.Itoa(maxItems),
	}
}

// threadsSearchInput builds the payload for futurizerush/meta-threads-scraper,
// whose date bounds are the only two that have ever been verified to filter
// server-side rather than being accepted and discarded.
func threadsSearchInput(in port.ApifySearchInput, maxItems int) map[string]any {
	payload := map[string]any{
		"mode":          "search",
		"keywords":      in.Keywords,
		"max_posts":     maxItems,
		"search_filter": "recent",
	}
	if !in.Window.From.IsZero() {
		payload["start_date"] = in.Window.From.UTC().Format("2006-01-02")
	}
	if !in.Window.To.IsZero() {
		payload["end_date"] = in.Window.To.UTC().Format("2006-01-02")
	}
	return payload
}

// youTubeSearchInput builds the payload for streamers/youtube-scraper in
// search mode. The actor spells the terms `searchQueries`, the cap
// `maxResults`, and its date filter `dateFilter` (hour/today/week/month/year);
// `keywords`/`maxItems`/`uploadDate` belong to the retired apidojo actors and
// are ignored silently here.
func youTubeSearchInput(in port.ApifySearchInput, maxItems int) map[string]any {
	payload := map[string]any{
		"searchQueries": in.Keywords,
		"maxResults":    maxItems,
	}
	// Omit the key for an open window: the filter is nullable and omitting it
	// means no date restriction (the ingestor still applies the exact window).
	if bucket := youTubeDateFilter(in.Window); bucket != "" {
		payload["dateFilter"] = bucket
	}
	return payload
}

// youTubeDateFilter maps the requested lower bound to streamers' enum. The
// filter is coarse and relative; the ingestor narrows to the exact days, so err
// towards a WIDER bucket — too narrow silently drops posts the operator asked
// for. Allowed set is exactly {hour, today, week, month, year}.
func youTubeDateFilter(w port.KeywordTimeWindow) string {
	if w.From.IsZero() {
		return ""
	}
	switch d := time.Since(w.From); {
	case d <= 2*time.Hour:
		return "hour"
	case d <= 24*time.Hour:
		return "today"
	case d <= 8*24*time.Hour:
		return "week"
	case d <= 32*24*time.Hour:
		return "month"
	default:
		return "year"
	}
}

// linkedInSearchInput builds the payload for harvestapi/linkedin-post-search.
//
// maxPosts is the real per-query cap and MUST be sent: omitted, the actor uses
// its own default of 100 posts per query and bills all of them. That was
// measured — a 5-post cap sent as `maxItems` (a key this actor does not have)
// returned 97 rows and cost $0.096.
//
// postedLimitDate is a real absolute lower bound. postedLimit is a relative
// alternative and is not sent: sending both lets the actor pick, and the
// absolute date is the one that matches the window we actually hold.
func linkedInSearchInput(in port.ApifySearchInput, maxItems int) map[string]any {
	payload := map[string]any{
		"searchQueries": in.Keywords,
		"maxPosts":      maxItems,
		// Newest first: the window's lower bound is the filter that matters
		// for cost, and a relevance sort can fill the cap with old posts
		// that the ingestor then drops.
		"sortBy": "date",
	}
	if !in.Window.From.IsZero() {
		payload["postedLimitDate"] = in.Window.From.UTC().Format("2006-01-02")
	}
	return payload
}

// xSearchInput builds the payload for apidojo/twitter-scraper-lite.
//
// This actor has no date input and no result cap. `maxItems` is therefore NOT
// sent: it is not in the schema, so it would be ignored, and the run's cost is
// bounded by the ingestor only. sort:Latest is sent because it is the only
// ordering available and a relevance-ordered pool fills the earliest rows with
// old posts. The actor bills a per-QUERY event on top of the per-result fee,
// so the keyword list is the only thing the caller can shrink.
func xSearchInput(in port.ApifySearchInput, _ int) map[string]any {
	return map[string]any{
		"searchTerms": in.Keywords,
		"sort":        "Latest",
	}
}

// facebookSearchInput builds the payload for danek/facebook-search-ppr.
//
// search_type must be "posts": the actor also searches groups, people and
// events, and its default is not posts. max_posts is a real cap.
func facebookSearchInput(in port.ApifySearchInput, maxItems int) map[string]any {
	// This actor takes ONE query string, not an array, so the keyword list is
	// sent verbatim and the actor's own matching decides the pool.
	return map[string]any{
		"query":       strings.Join(in.Keywords, " "),
		"search_type": "posts",
		"max_posts":   maxItems,
	}
}

// tiktokSearchInput builds the payload for xmolodtsov/tiktok-search-scraper.
//
// Pay-per-event, billed per result returned, so maxItems is the cost ceiling —
// this actor honours it as a real cap, unlike the previous actor's
// resultsPerPage, which was a page size and did not bound what we were billed
// for. Everything the actor offers beyond the keyword search itself — the
// suggest/hashtag/music expansion pools — is billed too and is left off.
func tiktokSearchInput(in port.ApifySearchInput, maxItems int) map[string]any {
	return map[string]any{
		"keywords": in.Keywords,
		"maxItems": maxItems,
	}
}

// searchPostURL is one row of the Instagram keyword-search actor's output:
// `post_url` is the only field that carries data (user_name is usually empty).
type searchPostURL struct {
	PostURL string `json:"post_url"`
}

// enrichInstagramPosts turns the search actor's URL rows into full post rows by
// running the per-post actor in small batches (with per-URL fallback).
//
// Batching 10 URLs per actor call cuts enrichment time from ~8-10 minutes
// (sequential 1-by-1 actor startups) down to ~30-45 seconds and cuts actor
// startup overhead by 10x. If a chunk fails (e.g. a deleted/private URL),
// only that chunk falls back to per-URL calls so one bad URL never poisons
// the batch.
//
// Deduped by URL: the same post can appear under two keywords, and paying twice
// for one post is the exact waste this pipeline exists to avoid.
func (r *Runner) enrichInstagramPosts(ctx context.Context, rows []json.RawMessage) ([]json.RawMessage, error) {
	urls := searchPostURLs(rows)
	if len(urls) == 0 {
		return nil, nil
	}

	// actorFor is the same resolver the single-post path uses, so the search
	// enrichment runs the exact actor a permalink scrape would.
	actorID := strings.TrimSpace(r.actorFor(domain.PlatformInstagram))
	if actorID == "" {
		return nil, errors.New("no post actor configured to enrich search results")
	}

	out := make([]json.RawMessage, 0, len(urls))
	for start := 0; start < len(urls); start += enrichBatchSize {
		end := start + enrichBatchSize
		if end > len(urls) {
			end = len(urls)
		}
		chunk := urls[start:end]
		payload := map[string]any{
			"username":     chunk,
			"resultsLimit": len(chunk),
		}
		items, _, err := r.runSync(ctx, actorID, payload, len(chunk))
		if err == nil && len(items) > 0 {
			out = append(out, items...)
			continue
		}
		if err != nil {
			r.log("apify: enrich batch failed, falling back per-URL", "chunkSize", len(chunk), "err", err)
		}
		for _, u := range chunk {
			single, _, serr := r.runSync(ctx, actorID, runInput(domain.PlatformInstagram, u, 1), 1)
			if serr != nil {
				r.log("apify: enrich post failed", "url", u, "err", serr)
				continue
			}
			out = append(out, single...)
		}
	}
	r.log("apify: enriched search results", "urls", len(urls), "posts", len(out))
	return out, nil
}

// searchPostURLs pulls the deduped, query-stripped post URLs out of the search
// actor's rows.
//
// Deduped because the same post can rank under two keywords, and paying the
// post actor twice for one post is the waste this pipeline exists to avoid.
// Query strings are stripped so `?img_index=1` and the bare URL count as one.
// Rows without a URL are skipped, not errored: the actor emits a page-metadata
// row alongside its results, and that is not a failure.
func searchPostURLs(rows []json.RawMessage) []string {
	seen := make(map[string]struct{}, len(rows))
	urls := make([]string, 0, len(rows))
	for _, raw := range rows {
		var u searchPostURL
		if err := json.Unmarshal(raw, &u); err != nil {
			continue
		}
		clean := strings.TrimSpace(strings.Split(u.PostURL, "?")[0])
		if clean == "" {
			continue
		}
		if _, dup := seen[clean]; dup {
			continue
		}
		seen[clean] = struct{}{}
		urls = append(urls, clean)
	}
	return urls
}

// RunSearch drives the platform's search actor: keywords + window instead of
// one permalink. The payload shape is the union of what the IG/Threads search
// actors accept; a field the actor ignores is harmless, a field it needs and
// is present keeps the search honest. Items stream to the same storage under
// the audit run id.
func (r *Runner) RunSearch(ctx context.Context, in port.ApifySearchInput) (port.ApifyOutput, error) {
	actorID := strings.TrimSpace(in.ActorID)
	if actorID == "" {
		return port.ApifyOutput{}, fmt.Errorf("apify: no search actor for run %s", in.RunID)
	}
	maxItems := in.MaxPosts
	if maxItems <= 0 {
		maxItems = 50
	}
	// Resume vs start. A resumed run was already paid for and started by an
	// earlier process; building the payload and POSTing again would buy a
	// SECOND run, so the start half is skipped entirely.
	runID := strings.TrimSpace(in.ResumeRunID)
	if runID == "" {
		payload := r.searchInput(in, maxItems)
		if payload == nil {
			// searchInput returns nil for a platform it has no branch for, on
			// purpose: the old if-chain silently sent the Instagram payload to
			// anything missing from it, and an unknown key is answered 200 with the
			// actor's own default — invisible. Marshal it and the nil map becomes
			// the four bytes "null", the run is still POSTed and still billed, and the
			// actor answers with defaults. So refuse before spending anything.
			return port.ApifyOutput{}, fmt.Errorf("apify: no search input for platform %q", in.Platform)
		}
		// ASYNC run + poll, not the sync endpoint.
		//
		// The search actor drives a real browser and takes ~40-60s per keyword, so a
		// multi-keyword batch runs for minutes. The sync endpoint's whole request has
		// to fit inside one http.Client.Timeout, and the batch died with "context
		// deadline exceeded" while the run was still healthy on Apify — we paid for a
		// run we then threw away. Async start returns immediately; the poll below
		// owns the wait and its budget is sized for the actor.
		// Pass an explicit server-side timeout so a stalled actor cannot keep
		// burning Apify credits for an hour if the API restarts or polls time out.
		startURL := fmt.Sprintf("%s/acts/%s/runs?token=%s&timeout=%d",
			r.baseURL, pathSegment(actorID), r.token, searchActorTimeoutSec)
		body, err := json.Marshal(payload)
		if err != nil {
			return port.ApifyOutput{}, fmt.Errorf("apify: encode search input: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, startURL, bytes.NewReader(body))
		if err != nil {
			return port.ApifyOutput{}, fmt.Errorf("apify: build search run: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := r.client.Do(req)
		if err != nil {
			return port.ApifyOutput{}, fmt.Errorf("apify: search run request: %w", err)
		}
		defer resp.Body.Close()

		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		if err != nil {
			return port.ApifyOutput{}, fmt.Errorf("apify: read search run body: %w", err)
		}
		if resp.StatusCode >= 400 {
			return port.ApifyOutput{
				Status: statusFailed,
				Error:  fmt.Sprintf("apify http %d: %s", resp.StatusCode, truncate(string(raw), 500)),
			}, nil
		}
		var started struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &started); err != nil || started.Data.ID == "" {
			return port.ApifyOutput{
				Status: statusFailed,
				Error:  truncate(fmt.Sprintf("unexpected run body: %v", err), 500),
			}, nil
		}
		runID = started.Data.ID
	}
	if in.OnStarted != nil {
		in.OnStarted(runID)
	}
	r.log("apify: search run started", "run", runID, "keywords", in.Keywords, "resumed", in.ResumeRunID != "")

	items, runStatus, statusMessage, err := r.pollRunDatasetWithin(ctx, runID, searchPollBudget)
	if err != nil {
		return port.ApifyOutput{RunID: runID}, err
	}
	if runStatus != statusSucceeded {
		return port.ApifyOutput{
			RunID:  runID,
			Status: runStatus,
			Error:  truncate(failedRunError("search actor", runStatus, statusMessage), 500),
		}, nil
	}

	// TWO-STAGE for Instagram: the search actor returns post URLs only, so each
	// URL is enriched by the post actor for its caption and metrics. Skipping
	// this would store posts with no caption and no engagement — the dashboard
	// would show rows of blank text and zeros, which reads as a broken scrape.
	//
	// Per-URL and BEST-EFFORT: one post that 404s (deleted, private) must not
	// fail the batch. Falls back to the raw search rows when enrichment yields
	// nothing, so a broken post actor degrades to URL-only rows rather than an
	// empty batch.
	if in.Platform == domain.PlatformInstagram {
		if enriched, err := r.enrichInstagramPosts(ctx, items); err != nil {
			r.log("apify: post enrichment failed, storing search rows only", "err", err)
		} else if len(enriched) > 0 {
			items = enriched
		}
	}

	stored := make([]port.RawItem, 0, len(items))
	var total int64
	for i, item := range items {
		key := fmt.Sprintf("apify/search-%s/%d.json", in.RunID, i)
		n, err := r.storage.Put(ctx, key, item)
		if err != nil {
			r.log("apify: storage write failed", "key", key, "err", err)
			continue
		}
		total += n
		stored = append(stored, port.RawItem{Key: key, Bytes: n})
	}
	r.log("apify: search complete", "run", in.RunID, "keywords", in.Keywords, "items", len(items), "bytes", total)
	return port.ApifyOutput{
		RunID:  runID,
		Status: statusSucceeded,
		Items:  stored,
	}, nil
}

// Run starts the actor for the job and blocks until it terminates. A run that
// fails to *start* returns an error; a run that starts then fails returns an
// output with a terminal status so the caller records the attempt and applies
// backoff (that outcome is not a caller-visible error).
//
// The input payload is per-platform (runInput): the IG and Threads post-scraper
// actors take different field names for the same job. Threads also uses the
// async run+poll path (runSync) because the sync endpoint pins
// maxTotalChargeUsd=0.025, which the Threads actor aborts on before delivering
// anything.
func (r *Runner) Run(ctx context.Context, in port.ApifyInput) (port.ApifyOutput, error) {
	actorID := strings.TrimSpace(in.ActorID)
	if actorID == "" {
		return port.ApifyOutput{}, fmt.Errorf("apify: no actor for job %s", in.ScrapeJobID)
	}

	maxItems := maxItemsOr(in.MaxItems)

	// Threads' actor takes the post permalink directly (`action:"post"`). The
	// IG actor takes the permalink in its `username` field.
	if in.Platform == domain.PlatformThreads {
		return r.runThreadsPost(ctx, actorID, in.ScrapeJobID, in.TargetURL, maxItems)
	}

	items, runID, err := r.runSync(ctx, actorID, runInput(in.Platform, in.TargetURL, maxItems), maxItems)
	if err != nil {
		return port.ApifyOutput{}, err
	}
	return r.storeItems(ctx, in.ScrapeJobID, runID, items)
}

// runInput builds the actor payload for one post scrape, per platform.
//
// The zero-value fallback is startUrls in logical_scrapers' object form, which
// is what most Apify actors take. Each branch below is pinned against the
// actor's own live schema, because a wrong key here is SILENT: the actor falls
// back to its own default (often "scrape this profile's recent posts") and the
// paid run returns rows we then have to throw away.
//
//   - instagram-post-scraper: `username` accepts usernames, profile URLs AND
//     post URLs; a post URL yields exactly that post. (The old
//     startUrls/directUrls fields are gone from the current build.)
//   - youtube-scraper / twitter-scraper-lite: `startUrls[]` — the SAME
//     actors their keyword search uses, which accept one video/tweet URL and
//     return that one row. Verified by running each with a single URL.
//   - tiktok-realtime-post-details-scraper: its field is `url` (singular) as an
//     ARRAY of strings — not `urls`, and not objects. Sending `startUrls` here
//     returns the actor's default profile scrape.
func runInput(p domain.Platform, targetURL string, maxItems int) map[string]any {
	switch p {
	case domain.PlatformInstagram:
		return map[string]any{
			"username":     []string{targetURL},
			"resultsLimit": maxItems,
		}
	case domain.PlatformTikTok:
		return map[string]any{
			"url": []string{targetURL},
		}
	case domain.PlatformYouTube:
		// streamers/youtube-scraper: `startUrls` objects + `maxResults`. One
		// target URL is one video; maxResults:1 keeps a single row.
		return map[string]any{
			"startUrls":  []map[string]string{{"url": targetURL}},
			"maxResults": 1,
		}
	case domain.PlatformX:
		return map[string]any{
			"startUrls": []map[string]string{{"url": targetURL}},
			"maxItems":  maxItems,
		}
	}
	return map[string]any{
		"startUrls": []map[string]string{{"url": targetURL}},
	}
}

// threadsPostInput builds the per-post payload for themineworks/threads-scraper.
// Split out from runThreadsPost so runner_test can pin the field names against
// the actor's live input schema: a wrong name is SILENT here — the actor falls
// back to its own default and `includeReplies` quietly returns the post with no
// comments at all, which reads on the dashboard as "this post has no replies".
//
// `mode: post` selects the URL mode, `postUrls` carries the permalink, and
// `includeReplies: true` is what makes reply rows arrive at all (they are
// excluded by default and are the source of this platform's comments).
func threadsPostInput(targetURL string, maxItems int) map[string]any {
	return map[string]any{
		"mode":           "post",
		"postUrls":       []string{targetURL},
		"maxPosts":       maxItems,
		"includeReplies": true,
	}
}

// runThreadsPost scrapes one Threads post (and its replies) with
// themineworks/threads-scraper.
//
// This actor REPLACED the prefix-derived futurizerush one, which could not be
// addressed by a post URL at all: it only has a user-posts mode, so a per-post
// scrape had to run as "all recent posts by this user" and then match a
// shortcode — and `apify/meta-threads-scraper` (what APIFY_ACTOR_PREFIX=apify
// resolves to) 404s outright. It in turn replaced pro100chok/threads-scraper-
// usage, whose run-sync path aborted on the shared charge cap.
//
// `includeReplies: true` is load-bearing, not a preference: this actor emits
// each reply as its OWN top-level dataset row (flagged `is_reply` and carrying
// `parent_post_id`), and it excludes them by default. Leaving it off would make
// every Threads "Fetch comments" click ingest one post and zero comments — the
// exact dead end the on-demand comment path exists to avoid. Replies arrive
// ordered after their parent, which the ingestor requires so the parent row
// exists before a reply resolves it.
//
// It runs through the async API rather than run-sync-get-dataset-items: the
// shared session pool plus pagination regularly exceeds the sync endpoint's
// charge cap, which aborts the run before any item is delivered.
func (r *Runner) runThreadsPost(ctx context.Context, actorID, scrapeJobID, targetURL string, maxItems int) (port.ApifyOutput, error) {
	if strings.TrimSpace(targetURL) == "" {
		return port.ApifyOutput{}, fmt.Errorf("apify: threads scrape needs a post url")
	}
	payload := threadsPostInput(targetURL, maxItems)
	body, err := json.Marshal(payload)
	if err != nil {
		return port.ApifyOutput{}, fmt.Errorf("apify: encode threads input: %w", err)
	}

	startURL := fmt.Sprintf("%s/acts/%s/runs?token=%s&maxTotalChargeUsd=%s",
		r.baseURL, pathSegment(actorID), r.token, threadsChargeCap)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, startURL, bytes.NewReader(body))
	if err != nil {
		return port.ApifyOutput{}, fmt.Errorf("apify: build threads run: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return port.ApifyOutput{}, fmt.Errorf("apify: threads run request: %w", err)
	}
	defer resp.Body.Close()
	startBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return port.ApifyOutput{}, fmt.Errorf("apify: read threads run body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return port.ApifyOutput{
			Status: statusFailed,
			Error:  fmt.Sprintf("apify http %d: %s", resp.StatusCode, truncate(string(startBody), 500)),
		}, nil
	}
	var started struct {
		Data struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(startBody, &started); err != nil || started.Data.ID == "" {
		return port.ApifyOutput{
			Status: statusFailed,
			// %v, not %s: this branch is also reached when the body parses but
			// carries no data.id, and then err is nil. %s rendered that as the
			// literal "unexpected run body: %!s(<nil>)" — an error message that
			// names neither the cause nor the missing id.
			Error: truncate(fmt.Sprintf("unexpected run body: %v", err), 500),
		}, nil
	}
	runID := started.Data.ID
	r.log("apify: threads run started", "run", runID, "url", targetURL)

	items, runStatus, statusMessage, err := r.pollRunDataset(ctx, runID)
	if err != nil {
		return port.ApifyOutput{RunID: runID}, err
	}
	if runStatus != statusSucceeded {
		return port.ApifyOutput{
			RunID:  runID,
			Status: runStatus,
			Error:  truncate(failedRunError("threads actor", runStatus, statusMessage), 500),
		}, nil
	}
	// Namespace the stored items by the caller's job id, like the sync path
	// does. The old user-posts mode derived a key from the @username, which
	// collided across concurrent scrapes of the same account.
	return r.storeItems(ctx, scrapeJobID, runID, items)
}

// pollRunDataset waits for the actor run to reach a terminal status, then
// downloads its dataset items. The poll window is the run budget: the sync
// endpoint blocks for exactly this long in the normal path anyway.

// failedRunError renders the error for a run that reached a terminal
// non-success status. Apify's statusMessage carries the actor's OWN reason for
// the failure ("geo: all 1 keywords returned zero results — blocked or
// errored"), and dropping it left operators with a bare "actor FAILED" and no
// way to tell a geo-block from a crash without opening the Apify console.
func failedRunError(label, status, statusMessage string) string {
	msg := strings.TrimSpace(statusMessage)
	if msg == "" {
		return label + " run: " + status
	}
	return label + " run " + status + ": " + msg
}

// pollRunDataset waits up to pollBudget for a run to reach a terminal status.
// statusMessage is Apify's own reason for a failed run; it is empty on success.
func (r *Runner) pollRunDataset(ctx context.Context, runID string) ([]json.RawMessage, string, string, error) {
	return r.pollRunDatasetWithin(ctx, runID, pollBudget)
}

// pollRunDatasetWithin is pollRunDataset with an explicit budget.
//
// The budget is a parameter because the actors differ by an order of magnitude:
// the Threads scrape finishes in well under a minute, while the Instagram
// keyword search drives a browser and takes ~40-60s PER KEYWORD. One shared
// constant either cuts the slow actor off (the batch died at 5 minutes while the
// run was still healthy) or makes a fast failure take a quarter hour to surface.
func (r *Runner) pollRunDatasetWithin(ctx context.Context, runID string, budget time.Duration) ([]json.RawMessage, string, string, error) {
	deadline := time.Now().Add(budget)
	var (
		lastMsg        string
		lastDatasetID  string
		lastProgressAt = time.Now()
	)
	for {
		if err := ctx.Err(); err != nil {
			return nil, "", "", fmt.Errorf("apify: poll canceled: %w", err)
		}
		if time.Now().After(deadline) {
			r.abortRun(ctx, runID)
			if items := r.tryHarvestPartial(ctx, runID, lastDatasetID, "poll budget exceeded"); len(items) > 0 {
				return items, statusSucceeded, "", nil
			}
			return nil, "", "", fmt.Errorf("apify: run %s did not finish in %s", runID, budget)
		}
		time.Sleep(pollInterval)

		url := fmt.Sprintf("%s/actor-runs/%s?token=%s", r.baseURL, runID, r.token)
		resp, err := r.client.Get(url)
		if err != nil {
			return nil, "", "", fmt.Errorf("apify: run status: %w", err)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		var st struct {
			Data struct {
				Status        string `json:"status"`
				StatusMessage string `json:"statusMessage"`
				DatasetID     string `json:"defaultDatasetId"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &st); err != nil {
			return nil, "", "", fmt.Errorf("apify: run status body: %w", err)
		}
		if st.Data.DatasetID != "" {
			lastDatasetID = st.Data.DatasetID
		}
		switch st.Data.Status {
		case statusSucceeded:
			items, err := r.fetchDataset(ctx, st.Data.DatasetID)
			if err != nil {
				return nil, "", "", err
			}
			return items, statusSucceeded, "", nil
		case "TIMED-OUT", "ABORTED":
			// An actor that timed out or was aborted after producing partial
			// results still has those items in its dataset — harvest them
			// instead of throwing away a paid run.
			if items := r.tryHarvestPartial(ctx, runID, st.Data.DatasetID, st.Data.Status); len(items) > 0 {
				return items, statusSucceeded, "", nil
			}
			return nil, st.Data.Status, st.Data.StatusMessage, nil
		case "FAILED":
			return nil, st.Data.Status, st.Data.StatusMessage, nil
		default:
			// Stall detection: if the actor has already emitted a progress
			// message (e.g. "19 result generated so far..") and stops advancing
			// for `searchStallTimeout`, abort it gracefully and harvest the
			// results it already wrote instead of waiting out the full timeout.
			msg := strings.TrimSpace(st.Data.StatusMessage)
			if msg != lastMsg {
				lastMsg = msg
				lastProgressAt = time.Now()
			} else if msg != "" && time.Since(lastProgressAt) >= searchStallTimeout {
				r.log("apify: run stalled with no progress; aborting to harvest partial dataset",
					"run", runID, "statusMessage", msg, "stalledFor", time.Since(lastProgressAt).Round(time.Second))
				r.abortRun(ctx, runID)
				if items := r.tryHarvestPartial(ctx, runID, lastDatasetID, "stalled"); len(items) > 0 {
					return items, statusSucceeded, "", nil
				}
				return nil, "TIMED-OUT", fmt.Sprintf("stalled: %s", msg), nil
			}
		}
	}
}

// abortRun asks Apify to stop a stalled or timed-out run so it stops billing.
func (r *Runner) abortRun(ctx context.Context, runID string) {
	url := fmt.Sprintf("%s/actor-runs/%s/abort?token=%s", r.baseURL, runID, r.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return
	}
	resp, err := r.client.Do(req)
	if err != nil {
		r.log("apify: abort run failed", "run", runID, "err", err)
		return
	}
	resp.Body.Close()
}

// tryHarvestPartial fetches whatever items an aborted/timed-out run already
// wrote to its dataset. Returns nil when the dataset is empty or unreadable.
func (r *Runner) tryHarvestPartial(ctx context.Context, runID, datasetID, reason string) []json.RawMessage {
	if datasetID == "" {
		return nil
	}
	items, err := r.fetchDataset(ctx, datasetID)
	if err != nil || len(items) == 0 {
		return nil
	}
	r.log("apify: harvested partial dataset", "run", runID, "reason", reason, "items", len(items))
	return items
}

// fetchDataset downloads every item of a finished run.
func (r *Runner) fetchDataset(ctx context.Context, datasetID string) ([]json.RawMessage, error) {
	url := fmt.Sprintf("%s/datasets/%s/items?token=%s&clean=true", r.baseURL, datasetID, r.token)
	resp, err := r.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("apify: dataset fetch: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("apify: read dataset body: %w", err)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("apify: parse dataset body: %w", err)
	}
	return items, nil
}

func (r *Runner) storeItems(ctx context.Context, jobKey, runID string, items []json.RawMessage) (port.ApifyOutput, error) {
	stored := make([]port.RawItem, 0, len(items))
	var total int64
	for i, item := range items {
		key := fmt.Sprintf("apify/%s/%d.json", jobKey, i)
		n, err := r.storage.Put(ctx, key, item)
		if err != nil {
			r.log("apify: storage write failed", "key", key, "err", err)
			continue
		}
		total += n
		stored = append(stored, port.RawItem{Key: key, Bytes: n})
	}
	r.log("apify: run complete", "job", jobKey, "items", len(items), "bytes", total)
	return port.ApifyOutput{
		RunID:  runID,
		Status: statusSucceeded,
		Items:  stored,
	}, nil
}

// syncMaxItemsFloor is the smallest `maxItems` the sync endpoint tolerates.
//
// The cap is a CHARGE cap on a pay-per-event actor, and Apify rejects a run
// whose ceiling is below $0.005 ("max-total-charge-usd-below-minimum"). At the
// post actor's price a maxItems of 1 rounds under that, so enriching a single
// search result failed with a 400 — the batch then found 15 URLs and stored 0
// posts. Requesting a few covers one run's worth with room to spare; the actor
// still returns only what it can for the payload it was given.
const syncMaxItemsFloor = 10

// runSync posts the payload to run-sync-get-dataset-items and returns the
// dataset items inline.
func (r *Runner) runSync(ctx context.Context, actorID string, payload map[string]any, maxItems int) ([]json.RawMessage, string, error) {
	if maxItems < syncMaxItemsFloor {
		maxItems = syncMaxItemsFloor
	}
	url := fmt.Sprintf("%s/acts/%s/run-sync-get-dataset-items?token=%s&maxItems=%d",
		r.baseURL, pathSegment(actorID), r.token, maxItems)
	body, err := json.Marshal(payload)
	if err != nil {
		// An unchecked error here would send an empty body, the actor would
		// answer 200 with its own defaults, and the scrape would quietly return
		// the wrong thing — the exact shape this package's comments keep
		// warning about.
		return nil, "", fmt.Errorf("apify: encode payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("apify: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("apify: run request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, "", fmt.Errorf("apify: read run body: %w", err)
	}
	runID := resp.Header.Get("x-apify-run-id")

	if resp.StatusCode >= 400 {
		return nil, runID, &RunError{
			RunID: runID, Status: statusFailed,
			Message: fmt.Sprintf("apify http %d: %s", resp.StatusCode, truncate(string(raw), 500)),
		}
	}

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, runID, &RunError{
			RunID: runID, Status: statusFailed,
			Message: truncate(fmt.Sprintf("unexpected body: %s", err), 500),
		}
	}
	return items, runID, nil
}

// RunError is a run that started but failed: callers record it as a terminal
// attempt (not a transport error). It satisfies error so the port stays simple;
// the service unwraps it via As to keep the job bookkeeping honest.
type RunError struct {
	RunID   string
	Status  string
	Message string
}

func (e *RunError) Error() string { return e.Message }

// pathSegment turns an actor id into the single URL path segment Apify's API
// expects: the store id "user/name" must travel as "user~name" (a literal "/"
// makes Apify read "acts/user" as the actor and 404 on the rest). Ids already
// written with "~" pass through unchanged.
func pathSegment(actorID string) string {
	return strings.ReplaceAll(strings.Trim(actorID, "/"), "/", "~")
}

func maxItemsOr(n int) int {
	if n <= 0 || n > defaultMaxItems {
		return defaultMaxItems
	}
	return n
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

const (
	defaultMaxItems = 100
	maxBodyBytes    = 32 << 20 // 32 MiB cap on one run body.
	statusSucceeded = "SUCCEEDED"
	statusFailed    = "FAILED"

	// The Threads actor aborts when the run's charge cap is below its per-item
	// price, and run-sync pins that cap at $0.025. The async API lets the
	// caller raise it, so the Threads path runs async and polls.
	threadsChargeCap = "1.00"

	// Async-run polling budget for a Threads user scrape.
	pollBudget   = 4 * time.Minute
	pollInterval = 5 * time.Second

	// Batch size for Instagram Stage-2 post enrichment (apify/instagram-post-scraper).
	enrichBatchSize = 10

	// Hard server-side timeout passed to Apify on async search runs (6 minutes)
	// and stall threshold when a running actor stops making progress (3 minutes).
	searchActorTimeoutSec = 360
	searchStallTimeout    = 3 * time.Minute
	searchPollBudget      = 7 * time.Minute
)
