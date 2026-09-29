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
func (r *Runner) searchInput(in port.ApifySearchInput, maxItems int) map[string]any {
	if in.Platform == domain.PlatformTikTok {
		return tiktokSearchInput(in, maxItems)
	}
	if in.Platform == domain.PlatformThreads {
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

	// No date keys: the actor's schema has no date field, so sending
	// onlyPostsNewerThan (the old apify/instagram-scraper spelling) would be
	// ignored — and an ignored lower bound means the ingestor has to drop
	// out-of-window posts it already paid to fetch.
	return map[string]any{
		"keywords": in.Keywords,
		"maxPosts": maxItems,
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
	// No maxItems query param: on a pay-per-result actor it caps the run's
	// charge, and too low a cap ABORTS the run ("max charge limit was too low to
	// deliver any posts") before the payload is even read — which is what the
	// Threads search actor did. The payload bounds the result count itself
	// (resultsLimit / max_posts), so the cap adds nothing here.
	url := fmt.Sprintf("%s/acts/%s/run-sync-get-dataset-items?token=%s",
		r.baseURL, pathSegment(actorID), r.token)

	payload := r.searchInput(in, maxItems)

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return port.ApifyOutput{}, fmt.Errorf("apify: build search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return port.ApifyOutput{}, fmt.Errorf("apify: search request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return port.ApifyOutput{}, fmt.Errorf("apify: read search body: %w", err)
	}
	runID := resp.Header.Get("x-apify-run-id")
	if resp.StatusCode >= 400 {
		return port.ApifyOutput{
			RunID:  runID,
			Status: statusFailed,
			Error:  fmt.Sprintf("apify http %d: %s", resp.StatusCode, truncate(string(raw), 500)),
		}, nil
	}

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return port.ApifyOutput{
			RunID:  runID,
			Status: statusFailed,
			Error:  truncate(fmt.Sprintf("unexpected body: %s", err), 500),
		}, nil
	}

	keys := make([]string, 0, len(items))
	var total int64
	for i, item := range items {
		key := fmt.Sprintf("apify/search-%s/%d.json", in.RunID, i)
		n, err := r.storage.Put(ctx, key, item)
		if err != nil {
			r.log("apify: storage write failed", "key", key, "err", err)
			continue
		}
		total += n
		keys = append(keys, key)
	}
	r.log("apify: search complete", "run", in.RunID, "keywords", in.Keywords, "items", len(items), "bytes", total)
	return port.ApifyOutput{
		RunID:    runID,
		Status:   statusSucceeded,
		ItemKeys: keys,
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
//   - instagram-post-scraper: `username` accepts usernames, profile URLs AND
//     post URLs; a post URL yields exactly that post. (The old
//     startUrls/directUrls fields are gone from the current build.)
//   - Other platforms fall back to startUrls (logical_scrapers' object form).
func runInput(p domain.Platform, targetURL string, maxItems int) map[string]any {
	if p == domain.PlatformInstagram {
		return map[string]any{
			"username":     []string{targetURL},
			"resultsLimit": maxItems,
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
	body, _ := json.Marshal(payload)

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
			Error:  truncate(fmt.Sprintf("unexpected run body: %s", err), 500),
		}, nil
	}
	runID := started.Data.ID
	r.log("apify: threads run started", "run", runID, "url", targetURL)

	items, runStatus, err := r.pollRunDataset(ctx, runID)
	if err != nil {
		return port.ApifyOutput{RunID: runID}, err
	}
	if runStatus != statusSucceeded {
		return port.ApifyOutput{
			RunID:  runID,
			Status: runStatus,
			Error:  truncate("threads actor run: "+runStatus, 500),
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
func (r *Runner) pollRunDataset(ctx context.Context, runID string) ([]json.RawMessage, string, error) {
	deadline := time.Now().Add(pollBudget)
	for {
		if err := ctx.Err(); err != nil {
			return nil, "", fmt.Errorf("apify: poll canceled: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, "", fmt.Errorf("apify: run %s did not finish in %s", runID, pollBudget)
		}
		time.Sleep(pollInterval)

		url := fmt.Sprintf("%s/actor-runs/%s?token=%s", r.baseURL, runID, r.token)
		resp, err := r.client.Get(url)
		if err != nil {
			return nil, "", fmt.Errorf("apify: run status: %w", err)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		var st struct {
			Data struct {
				Status    string `json:"status"`
				DatasetID string `json:"defaultDatasetId"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &st); err != nil {
			return nil, "", fmt.Errorf("apify: run status body: %w", err)
		}
		switch st.Data.Status {
		case statusSucceeded:
			items, err := r.fetchDataset(ctx, st.Data.DatasetID)
			if err != nil {
				return nil, "", err
			}
			return items, statusSucceeded, nil
		case "FAILED", "ABORTED", "TIMED-OUT":
			return nil, st.Data.Status, nil
		}
	}
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
	keys := make([]string, 0, len(items))
	var total int64
	for i, item := range items {
		key := fmt.Sprintf("apify/%s/%d.json", jobKey, i)
		n, err := r.storage.Put(ctx, key, item)
		if err != nil {
			r.log("apify: storage write failed", "key", key, "err", err)
			continue
		}
		total += n
		keys = append(keys, key)
	}
	r.log("apify: run complete", "job", jobKey, "items", len(items), "bytes", total)
	return port.ApifyOutput{
		RunID:    runID,
		Status:   statusSucceeded,
		ItemKeys: keys,
	}, nil
}

// runSync posts the payload to run-sync-get-dataset-items and returns the
// dataset items inline.
func (r *Runner) runSync(ctx context.Context, actorID string, payload map[string]any, maxItems int) ([]json.RawMessage, string, error) {
	url := fmt.Sprintf("%s/acts/%s/run-sync-get-dataset-items?token=%s&maxItems=%d",
		r.baseURL, pathSegment(actorID), r.token, maxItems)
	body, _ := json.Marshal(payload)
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
)
