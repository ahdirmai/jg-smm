package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/repository/sqlcgen"
)

// p3PtrInt boxes an int so the paging params (limit/offset) can be optional.
func p3PtrInt(n int) *int { return &n }

// P3 action-engine repository tests. Same contract as the P2 suite: real
// Postgres, skipped unless SMM_TEST_DB=1, because the interesting behaviour
// lives in the SQL — the FIFO claim, the (job, attempt) upsert, the COALESCE
// that protects an earlier screenshot, and the CHECK constraints.

// newP3TestRepo builds the action repo over a real pool and clears the P3
// tables so every test is deterministic against a shared dev database.
func newP3TestRepo(t *testing.T) *ActionRepo {
	t.Helper()
	if os.Getenv("SMM_TEST_DB") != "1" {
		t.Skip("set SMM_TEST_DB=1 to run repository integration tests")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://smm:smm@localhost:24543/smm?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	// action_log -> action_job -> {target, account, worker}. Clearing these
	// makes each test independent; the parent tables are emptied so a unique
	// constraint never fires on a row left behind by a previous run.
	if _, err := pool.Exec(context.Background(), `
		TRUNCATE TABLE
		  action_log, action_job,
		  scrape_job, target, comment, post, raw_payload, apify_run,
		  official_account, analytics_snapshot, analytics_mention,
		  analytics_ingest_run, provision_log,
		  worker, account
		CASCADE`); err != nil {
		t.Fatalf("truncate p3 tables: %v", err)
	}
	return NewActionRepo(sqlcgen.New(pool))
}

// p3SeedWorker inserts a worker row the action_job.worker_id FK can reference,
// and returns its id. A job is stamped with the claiming worker at claim time,
// so the FK needs a real row.
func p3SeedWorker(t *testing.T, pool *pgxpool.Pool, name string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := pool.QueryRow(ctx,
		`INSERT INTO worker (name, region, status, desired_state, source)
		 VALUES ($1, 'SG', 'IDLE', 'RUNNING', 'MANUAL')
		 ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		 RETURNING id::text`, name).Scan(&id)
	if err != nil {
		t.Fatalf("seed worker: %v", err)
	}
	return id
}

// p3SeedTarget inserts a target row an action_job can point at, and returns its
// id. Targets are platform-local coordinates for an action.
func p3SeedTarget(t *testing.T, pool *pgxpool.Pool, externalID string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := pool.QueryRow(ctx,
		`INSERT INTO target (kind, platform, external_id, url)
		 VALUES ('POST', 'INSTAGRAM', $1, $2)
		 ON CONFLICT (platform, external_id) DO UPDATE SET url = EXCLUDED.url
		 RETURNING id::text`,
		externalID, "https://instagram.com/p/"+externalID).Scan(&id)
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}
	return id
}

// TestActionJobRoundTrip checks create/read and the defaults the caller leans
// on: PENDING status, attempts 0, and scheduled_at defaulting to now.
func TestActionJobRoundTrip(t *testing.T) {
	repo := newP3TestRepo(t)
	ctx := context.Background()
	pool := testPool(t)

	accountID := p2SeedAccount(t, pool, "act-rt-"+p2Suffix(t))
	targetID := p3SeedTarget(t, pool, "tgt-rt-"+p2Suffix(t))

	job, err := repo.CreateActionJob(ctx, domain.ActionJob{
		Type:      domain.JobTypeActionComment,
		TargetID:  targetID,
		AccountID: accountID,
	})
	if err != nil {
		t.Fatalf("create action job: %v", err)
	}
	if job.ID == "" || job.Status != domain.JobStatusPending {
		t.Fatalf("new action job must be PENDING, got status %q id %q", job.Status, job.ID)
	}
	if job.Attempts != 0 {
		t.Fatalf("new action job must start at 0 attempts, got %d", job.Attempts)
	}
	if job.ScheduledAt.IsZero() {
		t.Fatal("scheduled_at must default to now when omitted")
	}

	got, err := repo.GetActionJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get action job: %v", err)
	}
	if got.Type != domain.JobTypeActionComment || got.TargetID != targetID || got.AccountID != accountID {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	// A like is a valid action type too, exercising the other enum value.
	like, err := repo.CreateActionJob(ctx, domain.ActionJob{
		Type:        domain.JobTypeActionLike,
		TargetID:    targetID,
		AccountID:   accountID,
		ScheduledAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create like job: %v", err)
	}
	if like.Type != domain.JobTypeActionLike {
		t.Fatalf("expected ACTION_LIKE, got %q", like.Type)
	}

	// Unknown id surfaces as the domain sentinel, not driver plumbing.
	if _, err := repo.GetActionJob(ctx, "00000000-0000-0000-0000-000000000099"); err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound for missing job, got %v", err)
	}
}

