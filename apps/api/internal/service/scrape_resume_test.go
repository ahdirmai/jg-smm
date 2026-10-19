package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// --- Orphaned batch resume ---------------------------------------------------
//
// A keyword batch runs in a detached goroutine, so a redeploy kills it. Before
// this, apify_run.run_id was written only after SUCCEEDED: the killed process
// left the batch RUNNING and the paid Apify run unreachable. These tests pin
// both halves of the fix — the id is recorded the moment the run starts, and a
// boot sweep reconnects to whatever a dead process left behind.

// startedRunner mimics the real runner's contract: OnStarted fires once with
// the Apify-side id before the poll, exactly like RunSearch.
type startedRunner struct {
	fakeRunner
	apifyRunID string
}

func (r *startedRunner) RunSearch(ctx context.Context, in port.ApifySearchInput) (port.ApifyOutput, error) {
	if in.OnStarted != nil {
		in.OnStarted(r.apifyRunID)
	}
	return r.fakeRunner.RunSearch(ctx, in)
}

// A run killed mid-poll must already have its Apify id on the audit row — that
// is the only thing that makes the paid run recoverable.
func TestRunSearchPersistsApifyRunIDBeforePoll(t *testing.T) {
	store := newFakeScrapeStore()
	// The poll never completes: the process "dies" by returning a transport
	// error after the id was handed over.
	runner := &startedRunner{
		fakeRunner: fakeRunner{outErr: context.DeadlineExceeded},
		apifyRunID: "apify-run-xyz",
	}
	svc := NewKeywordScrapeService(store, runner,
		func(domain.Platform) string { return "apify/instagram-scraper" },
		&fakeRawStorage{items: map[string][]byte{}}, nil)
	svc.SetBatchStore(newFakeKeywordBatchStore())

	if _, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram, Keywords: []string{"kamu"},
	}); err == nil {
		t.Fatal("a transport error must be an error")
	}

	store.mu.Lock()
	runs := make([]domain.ApifyRun, 0, len(store.runs))
	for _, r := range store.runs {
		runs = append(runs, r)
	}
	store.mu.Unlock()
	if len(runs) == 0 {
		t.Fatal("no apify_run row")
	}
	if runs[0].RunID != "apify-run-xyz" {
		t.Fatalf("apify_run.run_id = %q, want the id recorded at start", runs[0].RunID)
	}
}

