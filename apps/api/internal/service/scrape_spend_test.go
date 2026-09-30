package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// --- M-7 ---------------------------------------------------------------------
//
// apify_run is the audit table. Its rows used to be closed only on the
// SUCCEEDED path: UpdateApifyRun sat after the `out.Status != "SUCCEEDED"` check
// in runOnce, so a transport error, an HTTP 4xx/5xx (the runner reports those
// as Status: FAILED with a nil error) and an ingest error each left the row
// RUNNING with finished_at NULL. The table then reported the opposite of what
// happened, and attempt 1's run was never closed by the retry.
//
// The fake store had to change for any of this to be observable: its
// UpdateApifyRun returned the argument unchanged and recorded nothing, so no
// test could tell a closed run from an open one.

// openRuns returns the apify_run rows still marked RUNNING or without a
// finished_at — i.e. the ones the audit table would report as in flight.
func openRuns(t *testing.T, store *fakeScrapeStore) []domain.ApifyRun {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	out := make([]domain.ApifyRun, 0, len(store.runs))
	for _, r := range store.runs {
		if r.Status == "RUNNING" || r.FinishedAt == nil {
			out = append(out, r)
		}
	}
	return out
}

// A FAILED actor run is reported by the runner as Status FAILED with a nil
// error, so the service's failure branch is the status check, not the error.

// rawItems lifts a test's S3-key fixture into the runner's output shape. Sizes
// are left at 0 unless a test is asserting on raw_payload.bytes.
func rawItems(keys []string) []port.RawItem {
	out := make([]port.RawItem, 0, len(keys))
	for _, k := range keys {
		out = append(out, port.RawItem{Key: k})
	}
	return out
}

func rawItem(key string) []port.RawItem { return rawItems([]string{key}) }

func TestKeywordFailedActorRunClosesApifyRun(t *testing.T) {
	store := newFakeScrapeStore()
	runner := &fakeRunner{out: port.ApifyOutput{Status: "FAILED", Error: "actor blew up"}}
	svc := NewKeywordScrapeService(store, runner,
		func(domain.Platform) string { return "apify/instagram-scraper" },
		&fakeRawStorage{items: map[string][]byte{}}, nil)
	svc.SetBatchStore(newFakeKeywordBatchStore())

	if _, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{"kamu"},
	}); err == nil {
		t.Fatal("an actor FAILED run must be an error")
	}

	if got := openRuns(t, store); len(got) != 0 {
		t.Fatalf("every apify_run must be closed after a FAILED actor run, %d still open: %+v", len(got), got)
	}
}

// A transport error returns before the status is ever read — the path that
// most obviously skipped the close.
func TestKeywordTransportErrorClosesApifyRun(t *testing.T) {
	store := newFakeScrapeStore()
	runner := &fakeRunner{outErr: errors.New("dial tcp: connection refused (redacted)")}
	svc := NewKeywordScrapeService(store, runner,
		func(domain.Platform) string { return "apify/instagram-scraper" },
		&fakeRawStorage{items: map[string][]byte{}}, nil)
	svc.SetBatchStore(newFakeKeywordBatchStore())

	if _, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{"kamu"},
	}); err == nil {
		t.Fatal("a transport error must be an error")
	}

	if got := openRuns(t, store); len(got) != 0 {
		t.Fatalf("every apify_run must be closed after a transport error, %d still open: %+v", len(got), got)
	}
}

// The ingest-error path: the actor SUCCEEDED and its keys were written, so the
// run row exists and is not FAILED — but the batch died at the ingest step and
// the row must not be left RUNNING just because the failure happened one step
// later. A cancelled context makes IngestRun return early, which is exactly a
// batch goroutine cancelled mid-flight.
func TestKeywordIngestErrorClosesApifyRun(t *testing.T) {
	store := newFakeScrapeStore()
	runner := &fakeRunner{out: port.ApifyOutput{Status: "SUCCEEDED", RunID: "r1", Items: []port.RawItem{{Key: "run/page-0.json"}}}}
	svc := NewKeywordScrapeService(store, runner,
		func(domain.Platform) string { return "apify/instagram-scraper" },
		&fakeRawStorage{items: map[string][]byte{}}, nil)
	batches := newFakeKeywordBatchStore()
	svc.SetBatchStore(batches)

	// Drive runOnce directly with an already-cancelled context: it is the unit
	// that owns the close, and CreateBatch deliberately detaches the batch
	// goroutine's context so a request cancel cannot abort a paid run.
	run, err := store.CreateApifyRun(context.Background(), domain.ApifyRun{ActorID: "apify/instagram-scraper", Status: "RUNNING"})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := svc.runOnce(ctx, run.ID, "apify/instagram-scraper", domain.PlatformInstagram, []string{"kamu"}, KeywordWindow{}, 50); err == nil {
		t.Fatal("an ingest on a cancelled context must be an error")
	}

	if got := openRuns(t, store); len(got) != 0 {
		t.Fatalf("every apify_run must be closed after an ingest error, %d still open: %+v", len(got), got)
	}
}