// TestActionJobFIFOClaim asserts the queue discipline: the oldest due job for
// an account is claimed first, a future job is not claimable, and an empty
// queue returns ErrNotFound so the poll loop can idle.
func TestActionJobFIFOClaim(t *testing.T) {
	repo := newP3TestRepo(t)
	ctx := context.Background()
	pool := testPool(t)

	accountID := p2SeedAccount(t, pool, "act-fifo-"+p2Suffix(t))
	targetID := p3SeedTarget(t, pool, "tgt-fifo-"+p2Suffix(t))
	workerID := p3SeedWorker(t, pool, "wrk-fifo-"+p2Suffix(t))

	base := time.Now().UTC()
	if _, err := repo.CreateActionJob(ctx, domain.ActionJob{
		Type:        domain.JobTypeActionLike,
		TargetID:    targetID,
		AccountID:   accountID,
		ScheduledAt: base.Add(time.Hour),
	}); err != nil {
		t.Fatalf("create later job: %v", err)
	}
	earlier, err := repo.CreateActionJob(ctx, domain.ActionJob{
		Type:        domain.JobTypeActionLike,
		TargetID:    targetID,
		AccountID:   accountID,
		ScheduledAt: base.Add(-time.Hour), // due now
	})
	if err != nil {
		t.Fatalf("create earlier job: %v", err)
	}

	// Another account's queue is empty -> ErrNotFound, not an error.
	if _, err := repo.ClaimNextActionJob(ctx, "00000000-0000-0000-0000-000000000099", workerID); err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound for empty account, got %v", err)
	}

	claimed, err := repo.ClaimNextActionJob(ctx, accountID, workerID)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.ID != earlier.ID {
		t.Fatalf("FIFO broken: claimed %s, expected %s", claimed.ID, earlier.ID)
	}
	if claimed.Status != domain.JobStatusRunning {
		t.Fatalf("claimed job must be RUNNING, got %q", claimed.Status)
	}
	if claimed.Attempts != 1 {
		t.Fatalf("claim must bump attempts to 1, got %d", claimed.Attempts)
	}
	if claimed.WorkerID != workerID {
		t.Fatalf("claim must stamp worker_id, got %q", claimed.WorkerID)
	}
	if claimed.StartedAt == nil {
		t.Fatal("claim must set started_at")
	}

	// The future job must stay claimable-later, not be claimed now.
	if _, err := repo.ClaimNextActionJob(ctx, accountID, workerID); err != domain.ErrNotFound {
		t.Fatalf("future job must not be claimable, got %v", err)
	}

	// Completion is a terminal projection: status flips, finished_at is set.
	done, err := repo.CompleteActionJob(ctx, claimed.ID, domain.JobStatusSuccess, nil)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if done.Status != domain.JobStatusSuccess || done.FinishedAt == nil {
		t.Fatalf("job not completed: %+v", done)
	}
}

