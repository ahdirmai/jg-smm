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

// Run starts the actor for the job and blocks until it terminates. A run that
// fails to *start* returns an error; a run that starts then fails returns an
// output with a terminal status so the caller records the attempt and applies
// backoff (that outcome is not a caller-visible error).
func (r *Runner) Run(ctx context.Context, in port.ApifyInput) (port.ApifyOutput, error) {
	actorID := strings.TrimSpace(in.ActorID)
	if actorID == "" {
		return port.ApifyOutput{}, fmt.Errorf("apify: no actor for job %s", in.ScrapeJobID)
	}

	maxItems := maxItemsOr(in.MaxItems)
	url := fmt.Sprintf("%s/acts/%s/run-sync-get-dataset-items?token=%s&maxItems=%d",
		r.baseURL, pathSegment(actorID), r.token, maxItems)

	body, _ := json.Marshal(map[string]any{
		"startUrls":   []string{in.TargetURL},
		"maxItems":    maxItems,
		"proxyGroups": []string{"RESIDENTIAL"},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return port.ApifyOutput{}, fmt.Errorf("apify: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return port.ApifyOutput{}, fmt.Errorf("apify: run request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return port.ApifyOutput{}, fmt.Errorf("apify: read run body: %w", err)
	}
	runID := resp.Header.Get("x-apify-run-id")

	if resp.StatusCode >= 400 {
		return port.ApifyOutput{
			RunID:  runID,
			Status: statusFailed,
			Error:  fmt.Sprintf("apify http %d: %s", resp.StatusCode, truncate(string(raw), 500)),
		}, nil
	}

	// The sync endpoint returns the dataset items inline. Persist each item to
	// object storage and hand back the keys — the DB stores keys only.
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
		key := fmt.Sprintf("apify/%s/%d.json", in.ScrapeJobID, i)
		n, err := r.storage.Put(ctx, key, item)
		if err != nil {
			r.log("apify: storage write failed", "key", key, "err", err)
			continue
		}
		total += n
		keys = append(keys, key)
	}

	r.log("apify: run complete", "job", in.ScrapeJobID, "items", len(items), "bytes", total)
	return port.ApifyOutput{
		RunID:    runID,
		Status:   statusSucceeded,
		ItemKeys: keys,
	}, nil
}

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
)