// --- M-8 ---------------------------------------------------------------------
//
// The ingest-error branch returned without calling s.fail, and s.fail itself
// used the request context — already cancelled in exactly the case it exists
// for. Operator closes the tab mid-fetch: the run is paid and still executing,
// ctx cancels, and the job row stays `running` permanently.

// closeRecordingStore records the context liveness each CompleteScrapeJob saw,
// so a test can prove the write is not riding on a cancelled request context.
type closeRecordingStore struct {
	*fakeScrapeStore
	ctxErrs []error
}

func (s *closeRecordingStore) CompleteScrapeJob(ctx context.Context, id string, status domain.JobStatus, err *string) (domain.ScrapeJob, error) {
	s.ctxErrs = append(s.ctxErrs, ctx.Err())
	return s.fakeScrapeStore.CompleteScrapeJob(ctx, id, status, err)
}

// A cancelled request must not stop the job row from being closed. Before the
// fix the write carried the request context, so on this path it failed with
// context.Canceled and the row stayed `running` forever — the target was never
// linked and the next scrape of the URL paid again.
func TestScrapeTargetFailSurvivesACancelledRequest(t *testing.T) {
	base := newFakeScrapeStore()
	store := &closeRecordingStore{fakeScrapeStore: base}
	runner := &fakeRunner{out: port.ApifyOutput{Status: "SUCCEEDED", RunID: "r1", Items: []port.RawItem{{Key: "run/page-0.json"}}}}
	svc := NewScrapeService(store, runner, actorForAny, &fakeRawStorage{items: map[string][]byte{}}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := svc.ScrapeTarget(ctx, domain.PlatformInstagram, "https://www.instagram.com/p/Ccancel/", true); err == nil {
		t.Fatal("a cancelled request must surface an error to the caller")
	}

	// Every job row the service touched must be terminal, never left running.
	jobs, err := store.ListScrapeJobs(ctx, nil, nil)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want exactly one scrape_job row, got %d", len(jobs))
	}
	if jobs[0].Status == domain.JobStatusRunning {
		t.Fatalf("scrape_job left running after a cancelled request (status %q)", jobs[0].Status)
	}
	// The write must have seen a live context despite the cancellation.
	sawCancelled := false
	for _, e := range store.ctxErrs {
		if errors.Is(e, context.Canceled) {
			sawCancelled = true
		}
	}
	if len(store.ctxErrs) == 0 {
		t.Fatal("fail never reached CompleteScrapeJob")
	}
	if sawCancelled {
		t.Fatal("the job write rode on the cancelled request context; a real store would reject it")
	}
}

// --- M-9 ---------------------------------------------------------------------
//
// storeItems returns SUCCEEDED with an empty item list; the IngestRun result was
// discarded, so readBack resolved tgt.PostID — the PRIOR scrape's link — and
// returned the old row. A forced re-scrape of a deleted post answered 200 with
// job `success` and the operator's panel showed the old comments as if the
// fetch had worked.

