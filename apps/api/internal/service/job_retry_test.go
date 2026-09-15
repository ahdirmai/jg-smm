package service

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
)

// P3-11 action callback + retry tests. The store is a scripted fake (the SQL
// upsert/claim/reschedule are proven against real Postgres in
// repository/action_test.go); what is proven here is the policy: which verdicts
// get another attempt, which terminate, and that a replayed callback does not
// duplicate a verdict.

// fakeActionStore records the calls so a test asserts the policy's decisions
// instead of the fake's own logic.
type fakeActionStore struct {
	logs         []domain.ActionLog
	logByAttempt map[string]domain.ActionLog
	completed    []completedJob
	rescheduled  []rescheduledJob
	// pending is the queue the scheduler's ListActionJobsByStatus returns, in
	// insertion order so a test can assert claim order.
	pending []domain.ActionJob
}

type completedJob struct {
	id     string
	status domain.JobStatus
	err    *string
}

type rescheduledJob struct {
	id string
	at time.Time
}

func newFakeActionStore() *fakeActionStore {
	return &fakeActionStore{logByAttempt: map[string]domain.ActionLog{}}
}

func attemptKey(jobID string, attempt int) string { return jobID + ":" + strconv.Itoa(attempt) }

func (f *fakeActionStore) UpsertActionLog(_ context.Context, l domain.ActionLog) (domain.ActionLog, error) {
	if l.Attempt <= 0 {
		return domain.ActionLog{}, domain.ErrValidation
	}
	k := attemptKey(l.ActionJobID, l.Attempt)
	f.logs = append(f.logs, l)
	f.logByAttempt[k] = l
	return l, nil
}

func (f *fakeActionStore) GetActionJob(_ context.Context, id string) (domain.ActionJob, error) {
	return domain.ActionJob{}, domain.ErrNotFound
}
func (f *fakeActionStore) CreateActionJob(_ context.Context, j domain.ActionJob) (domain.ActionJob, error) {
	f.pending = append(f.pending, j)
	return j, nil
}
func (f *fakeActionStore) ClaimNextActionJob(_ context.Context, accountID, workerID string) (domain.ActionJob, error) {
	// Claim the first PENDING row for the account, mirroring the SQL claim.
	for i := range f.pending {
		if f.pending[i].Status == domain.JobStatusPending && f.pending[i].AccountID == accountID {
			f.pending[i].Status = domain.JobStatusRunning
			f.pending[i].Attempts++
			if workerID != "" {
				f.pending[i].WorkerID = workerID
			}
			return f.pending[i], nil
		}
	}
	return domain.ActionJob{}, domain.ErrNotFound
}
func (f *fakeActionStore) CompleteActionJob(_ context.Context, id string, status domain.JobStatus, errMsg *string) (domain.ActionJob, error) {
	f.completed = append(f.completed, completedJob{id: id, status: status, err: errMsg})
	return domain.ActionJob{ID: id, Status: status}, nil
}
func (f *fakeActionStore) RescheduleActionJob(_ context.Context, id string, at time.Time) (domain.ActionJob, error) {
	f.rescheduled = append(f.rescheduled, rescheduledJob{id: id, at: at})
	return domain.ActionJob{ID: id, ScheduledAt: at}, nil
}
func (f *fakeActionStore) GetActionLog(_ context.Context, jobID string, attempt int) (domain.ActionLog, error) {
	if l, ok := f.logByAttempt[attemptKey(jobID, attempt)]; ok {
		return l, nil
	}
	return domain.ActionLog{}, domain.ErrNotFound
}
func (f *fakeActionStore) ListActionLogsByJob(_ context.Context, jobID string) ([]domain.ActionLog, error) {
	return nil, nil
}
func (f *fakeActionStore) ListActionLogsByErrorClass(_ context.Context, class domain.ErrorClass, limit, offset *int) ([]domain.ActionLog, error) {
	return nil, nil
}
func (f *fakeActionStore) ListActionJobs(_ context.Context, limit, offset *int) ([]domain.ActionJob, error) {
	return nil, nil
}
func (f *fakeActionStore) ListActionJobsByStatus(_ context.Context, status domain.JobStatus, limit, offset *int) ([]domain.ActionJob, error) {
	if status != domain.JobStatusPending {
		return nil, nil
	}
	out := make([]domain.ActionJob, 0, len(f.pending))
	for _, j := range f.pending {
		if j.Status == domain.JobStatusPending {
			out = append(out, j)
		}
	}
	return out, nil
}
func (f *fakeActionStore) ListActionJobsByAccount(_ context.Context, accountID string, limit, offset *int) ([]domain.ActionJob, error) {
	return nil, nil
}
func (f *fakeActionStore) LatestActionLogsByJobs(_ context.Context, jobIDs []string) ([]domain.ActionLog, error) {
	want := make(map[string]bool, len(jobIDs))
	for _, id := range jobIDs {
		want[id] = true
	}
	out := make([]domain.ActionLog, 0, len(f.logs))
	for _, l := range f.logs {
		if want[l.ActionJobID] {
			out = append(out, l)
		}
	}
	return out, nil
}

