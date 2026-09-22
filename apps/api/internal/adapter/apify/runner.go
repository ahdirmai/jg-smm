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
	url := fmt.Sprintf("%s/acts/%s/run-sync-get-dataset-items?token=%s&maxItems=%d",
		r.baseURL, pathSegment(actorID), r.token, maxItems)

	searchTerms := make([]string, len(in.Keywords))
	copy(searchTerms, in.Keywords)
	payload := map[string]any{
		"searchTerms": searchTerms,
		"maxItems":    maxItems,
		"proxyGroups": []string{"RESIDENTIAL"},
	}
	// The window fields the actors read; actors without date filtering ignore
	// them. RFC3339 dates, zero times omitted.
	if !in.Window.From.IsZero() {
		payload["since"] = in.Window.From.UTC().Format("2006-01-02")
	}
	if !in.Window.To.IsZero() {
		payload["until"] = in.Window.To.UTC().Format("2006-01-02")
	}

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
	r.log("apify: search complete", "run", in.RunID, "keywords", searchTerms, "items", len(items), "bytes", total)
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

	// Threads' actor has no single-post mode: it scrapes a user's recent posts,
	// and the on-demand service matches the target's shortcode among them. The
	// IG actor takes the permalink directly.
	if in.Platform == domain.PlatformThreads {
		return r.runThreadsUserPosts(ctx, actorID, in.TargetURL, maxItems)
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

// runThreadsUserPosts scrapes a Threads user's recent posts and filters them
// to the target's post code. The futurizerush/meta-threads-scraper actor has
// no direct-post-URL mode; its run must go through the async API (the sync
// endpoint's charge cap aborts it), so this helper drives run+poll.
func (r *Runner) runThreadsUserPosts(ctx context.Context, actorID, targetURL string, maxItems int) (port.ApifyOutput, error) {
	user := threadsUserFromURL(targetURL)
	if user == "" {
		return port.ApifyOutput{}, fmt.Errorf("apify: cannot resolve threads username from %q", targetURL)
	}
	payload := map[string]any{
		"mode":      "user",
		"usernames": []string{user},
		"max_posts": maxItems,
	}
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
	r.log("apify: threads run started", "run", runID, "user", user)

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
	return r.storeItems(ctx, threadsJobKey(targetURL, user), runID, items)
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
				Status   string `json:"status"`
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

// threadsJobKey namespaces the storage keys for a threads user scrape (the
// caller passes the on-demand job id; the scheduler path passes its own id).
func threadsJobKey(targetURL, user string) string {
	if i := strings.Index(targetURL, "/post/"); i > 0 {
		if s := strings.LastIndex(targetURL[:i], "/@"); s >= 0 && s+2 < i {
			return strings.Trim(targetURL[s+2:i], "/")
		}
	}
	return "threads-" + user
}

// threadsUserFromURL extracts the @username from a Threads post permalink
// (https://www.threads.com/@user/post/ID or threads.net variant).
func threadsUserFromURL(raw string) string {
	u := raw
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	seg := strings.Split(u, "/")
	for _, s := range seg {
		if strings.HasPrefix(s, "@") && len(s) > 1 {
			return strings.TrimPrefix(s, "@")
		}
	}
	return ""
}

// storeItems persists dataset items into object storage and returns the output.
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

// pathSegment keeps an actor id ("~user/name") a single path segment by
// trimming surrounding slashes; Apify accepts the tilde literally.
func pathSegment(actorID string) string { return strings.Trim(actorID, "/") }

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