// Zero-item run on a post that already has a stored row from a previous
// scrape. The response must not present that old row as fresh evidence.
func TestScrapeTargetZeroItemRunDoesNotReturnStaleRow(t *testing.T) {
	store := newFakeScrapeStore()
	// The prior scrape: a post row already linked to the target.
	tgt, err := store.UpsertTarget(context.Background(), domain.Target{
		Kind: domain.TargetKindPost, Platform: domain.PlatformInstagram,
		ExternalID: "https://www.instagram.com/p/Cstale/", URL: "https://www.instagram.com/p/Cstale/",
	})
	if err != nil {
		t.Fatalf("upsert target: %v", err)
	}
	post, err := store.UpsertPost(context.Background(), domain.Post{
		Platform: domain.PlatformInstagram, ExternalID: "Cstale", AuthorHandle: "old",
		ScrapedAt: time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("upsert post: %v", err)
	}
	if err := store.LinkTargetPost(context.Background(), tgt.ID, post.ID); err != nil {
		t.Fatalf("link target: %v", err)
	}
	if _, err := store.UpsertComment(context.Background(), domain.Comment{
		PostID: post.ID, Platform: domain.PlatformInstagram, ExternalID: "cmt-old", Text: "old comment",
	}); err != nil {
		t.Fatalf("upsert comment: %v", err)
	}

	// The actor succeeds but delivers nothing: no dataset keys, so ItemsRead 0.
	runner := &fakeRunner{out: port.ApifyOutput{Status: "SUCCEEDED", RunID: "r1"}}
	svc := NewScrapeService(store, runner, actorForAny, &fakeRawStorage{items: map[string][]byte{}}, nil)

	detail, err := svc.ScrapeTarget(context.Background(), domain.PlatformInstagram, "https://www.instagram.com/p/Cstale/", true)
	if err == nil {
		t.Fatalf("a zero-item run must not be reported as a fresh scrape; got %d comments for %s",
			len(detail.Comments), detail.Post.ExternalID)
	}
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("want ErrValidation (the read-back-miss shape), got %v", err)
	}

	// The job must not be reported as a success either.
	jobs, err := store.ListScrapeJobs(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Status != domain.JobStatusFailed {
		t.Fatalf("the zero-item job must be FAILED, got %+v", jobs)
	}
}

// ingestingRunner returns a runner whose SUCCEEDED run carries one real IG post
// row, so a scrape reaches `success` instead of the zero-item guard.
func ingestingRunner(t *testing.T, code string) (*fakeRunner, *fakeRawStorage) {
	t.Helper()
	storage := &fakeRawStorage{items: map[string][]byte{}}
	key := "run/" + code + ".json"
	body, err := json.Marshal(mkIGPostRow(code, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)))
	if err != nil {
		t.Fatalf("marshal post row: %v", err)
	}
	storage.items[key] = body
	return &fakeRunner{out: port.ApifyOutput{Status: "SUCCEEDED", RunID: "r1",
		Items: []port.RawItem{{Key: key, Bytes: int64(len(body))}}}}, storage
}

// --- M-10 --------------------------------------------------------------------
//
// Neither endpoint that spends money had a limiter. `force` is a
// caller-controlled boolean that skips the 6h cache, so an OPERATOR could retry
// in a loop. The ceiling reuses the account-import limiter shape.