// The heartbeat of the fix: a batch left RUNNING with a stored Apify id is
// reconnected to THAT run — no second start, hence no second bill.
func TestResumeBatchPollsTheRecordedRun(t *testing.T) {
	store := newFakeScrapeStore()
	storage := &fakeRawStorage{items: map[string][]byte{}}
	post, _ := json.Marshal(mkIGPostRow("Dresume", "2026-09-10T02:28:41.000Z"))
	storage.items["run/post-0.json"] = post

	runner := &fakeRunner{out: port.ApifyOutput{
		Status: "SUCCEEDED", RunID: "apify-run-9",
		Items: []port.RawItem{{Key: "run/post-0.json"}},
	}}
	svc := NewKeywordScrapeService(store, runner,
		func(domain.Platform) string { return "apify/instagram-scraper" }, storage, nil)
	batches := newFakeKeywordBatchStore()
	svc.SetBatchStore(batches)

	// The dead process's legacy: apify_run RUNNING with its id recorded, batch
	// RUNNING pointing at it.
	run, err := store.CreateApifyRun(context.Background(), domain.ApifyRun{
		ActorID: "apify/instagram-scraper", RunID: "apify-run-9", Status: "RUNNING",
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	b, err := batches.CreateKeywordBatch(context.Background(), domain.KeywordBatch{
		Platform: domain.PlatformInstagram, Keywords: []string{"kamu"},
		MaxPosts: 50, ActorID: "apify/instagram-scraper",
		Status: domain.KeywordBatchRunning, ApifyRunID: &run.ID,
	})
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}

	svc.resumeBatch(context.Background(), b)

	if len(runner.search) != 1 {
		t.Fatalf("want exactly 1 RunSearch (resume, not restart), got %d", len(runner.search))
	}
	if got := runner.search[0].ResumeRunID; got != "apify-run-9" {
		t.Fatalf("ResumeRunID = %q, want the stored apify id", got)
	}
	got, _ := batches.GetKeywordBatch(context.Background(), b.ID)
	if got.Status != domain.KeywordBatchSucceeded {
		t.Fatalf("batch status = %s, want SUCCEEDED (err %v)", got.Status, got.Error)
	}
	if len(batches.posts[b.ID]) != 1 {
		t.Fatalf("batch posts = %v, want the ingested post linked", batches.posts[b.ID])
	}
}

// A run killed between starting the actor and recording its id is
// unrecoverable. Starting a fresh run would pay twice while the old one may
// still finish, so the batch must fail with the honest reason.
func TestResumeBatchFailsWhenRunIDWasNeverRecorded(t *testing.T) {
	store := newFakeScrapeStore()
	runner := &fakeRunner{out: port.ApifyOutput{Status: "SUCCEEDED", RunID: "r1"}}
	svc := NewKeywordScrapeService(store, runner,
		func(domain.Platform) string { return "apify/instagram-scraper" },
		&fakeRawStorage{items: map[string][]byte{}}, nil)
	batches := newFakeKeywordBatchStore()
	svc.SetBatchStore(batches)

	run, _ := store.CreateApifyRun(context.Background(), domain.ApifyRun{
		ActorID: "apify/instagram-scraper", Status: "RUNNING",
	})
	b, _ := batches.CreateKeywordBatch(context.Background(), domain.KeywordBatch{
		Platform: domain.PlatformInstagram, Keywords: []string{"kamu"},
		MaxPosts: 50, ActorID: "apify/instagram-scraper",
		Status: domain.KeywordBatchRunning, ApifyRunID: &run.ID,
	})

	svc.resumeBatch(context.Background(), b)

	if len(runner.search) != 0 {
		t.Fatalf("an unrecoverable run must not start a new one, got %d runs", len(runner.search))
	}
	got, _ := batches.GetKeywordBatch(context.Background(), b.ID)
	if got.Status != domain.KeywordBatchFailed {
		t.Fatalf("batch status = %s, want FAILED", got.Status)
	}
	if got.Error == nil || !strings.Contains(*got.Error, "before the Apify run id was recorded") {
		t.Fatalf("error = %v, want the not-recorded reason", got.Error)
	}
}

// A batch killed before it even created its audit row spent nothing, so a
// fresh launch is safe.
func TestResumeBatchRestartsWhenNothingWasSpent(t *testing.T) {
	svc, _, runner := keywordSvcFor(t, &fakeRawStorage{items: map[string][]byte{}})
	batches := newFakeKeywordBatchStore()
	svc.SetBatchStore(batches)

	b, _ := batches.CreateKeywordBatch(context.Background(), domain.KeywordBatch{
		Platform: domain.PlatformInstagram, Keywords: []string{"kamu"},
		MaxPosts: 50, ActorID: "apify/instagram-scraper",
		Status: domain.KeywordBatchRunning,
	})

	svc.resumeBatch(context.Background(), b)

	if len(runner.search) == 0 {
		t.Fatal("a batch with no audit row must restart")
	}
	if got := runner.search[0].ResumeRunID; got != "" {
		t.Fatalf("ResumeRunID = %q, want a fresh start", got)
	}
	got, _ := batches.GetKeywordBatch(context.Background(), b.ID)
	if got.Status != domain.KeywordBatchSucceeded {
		t.Fatalf("batch status = %s, want SUCCEEDED", got.Status)
	}
}

// The boot sweep is what runs the above in production: it must find RUNNING
// batches without being told their ids.
func TestResumeOrphanedSweepsRunningBatches(t *testing.T) {
	store := newFakeScrapeStore()
	runner := &fakeRunner{out: port.ApifyOutput{Status: "SUCCEEDED", RunID: "r1"}}
	svc := NewKeywordScrapeService(store, runner,
		func(domain.Platform) string { return "apify/instagram-scraper" },
		&fakeRawStorage{items: map[string][]byte{}}, nil)
	batches := newFakeKeywordBatchStore()
	svc.SetBatchStore(batches)

	run, _ := store.CreateApifyRun(context.Background(), domain.ApifyRun{
		ActorID: "apify/instagram-scraper", RunID: "apify-run-1", Status: "RUNNING",
	})
	b, _ := batches.CreateKeywordBatch(context.Background(), domain.KeywordBatch{
		Platform: domain.PlatformInstagram, Keywords: []string{"kamu"},
		MaxPosts: 50, ActorID: "apify/instagram-scraper",
		Status: domain.KeywordBatchRunning, ApifyRunID: &run.ID,
	})

	svc.ResumeOrphaned(context.Background())

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := batches.GetKeywordBatch(context.Background(), b.ID)
		if got.Status.IsTerminal() {
			if got.Status != domain.KeywordBatchSucceeded {
				t.Fatalf("swept batch status = %s (err %v), want SUCCEEDED", got.Status, got.Error)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("ResumeOrphaned left the batch RUNNING")
}