// TestActionJobReschedule exercises the retry/backoff path: a failed-but-
// retryable job returns to PENDING at a future time and becomes claimable
// again once that time passes.
func TestActionJobReschedule(t *testing.T) {
	repo := newP3TestRepo(t)
	ctx := context.Background()
	pool := testPool(t)

	accountID := p2SeedAccount(t, pool, "act-resched-"+p2Suffix(t))
	targetID := p3SeedTarget(t, pool, "tgt-resched-"+p2Suffix(t))
	workerID := p3SeedWorker(t, pool, "wrk-resched-"+p2Suffix(t))

	job, err := repo.CreateActionJob(ctx, domain.ActionJob{
		Type:        domain.JobTypeActionComment,
		TargetID:    targetID,
		AccountID:   accountID,
		ScheduledAt: time.Now().UTC().Add(-time.Hour), // due now
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Nothing is claimable while the job sits in the future.
	backoff := time.Now().UTC().Add(time.Hour)
	rescheduled, err := repo.RescheduleActionJob(ctx, job.ID, backoff)
	if err != nil {
		t.Fatalf("reschedule: %v", err)
	}
	if rescheduled.Status != domain.JobStatusPending {
		t.Fatalf("reschedule must reset status to PENDING, got %q", rescheduled.Status)
	}
	if !rescheduled.ScheduledAt.Equal(backoff) {
		t.Fatalf("reschedule must move scheduled_at, got %v", rescheduled.ScheduledAt)
	}

	// A rescheduled (future) job is not claimable yet.
	if _, err := repo.ClaimNextActionJob(ctx, accountID, workerID); err != domain.ErrNotFound {
		t.Fatalf("rescheduled future job must not be claimable, got %v", err)
	}

	// Pushing it back into the past makes it claimable again — the retry loop.
	if _, err := repo.RescheduleActionJob(ctx, job.ID, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("reschedule back: %v", err)
	}
	claimed, err := repo.ClaimNextActionJob(ctx, accountID, workerID)
	if err != nil {
		t.Fatalf("claim after reschedule: %v", err)
	}
	if claimed.ID != job.ID {
		t.Fatalf("expected the rescheduled job to be claimed, got %s", claimed.ID)
	}
	if claimed.Attempts != 1 {
		t.Fatalf("claim after reschedule must count the attempt, got %d", claimed.Attempts)
	}
}

// TestActionJobListFilters checks the three queue views the dashboard reads:
// all, by status, and by account.
func TestActionJobListFilters(t *testing.T) {
	repo := newP3TestRepo(t)
	ctx := context.Background()
	pool := testPool(t)

	accountA := p2SeedAccount(t, pool, "act-lst-a-"+p2Suffix(t))
	accountB := p2SeedAccount(t, pool, "act-lst-b-"+p2Suffix(t))
	targetID := p3SeedTarget(t, pool, "tgt-lst-"+p2Suffix(t))

	pending, err := repo.CreateActionJob(ctx, domain.ActionJob{
		Type:        domain.JobTypeActionLike,
		TargetID:    targetID,
		AccountID:   accountA,
		ScheduledAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	done, err := repo.CreateActionJob(ctx, domain.ActionJob{
		Type:        domain.JobTypeActionLike,
		TargetID:    targetID,
		AccountID:   accountB,
		ScheduledAt: time.Now().UTC().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("create done: %v", err)
	}
	if _, err := repo.CompleteActionJob(ctx, done.ID, domain.JobStatusSuccess, nil); err != nil {
		t.Fatalf("complete done: %v", err)
	}

	all, err := repo.ListActionJobs(ctx, p3PtrInt(10), p3PtrInt(0))
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(all))
	}

	byStatus, err := repo.ListActionJobsByStatus(ctx, domain.JobStatusPending, p3PtrInt(10), p3PtrInt(0))
	if err != nil {
		t.Fatalf("list by status: %v", err)
	}
	if len(byStatus) != 1 || byStatus[0].ID != pending.ID {
		t.Fatalf("status filter must return only the pending job, got %d", len(byStatus))
	}

	byAccount, err := repo.ListActionJobsByAccount(ctx, accountB, p3PtrInt(10), p3PtrInt(0))
	if err != nil {
		t.Fatalf("list by account: %v", err)
	}
	if len(byAccount) != 1 || byAccount[0].ID != done.ID {
		t.Fatalf("account filter must return only account B's job, got %d", len(byAccount))
	}
}

// TestActionLogUpsertIdempotent is the callback heart: the worker reports the
// attempt as RUNNING, then posts the terminal verdict on the SAME row (same
// job + attempt). UNIQUE (action_job_id, attempt) makes them one row, so an
// attempt is never duplicated and a retry is always a new attempt number.
func TestActionLogUpsertIdempotent(t *testing.T) {
	repo := newP3TestRepo(t)
	ctx := context.Background()
	pool := testPool(t)

	accountID := p2SeedAccount(t, pool, "act-log-"+p2Suffix(t))
	targetID := p3SeedTarget(t, pool, "tgt-log-"+p2Suffix(t))
	workerID := p3SeedWorker(t, pool, "wrk-log-"+p2Suffix(t))

	_, err := repo.CreateActionJob(ctx, domain.ActionJob{
		Type:        domain.JobTypeActionComment,
		TargetID:    targetID,
		AccountID:   accountID,
		ScheduledAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	claimed, err := repo.ClaimNextActionJob(ctx, accountID, workerID)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	rendered := "nice post!"

	// 1. The worker starts the attempt: RUNNING, no verdict yet, but it already
	// captured a screenshot it does not want to lose.
	running, err := repo.UpsertActionLog(ctx, domain.ActionLog{
		ActionJobID:   claimed.ID,
		Attempt:       claimed.Attempts,
		Status:        domain.AttemptRunning,
		WorkerID:      workerID,
		RenderedText:  rendered,
		ScreenshotURL: "s3://shots/running.png",
		DurationMs:    120,
	})
	if err != nil {
		t.Fatalf("upsert running: %v", err)
	}
	if running.Status != domain.AttemptRunning {
		t.Fatalf("expected RUNNING, got %q", running.Status)
	}
	if running.ScreenshotURL != "s3://shots/running.png" {
		t.Fatalf("running callback must keep its screenshot, got %q", running.ScreenshotURL)
	}

	// 2. The terminal verdict arrives on the same (job, attempt) — one row, not
	// two. It omits the screenshot; COALESCE must preserve the earlier capture.
	terminal, err := repo.UpsertActionLog(ctx, domain.ActionLog{
		ActionJobID:   claimed.ID,
		Attempt:       claimed.Attempts,
		Status:        domain.AttemptSuccess,
		Verified:      true,
		WorkerID:      workerID,
		RenderedText:  rendered,
		ScreenshotURL: "", // terminal callback has no new shot
		DurationMs:    340,
	})
	if err != nil {
		t.Fatalf("upsert terminal: %v", err)
	}
	if terminal.ID != running.ID {
		t.Fatal("RUNNING and terminal must share one row (unique job+attempt)")
	}
	if terminal.Status != domain.AttemptSuccess || !terminal.Verified {
		t.Fatalf("terminal verdict must be SUCCESS+verified, got %q verified=%v", terminal.Status, terminal.Verified)
	}
	if terminal.ScreenshotURL != "s3://shots/running.png" {
		t.Fatalf("COALESCE must keep the earlier screenshot, got %q", terminal.ScreenshotURL)
	}
	if terminal.DurationMs != 340 {
		t.Fatalf("duration must be updated to the terminal value, got %d", terminal.DurationMs)
	}

	// 3. Exactly one row for the attempt — no duplicate verdict rows.
	logs, err := repo.ListActionLogsByJob(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log row for one attempt, got %d", len(logs))
	}

	got, err := repo.GetActionLog(ctx, claimed.ID, claimed.Attempts)
	if err != nil {
		t.Fatalf("get log: %v", err)
	}
	if got.ResponseExcerpt != "" {
		t.Fatalf("expected empty excerpt preserved, got %q", got.ResponseExcerpt)
	}
	if _, err := repo.GetActionLog(ctx, claimed.ID, 999); err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound for missing attempt, got %v", err)
	}
}

// TestActionLogRetryNewAttempt asserts a retry is a NEW attempt number: the
// previous attempt's verdict survives untouched, and the list is newest-first.
func TestActionLogRetryNewAttempt(t *testing.T) {
	repo := newP3TestRepo(t)
	ctx := context.Background()
	pool := testPool(t)

	accountID := p2SeedAccount(t, pool, "act-retry-"+p2Suffix(t))
	targetID := p3SeedTarget(t, pool, "tgt-retry-"+p2Suffix(t))
	workerID := p3SeedWorker(t, pool, "wrk-retry-"+p2Suffix(t))

	job, err := repo.CreateActionJob(ctx, domain.ActionJob{
		Type:        domain.JobTypeActionComment,
		TargetID:    targetID,
		AccountID:   accountID,
		ScheduledAt: time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Attempt 1 fails with a retryable class; the job is rescheduled.
	first, err := repo.ClaimNextActionJob(ctx, accountID, workerID)
	if err != nil {
		t.Fatalf("claim 1: %v", err)
	}
	if _, err := repo.UpsertActionLog(ctx, domain.ActionLog{
		ActionJobID:  first.ID,
		Attempt:      first.Attempts,
		Status:       domain.AttemptFailed,
		WorkerID:     workerID,
		RenderedText: "hi",
		ErrorClass:   string(domain.ErrorClassTransient),
		DurationMs:   99,
	}); err != nil {
		t.Fatalf("upsert fail: %v", err)
	}
	if _, err := repo.RescheduleActionJob(ctx, first.ID, time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatalf("reschedule: %v", err)
	}

	// Attempt 2 succeeds; attempts is bumped again.
	second, err := repo.ClaimNextActionJob(ctx, accountID, workerID)
	if err != nil {
		t.Fatalf("claim 2: %v", err)
	}
	if second.Attempts != 2 {
		t.Fatalf("retry must be attempt 2, got %d", second.Attempts)
	}
	if _, err := repo.UpsertActionLog(ctx, domain.ActionLog{
		ActionJobID:  second.ID,
		Attempt:      second.Attempts,
		Status:       domain.AttemptSuccess,
		Verified:     true,
		WorkerID:     workerID,
		RenderedText: "hi",
	}); err != nil {
		t.Fatalf("upsert success: %v", err)
	}

	logs, err := repo.ListActionLogsByJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(logs))
	}
	if logs[0].Attempt != 2 || logs[1].Attempt != 1 {
		t.Fatalf("logs must be newest-first, got %d then %d", logs[0].Attempt, logs[1].Attempt)
	}
	// The failed attempt's verdict is untouched by the retry.
	if logs[1].Status != domain.AttemptFailed || logs[1].ErrorClass != string(domain.ErrorClassTransient) {
		t.Fatalf("attempt 1 must keep its FAILED verdict, got %+v", logs[1])
	}
}

// TestActionLogConstraints checks the schema guards the service leans on:
// attempt must be > 0, duration >= 0, and error_class must be a known class.
func TestActionLogConstraints(t *testing.T) {
	repo := newP3TestRepo(t)
	ctx := context.Background()
	pool := testPool(t)

	accountID := p2SeedAccount(t, pool, "act-con-"+p2Suffix(t))
	targetID := p3SeedTarget(t, pool, "tgt-con-"+p2Suffix(t))
	workerID := p3SeedWorker(t, pool, "wrk-con-"+p2Suffix(t))

	_, err := repo.CreateActionJob(ctx, domain.ActionJob{
		Type:        domain.JobTypeActionLike,
		TargetID:    targetID,
		AccountID:   accountID,
		ScheduledAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	claimed, err := repo.ClaimNextActionJob(ctx, accountID, workerID)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	// attempt <= 0 is rejected by the repo before it hits SQL: the attempt
	// number is the retry counter and attempt 0 has no meaning.
	if _, err := repo.UpsertActionLog(ctx, domain.ActionLog{
		ActionJobID:  claimed.ID,
		Attempt:      0,
		Status:       domain.AttemptSuccess,
		RenderedText: "x",
	}); err == nil {
		t.Fatal("expected error for attempt 0, got nil")
	}

	// A negative duration is a schema violation: durations are measurements.
	if _, err := repo.UpsertActionLog(ctx, domain.ActionLog{
		ActionJobID:  claimed.ID,
		Attempt:      claimed.Attempts,
		Status:       domain.AttemptFailed,
		RenderedText: "x",
		DurationMs:   -1,
	}); err == nil {
		t.Fatal("expected error for negative duration, got nil")
	}

	// An unknown error class is rejected by the CHECK constraint: the classifier
	// is a closed set, and a typo must not silently become a new class.
	if _, err := repo.UpsertActionLog(ctx, domain.ActionLog{
		ActionJobID:  claimed.ID,
		Attempt:      claimed.Attempts,
		Status:       domain.AttemptFailed,
		RenderedText: "x",
		ErrorClass:   "NOT_A_CLASS",
	}); err == nil {
		t.Fatal("expected error for unknown error_class, got nil")
	}

	// Every documented class is accepted, including the NULL (unclassified)
	// case: a failure the classifier has not labelled yet.
	for _, c := range domain.AllErrorClasses {
		if _, err := repo.UpsertActionLog(ctx, domain.ActionLog{
			ActionJobID:  claimed.ID,
			Attempt:      claimed.Attempts,
			Status:       domain.AttemptFailed,
			WorkerID:     workerID,
			RenderedText: "x",
			ErrorClass:   string(c),
		}); err != nil {
			t.Fatalf("error_class %q must be accepted: %v", c, err)
		}
	}
}

// TestActionLogsByErrorClass checks the failure-class triage view (P3-12):
// recent failures of one class, newest-first.
func TestActionLogsByErrorClass(t *testing.T) {
	repo := newP3TestRepo(t)
	ctx := context.Background()
	pool := testPool(t)

	accountID := p2SeedAccount(t, pool, "act-cls-"+p2Suffix(t))
	targetID := p3SeedTarget(t, pool, "tgt-cls-"+p2Suffix(t))
	workerID := p3SeedWorker(t, pool, "wrk-cls-"+p2Suffix(t))

	_, err := repo.CreateActionJob(ctx, domain.ActionJob{
		Type:        domain.JobTypeActionComment,
		TargetID:    targetID,
		AccountID:   accountID,
		ScheduledAt: time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	claimed, err := repo.ClaimNextActionJob(ctx, accountID, workerID)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := repo.UpsertActionLog(ctx, domain.ActionLog{
		ActionJobID:  claimed.ID,
		Attempt:      claimed.Attempts,
		Status:       domain.AttemptFailed,
		WorkerID:     workerID,
		RenderedText: "hi",
		ErrorClass:   string(domain.ErrorClassRateLimit),
	}); err != nil {
		t.Fatalf("upsert rate-limited: %v", err)
	}

	// The AUTH slice is empty even though failures exist under another class.
	auth, err := repo.ListActionLogsByErrorClass(ctx, domain.ErrorClassAuth, p3PtrInt(10), p3PtrInt(0))
	if err != nil {
		t.Fatalf("list by auth class: %v", err)
	}
	if len(auth) != 0 {
		t.Fatalf("AUTH class must be empty, got %d", len(auth))
	}

	rl, err := repo.ListActionLogsByErrorClass(ctx, domain.ErrorClassRateLimit, p3PtrInt(10), p3PtrInt(0))
	if err != nil {
		t.Fatalf("list by rate-limit class: %v", err)
	}
	if len(rl) != 1 || rl[0].ErrorClass != string(domain.ErrorClassRateLimit) {
		t.Fatalf("rate-limit class must return the one failure, got %d", len(rl))
	}
}