// newRetryTestService wires a JobService over the fake store with a short
// backoff so the reschedule math is readable in assertions. fixedClock is
// shared with the auth tests.
func newRetryTestService(store *fakeActionStore) *JobService {
	svc := NewJobService(nil, nil, nil, store, fixedClock{t: time.Unix(1_000_000, 0).UTC()}, nil)
	svc.retry = RetryPolicy{MaxAttempts: 3, Backoff: 10 * time.Second}
	return svc
}

func TestParseAttemptID(t *testing.T) {
	cases := []struct {
		in      string
		wantJob string
		wantN   int
		wantErr bool
	}{
		{"job-1:2", "job-1", 2, false},
		{"job-1:1", "job-1", 1, false},
		{"", "", 0, true},
		{"job-1", "", 0, true},   // no attempt number
		{"job-1:", "", 0, true},  // empty attempt number
		{"job-1:0", "", 0, true}, // attempts are 1-based
		{"job-1:-3", "", 0, true},
		{"job-1:x", "", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			job, n, err := parseAttemptID(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if job != tc.wantJob || n != tc.wantN {
				t.Fatalf("parse %q = (%q,%d), want (%q,%d)", tc.in, job, n, tc.wantJob, tc.wantN)
			}
		})
	}
}

// TestRecordAttemptSuccessCompletes: a success is verified on the log row and
// completes the job. No retry is scheduled for a success, ever.
func TestRecordAttemptSuccessCompletes(t *testing.T) {
	store := newFakeActionStore()
	svc := newRetryTestService(store)
	ctx := context.Background()

	if err := svc.RecordAttempt(ctx, AttemptRecord{
		AttemptID:    "job-ok:1",
		Status:       domain.AttemptSuccess,
		RenderedText: strPtr("love the launch"),
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if len(store.rescheduled) != 0 {
		t.Fatal("a success must not schedule a retry")
	}
	if len(store.completed) != 1 || store.completed[0].status != domain.JobStatusSuccess {
		t.Fatalf("job must be completed SUCCESS, got %+v", store.completed)
	}
	log, err := store.GetActionLog(ctx, "job-ok", 1)
	if err != nil {
		t.Fatalf("get log: %v", err)
	}
	if !log.Verified || log.Status != domain.AttemptSuccess {
		t.Fatalf("attempt must be verified SUCCESS, got %+v", log)
	}
	if log.RenderedText != "love the launch" {
		t.Fatalf("rendered text must be stored, got %q", log.RenderedText)
	}
	if log.ErrorClass != "" {
		t.Fatalf("a success carries no error class, got %q", log.ErrorClass)
	}
}

// TestRecordAttemptRetryableReschedules: a TRANSIENT failure inside the budget
// goes back to the queue with escalating backoff, not to FAILED.
func TestRecordAttemptRetryableReschedules(t *testing.T) {
	store := newFakeActionStore()
	svc := newRetryTestService(store)
	ctx := context.Background()
	base := svc.clock.Now()

	if err := svc.RecordAttempt(ctx, AttemptRecord{
		AttemptID: "job-r:1",
		Status:    domain.AttemptFailed,
		Error:     strPtr("page.goto: Timeout 30000ms exceeded"),
	}); err != nil {
		t.Fatalf("record 1: %v", err)
	}
	if len(store.rescheduled) != 1 {
		t.Fatalf("a retryable failure must be rescheduled, got %d", len(store.rescheduled))
	}
	// backoff << (attempt-1) = 10s << 0 = 10s after base.
	want := base.Add(10 * time.Second)
	if !store.rescheduled[0].at.Equal(want) {
		t.Fatalf("backoff: got %v, want %v", store.rescheduled[0].at, want)
	}
	if len(store.completed) != 0 {
		t.Fatal("a retryable failure inside the budget must not complete the job")
	}
	log, _ := store.GetActionLog(ctx, "job-r", 1)
	if log.ErrorClass != string(domain.ErrorClassTransient) {
		t.Fatalf("class must be TRANSIENT, got %q", log.ErrorClass)
	}
}

// TestRecordAttemptEscalatingBackoff: the second retry waits twice as long as
// the first (backoff shifts left per attempt).
func TestRecordAttemptEscalatingBackoff(t *testing.T) {
	store := newFakeActionStore()
	svc := newRetryTestService(store)
	ctx := context.Background()
	base := svc.clock.Now()

	for attempt := 1; attempt <= 2; attempt++ {
		if err := svc.RecordAttempt(ctx, AttemptRecord{
			AttemptID: "job-b:" + strconv.Itoa(attempt),
			Status:    domain.AttemptFailed,
			Error:     strPtr("net::ERR_CONNECTION_RESET"),
		}); err != nil {
			t.Fatalf("record %d: %v", attempt, err)
		}
	}
	if len(store.rescheduled) != 2 {
		t.Fatalf("expected 2 reschedules, got %d", len(store.rescheduled))
	}
	// attempt 1 -> 10s (10<<0); attempt 2 -> 20s (10<<1).
	want := []time.Duration{10 * time.Second, 20 * time.Second}
	for i, r := range store.rescheduled {
		if got := r.at.Sub(base); got != want[i] {
			t.Fatalf("reschedule %d: got %v, want %v", i, got, want[i])
		}
	}
}

// TestRecordAttemptBudgetExhausted: a retryable failure on the last allowed
// attempt terminates as FAILED with the reason preserved.
func TestRecordAttemptBudgetExhausted(t *testing.T) {
	store := newFakeActionStore()
	svc := newRetryTestService(store)
	ctx := context.Background()

	if err := svc.RecordAttempt(ctx, AttemptRecord{
		AttemptID: "job-x:3",
		Status:    domain.AttemptFailed,
		Error:     strPtr("timeout again"),
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if len(store.rescheduled) != 0 {
		t.Fatal("the last allowed attempt must not be rescheduled")
	}
	if len(store.completed) != 1 || store.completed[0].status != domain.JobStatusFailed {
		t.Fatalf("job must be FAILED, got %+v", store.completed)
	}
	if store.completed[0].err == nil || *store.completed[0].err != "timeout again" {
		t.Fatalf("the failure reason must be preserved, got %+v", store.completed[0].err)
	}
}

// TestRecordAttemptAuthNeverRetries: an AUTH failure terminates immediately
// even with budget left — re-running the action on a dead session cannot
// produce a different result.
func TestRecordAttemptAuthNeverRetries(t *testing.T) {
	store := newFakeActionStore()
	svc := newRetryTestService(store)
	ctx := context.Background()

	if err := svc.RecordAttempt(ctx, AttemptRecord{
		AttemptID: "job-a:1",
		Status:    domain.AttemptFailed,
		Error:     strPtr("Please log in to continue"),
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if len(store.rescheduled) != 0 {
		t.Fatal("an AUTH failure must never be retried")
	}
	if len(store.completed) != 1 || store.completed[0].status != domain.JobStatusFailed {
		t.Fatalf("AUTH must terminate FAILED, got %+v", store.completed)
	}
	log, _ := store.GetActionLog(ctx, "job-a", 1)
	if log.ErrorClass != string(domain.ErrorClassAuth) {
		t.Fatalf("class must be AUTH, got %q", log.ErrorClass)
	}
}

// TestRecordAttemptBannedNeverRetries: same for BANNED — the account is locked,
// another attempt cannot help.
func TestRecordAttemptBannedNeverRetries(t *testing.T) {
	store := newFakeActionStore()
	svc := newRetryTestService(store)
	ctx := context.Background()

	if err := svc.RecordAttempt(ctx, AttemptRecord{
		AttemptID: "job-ban:1",
		Status:    domain.AttemptFailed,
		Error:     strPtr("Your account has been banned"),
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if len(store.rescheduled) != 0 {
		t.Fatal("a BANNED failure must never be retried")
	}
	if len(store.completed) != 1 || store.completed[0].status != domain.JobStatusFailed {
		t.Fatalf("BANNED must terminate FAILED, got %+v", store.completed)
	}
}

// TestRecordAttemptIdempotentReplay: the (job, attempt) pair is the
// idempotency key. The RUNNING report and the terminal verdict are one row by
// construction, and a replay cannot double-complete the job.
func TestRecordAttemptIdempotentReplay(t *testing.T) {
	store := newFakeActionStore()
	svc := newRetryTestService(store)
	ctx := context.Background()

	// Running first (no verdict yet), then the terminal success on the same key.
	for _, status := range []domain.AttemptStatus{domain.AttemptRunning, domain.AttemptSuccess} {
		if err := svc.RecordAttempt(ctx, AttemptRecord{
			AttemptID:    "job-idem:1",
			Status:       status,
			RenderedText: strPtr("nice post"),
		}); err != nil {
			t.Fatalf("record %q: %v", status, err)
		}
	}
	if len(store.logs) != 2 {
		t.Fatalf("expected 2 upsert calls (running + terminal on one row), got %d", len(store.logs))
	}
	// The RUNNING callback must NOT complete the job: only the terminal
	// verdict does, otherwise a worker's "I started" message would close a job
	// it has not finished.
	if len(store.completed) != 1 || store.completed[0].status != domain.JobStatusSuccess {
		t.Fatalf("exactly one SUCCESS completion (the terminal verdict), got %+v", store.completed)
	}
	log, _ := store.GetActionLog(ctx, "job-idem", 1)
	if log.Status != domain.AttemptSuccess || !log.Verified {
		t.Fatalf("the replayed verdict must be SUCCESS+verified, got %+v", log)
	}
}

// TestRecordAttemptRunningIsNotTerminal: a RUNNING callback only books the
// attempt; it must not complete or reschedule the job, because the worker is
// still mid-action.
func TestRecordAttemptRunningIsNotTerminal(t *testing.T) {
	store := newFakeActionStore()
	svc := newRetryTestService(store)
	ctx := context.Background()

	if err := svc.RecordAttempt(ctx, AttemptRecord{
		AttemptID: "job-run:1",
		Status:    domain.AttemptRunning,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if len(store.completed) != 0 || len(store.rescheduled) != 0 {
		t.Fatalf("a RUNNING callback must not touch the job, got completed=%d rescheduled=%d", len(store.completed), len(store.rescheduled))
	}
	log, err := store.GetActionLog(ctx, "job-run", 1)
	if err != nil {
		t.Fatalf("the attempt row must still be written: %v", err)
	}
	if log.Status != domain.AttemptRunning || log.Verified {
		t.Fatalf("the attempt must be RUNNING and unverified, got %+v", log)
	}
}

// TestRecordAttemptStoreUnavailable: no store wired -> the honest
// ErrUnavailable, not a panic, so a partial deployment degrades cleanly.
func TestRecordAttemptStoreUnavailable(t *testing.T) {
	svc := NewJobService(nil, nil, nil, nil, nil, nil)
	if err := svc.RecordAttempt(context.Background(), AttemptRecord{
		AttemptID: "job-z:1",
		Status:    domain.AttemptSuccess,
	}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

// TestRecordAttemptInvalidAttemptID: a malformed id is a validation error, so
// the callback returns 4xx and the worker's own retry loop stops on it (4xx
// stop per the ticket).
func TestRecordAttemptInvalidAttemptID(t *testing.T) {
	store := newFakeActionStore()
	svc := newRetryTestService(store)
	if err := svc.RecordAttempt(context.Background(), AttemptRecord{
		AttemptID: "not-an-attempt-id",
		Status:    domain.AttemptSuccess,
	}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected ErrValidation for a malformed attempt id, got %v", err)
	}
}

// TestRecordAttemptNeverThrows: a store error surfaces as an error rather than a
// panic; the caller turns it into a 5xx the worker can retry.
func TestRecordAttemptNeverThrows(t *testing.T) {
	store := &erroringActionStore{fakeActionStore: newFakeActionStore()}
	svc := NewJobService(nil, nil, nil, store, fixedClock{t: time.Unix(1_000_000, 0).UTC()}, nil)

	if err := svc.RecordAttempt(context.Background(), AttemptRecord{
		AttemptID: "job-c:1",
		Status:    domain.AttemptSuccess,
	}); err == nil {
		t.Fatal("a store error must surface, not be swallowed")
	}
}

// erroringActionStore fails the write so the service's error handling is
// exercised without a real dependency. The other methods are inherited.
type erroringActionStore struct {
	*fakeActionStore
}

func (e *erroringActionStore) UpsertActionLog(_ context.Context, l domain.ActionLog) (domain.ActionLog, error) {
	return domain.ActionLog{}, errors.New("store is down")
}