// The second paid call inside the window is refused before the runner is
// reached, at both spending endpoints.
func TestScrapeEndpointsRateLimitBeforeReachingTheRunner(t *testing.T) {
	t.Run("target", func(t *testing.T) {
		store := newFakeScrapeStore()
		runner, storage := ingestingRunner(t, "Ctwo")
		// The runner's single dataset key is shared by every call, but the URL
		// decides which post the ingest stores, so preload all three.
		for _, code := range []string{"Cone", "Ctwo", "Cthree"} {
			body, err := json.Marshal(mkIGPostRow(code, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)))
			if err != nil {
				t.Fatalf("marshal %s: %v", code, err)
			}
			storage.items["run/"+code+".json"] = body
		}
		keys := []string{"run/Cone.json", "run/Ctwo.json", "run/Cthree.json"}
		runner.outs = []port.ApifyOutput{
			{Status: "SUCCEEDED", RunID: "r1", Items: rawItems(keys[:1])},
			{Status: "SUCCEEDED", RunID: "r2", Items: rawItems(keys[1:2])},
			{Status: "SUCCEEDED", RunID: "r3", Items: rawItems(keys[2:3])},
		}
		runner.out.Items = nil
		svc := NewScrapeService(store, runner, actorForAny, storage, nil)
		svc.SetSpendLimit(1, time.Minute)

		if _, err := svc.ScrapeTargetAs(context.Background(), domain.PlatformInstagram, "https://www.instagram.com/p/Cone/", true, "1.2.3.4"); err != nil {
			t.Fatalf("first call must pass the ceiling: %v", err)
		}
		if _, err := svc.ScrapeTargetAs(context.Background(), domain.PlatformInstagram, "https://www.instagram.com/p/Ctwo/", true, "1.2.3.4"); !errors.Is(err, domain.ErrRateLimited) {
			t.Fatalf("second call inside the window: want ErrRateLimited, got %v", err)
		}
		if len(runner.runs) != 1 {
			t.Fatalf("a refused call must not reach the runner: want 1 paid run, got %d", len(runner.runs))
		}
		// A different caller has its own budget: it must be admitted past the
		// ceiling. (Whether its run then produces a post is not this test's
		// subject — only ErrRateLimited would prove the ceiling was shared.)
		if _, err := svc.ScrapeTargetAs(context.Background(), domain.PlatformInstagram, "https://www.instagram.com/p/Cthree/", true, "5.6.7.8"); errors.Is(err, domain.ErrRateLimited) {
			t.Fatal("a distinct caller must not share the ceiling")
		}
	})

	t.Run("keywords", func(t *testing.T) {
		store := newFakeScrapeStore()
		runner := &countingRunner{}
		svc := NewKeywordScrapeService(store, runner,
			func(domain.Platform) string { return "apify/instagram-scraper" },
			&fakeRawStorage{items: map[string][]byte{}}, nil)
		svc.SetBatchStore(newFakeKeywordBatchStore())
		svc.SetSpendLimit(1, time.Minute)

		// The ceiling is checked before the batch row exists, so no goroutine
		// and no actor run are involved in the assertion: the refusal is proof
		// the second batch was never created.
		if _, err := svc.CreateBatch(context.Background(), KeywordScrapeInput{
			Platform: domain.PlatformInstagram, Keywords: []string{"a"}, Caller: "1.2.3.4",
		}); err != nil {
			t.Fatalf("first batch must pass the ceiling: %v", err)
		}
		if _, err := svc.CreateBatch(context.Background(), KeywordScrapeInput{
			Platform: domain.PlatformInstagram, Keywords: []string{"b"}, Caller: "1.2.3.4",
		}); !errors.Is(err, domain.ErrRateLimited) {
			t.Fatalf("second batch inside the window: want ErrRateLimited, got %v", err)
		}
		batches, err := svc.batches.ListKeywordBatches(context.Background(), nil, nil)
		if err != nil {
			t.Fatalf("list batches: %v", err)
		}
		if len(batches) != 1 {
			t.Fatalf("a refused batch must not create a row; want 1 batch, got %d", len(batches))
		}
	})
}

// An unconfigured ceiling must not silently become a ceiling of zero — the
// self-hosted default has to keep working.
func TestScrapeSpendCeilingUnconfiguredIsUnlimited(t *testing.T) {
	store := newFakeScrapeStore()
	runner, storage := ingestingRunner(t, "Cun")
	svc := NewScrapeService(store, runner, actorForAny, storage, nil)
	// SetSpendLimit(0, …) means "no ceiling configured".
	svc.SetSpendLimit(0, time.Minute)

	for i := 0; i < 3; i++ {
		if _, err := svc.ScrapeTargetAs(context.Background(), domain.PlatformInstagram,
			"https://www.instagram.com/p/Cun/", true, "1.2.3.4"); err != nil {
			t.Fatalf("call %d must not be refused with no ceiling configured: %v", i, err)
		}
	}
	if len(runner.runs) != 3 {
		t.Fatalf("want 3 paid runs with no ceiling, got %d", len(runner.runs))
	}
}

// The limiter's window is a fixed window with a caller map that must not grow
// without bound for idle callers — the same property the import limiter has.
func TestScrapeSpendLimiterReapsStaleHits(t *testing.T) {
	store := newFakeScrapeStore()
	runner := &countingRunner{}
	svc := NewScrapeService(store, runner, actorForAny, &fakeRawStorage{items: map[string][]byte{}}, nil)
	svc.SetSpendLimit(1, time.Minute)

	// Drive the limiter directly with a old timestamp so the stale hit is reaped.
	if !svc.spend.Allow("9.9.9.9", time.Now().Add(-time.Hour)) {
		t.Fatal("a fresh caller must be allowed")
	}
	if !svc.spend.Allow("9.9.9.9", time.Now()) {
		t.Fatal("a hit from outside the window must not count")
	}
	if svc.spend.Allow("9.9.9.9", time.Now()) {
		t.Fatal("the second in-window hit must be refused")
	}
}

// --- end --------------------------------------------------------------------
