package service

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// Manual retry (operator pressing Retry on a FAILED row). The store SQL is
// proven in repository/action_test.go; what matters here is the guard —
// re-queueing a job that is not FAILED would either double-deliver work to a
// second worker or repeat an action the platform already performed.

type retryClock struct{ at time.Time }

func (c retryClock) Now() time.Time { return c.at }

func newRetryService(store port.ActionStore, at time.Time) *ActionService {
	return NewActionService(store, nil, nil, retryClock{at: at}, nil, nil, nil, nil)
}

func TestRetryRequeuesFailedJob(t *testing.T) {
	store := newFakeActionStore()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	store.completed = []completedJob{{id: "job-1", status: domain.JobStatusFailed}}

	svc := newRetryService(store, now)
	if _, err := svc.Retry(context.Background(), "job-1"); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	if len(store.rescheduled) != 1 {
		t.Fatalf("rescheduled %d jobs, want 1", len(store.rescheduled))
	}
	got := store.rescheduled[0]
	if got.id != "job-1" {
		t.Errorf("rescheduled %q, want job-1", got.id)
	}
	// Now, not now+backoff: the velocity gate decides safety, not the button.
	if !got.at.Equal(now) {
		t.Errorf("scheduled_at = %v, want %v (no backoff on a manual retry)", got.at, now)
	}
}

func TestRetryRejectsNonFailedStatuses(t *testing.T) {
	// Re-queueing a PENDING/RUNNING job hands the same work to a second
	// worker; re-queueing a SUCCESS repeats an action already performed.
	for _, status := range []domain.JobStatus{
		domain.JobStatusPending,
		domain.JobStatusRunning,
		domain.JobStatusSuccess,
		domain.JobStatusCancelled,
	} {
		t.Run(string(status), func(t *testing.T) {
			store := newFakeActionStore()
			store.completed = []completedJob{{id: "job-1", status: status}}

			svc := newRetryService(store, time.Now())
			_, err := svc.Retry(context.Background(), "job-1")
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("Retry(%s) error = %v, want ErrValidation", status, err)
			}
			if len(store.rescheduled) != 0 {
				t.Errorf("rescheduled %d jobs, want 0", len(store.rescheduled))
			}
		})
	}
}

func TestRetryUnknownJob(t *testing.T) {
	store := newFakeActionStore()
	svc := newRetryService(store, time.Now())

	_, err := svc.Retry(context.Background(), "nope")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Retry error = %v, want ErrNotFound", err)
	}
}

// The dashboard's "run pending now" button: everything waiting in the backoff
// is pulled to the current instant, so the next worker poll claims it instead
// of the operator waiting out a timer.
func TestRunPendingNowPullsWholeQueueForward(t *testing.T) {
	store := newFakeActionStore()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	store.pending = []domain.ActionJob{
		{ID: "job-1", Status: domain.JobStatusPending, ScheduledAt: now.Add(4 * time.Minute)},
		{ID: "job-2", Status: domain.JobStatusPending, ScheduledAt: now.Add(9 * time.Minute)},
	}

	n, err := newRetryService(store, now).RunPendingNow(context.Background())
	if err != nil {
		t.Fatalf("RunPendingNow: %v", err)
	}
	if n != 2 {
		t.Fatalf("moved %d jobs, want 2", n)
	}
	for _, r := range store.rescheduled {
		if !r.at.Equal(now) {
			t.Errorf("job %s scheduled at %v, want %v (now, not its old backoff)", r.id, r.at, now)
		}
	}
}

// A queue larger than MaxActionBatch moves entirely. The button promises "run
// pending now"; a paged sweep would leave the tail waiting on its backoff
// timer.
func TestRunPendingNowSweepsPastTheBatchCap(t *testing.T) {
	store := newFakeActionStore()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	total := MaxActionBatch + 10
	store.pending = make([]domain.ActionJob, 0, total)
	for i := 0; i < total; i++ {
		store.pending = append(store.pending, domain.ActionJob{
			ID:          "job-" + strconv.Itoa(i),
			Status:      domain.JobStatusPending,
			ScheduledAt: now.Add(time.Duration(i) * time.Minute),
		})
	}

	n, err := newRetryService(store, now).RunPendingNow(context.Background())
	if err != nil {
		t.Fatalf("RunPendingNow: %v", err)
	}
	if n != total {
		t.Fatalf("moved %d jobs, want all %d (sweep must not stop at the %d cap)",
			n, total, MaxActionBatch)
	}
}

// An empty queue is a valid answer, not an error — the button has to be safe to
// press when there is nothing waiting.
func TestRunPendingNowEmptyQueue(t *testing.T) {
	n, err := newRetryService(newFakeActionStore(), time.Now()).RunPendingNow(context.Background())
	if err != nil {
		t.Fatalf("RunPendingNow on an empty queue: %v", err)
	}
	if n != 0 {
		t.Errorf("moved %d jobs, want 0", n)
	}
}

// --- cancel: operator stop ---------------------------------------------------

func TestCancelStopsQueuedAndRunningJobs(t *testing.T) {
	for _, status := range []domain.JobStatus{domain.JobStatusPending, domain.JobStatusRunning} {
		t.Run(string(status), func(t *testing.T) {
			store := newFakeActionStore()
			store.pending = []domain.ActionJob{{ID: "job-1", Status: status}}

			job, err := newRetryService(store, time.Now()).Cancel(context.Background(), "job-1")
			if err != nil {
				t.Fatalf("Cancel(%s): %v", status, err)
			}
			if job.Status != domain.JobStatusCancelled {
				t.Fatalf("status = %s, want cancelled", job.Status)
			}
			if len(store.cancelled) != 1 || store.cancelled[0] != "job-1" {
				t.Fatalf("store cancelled %v, want [job-1]", store.cancelled)
			}
		})
	}
}

// A terminal job cannot be re-graded: cancelling a SUCCESS would rewrite the
// platform's verdict, and cancelling a CANCELLED row is a pointless press.
func TestCancelRejectsTerminalStatuses(t *testing.T) {
	for _, status := range []domain.JobStatus{
		domain.JobStatusSuccess,
		domain.JobStatusFailed,
		domain.JobStatusCancelled,
	} {
		t.Run(string(status), func(t *testing.T) {
			store := newFakeActionStore()
			store.completed = []completedJob{{id: "job-1", status: status}}

			_, err := newRetryService(store, time.Now()).Cancel(context.Background(), "job-1")
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("Cancel(%s) error = %v, want ErrValidation", status, err)
			}
			if len(store.cancelled) != 0 {
				t.Errorf("store cancelled %v, want none", store.cancelled)
			}
		})
	}
}

func TestCancelUnknownJob(t *testing.T) {
	_, err := newRetryService(newFakeActionStore(), time.Now()).Cancel(context.Background(), "nope")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Cancel error = %v, want ErrNotFound", err)
	}
}

// Bulk cancel clears the queue but leaves RUNNING rows alone: a dispatched job
// may already have posted, and its late callback is already guarded. The
// per-row cancel exists for that case.
func TestCancelPendingNowLeavesRunningRows(t *testing.T) {
	store := newFakeActionStore()
	store.pending = []domain.ActionJob{
		{ID: "p-1", Status: domain.JobStatusPending},
		{ID: "p-2", Status: domain.JobStatusPending},
		{ID: "r-1", Status: domain.JobStatusRunning},
	}

	n, err := newRetryService(store, time.Now()).CancelPendingNow(context.Background())
	if err != nil {
		t.Fatalf("CancelPendingNow: %v", err)
	}
	if n != 2 {
		t.Fatalf("cancelled %d jobs, want 2", n)
	}
	if store.pending[2].Status != domain.JobStatusRunning {
		t.Fatalf("RUNNING row became %s, want running", store.pending[2].Status)
	}
}

func TestCancelPendingNowEmptyQueue(t *testing.T) {
	n, err := newRetryService(newFakeActionStore(), time.Now()).CancelPendingNow(context.Background())
	if err != nil {
		t.Fatalf("CancelPendingNow on an empty queue: %v", err)
	}
	if n != 0 {
		t.Errorf("cancelled %d jobs, want 0", n)
	}
}

// --- M-4: the sweep must move every distinct row exactly once --------------

// The bug: ListActionJobsByStatus ordered by scheduled_at LIMIT/OFFSET while
// each reschedule rewrote scheduled_at to now. The offset therefore walked a
// set that re-sorted under it. With two due batches — A at T0 and B at T0+10s,
// 50 each — page 1 moved A and re-sorted the set to [B, A]; page 2 (OFFSET 50)
// re-read the 50 rows it had already moved; OFFSET 100 was empty, so the loop
// ended reporting 100 moved while exactly 50 had and batch B was never touched.
//
// pagedActionStore is the OLD caller's view of the store, kept here so the
// regression is pinned: ListActionJobsByStatus re-sorts by scheduled_at on every
// read and honours LIMIT/OFFSET, exactly as the SQL does. The sweep under test
// must not depend on a stable sort it does not have.
func TestRunPendingNowMovesEachDueJobExactlyOnce(t *testing.T) {
	store := newFakeActionStore()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	t0 := now.Add(-time.Hour)
	// Batch A: 50 jobs due an hour ago. Batch B: 50 jobs due 10 seconds later.
	for i := 0; i < MaxActionBatch; i++ {
		store.pending = append(store.pending, domain.ActionJob{
			ID:          "A-" + strconv.Itoa(i),
			Status:      domain.JobStatusPending,
			ScheduledAt: t0,
		})
	}
	for i := 0; i < MaxActionBatch; i++ {
		store.pending = append(store.pending, domain.ActionJob{
			ID:          "B-" + strconv.Itoa(i),
			Status:      domain.JobStatusPending,
			ScheduledAt: t0.Add(10 * time.Second),
		})
	}

	n, err := newRetryService(store, now).RunPendingNow(context.Background())
	if err != nil {
		t.Fatalf("RunPendingNow: %v", err)
	}
	if n != 100 {
		t.Fatalf("moved %d jobs, want 100 distinct", n)
	}

	// Every row, A and B alike, is now due at the same instant — this is the
	// half the old loop failed: B was never read at all.
	distinct := map[string]int{}
	for _, j := range store.pending {
		if !j.ScheduledAt.Equal(now) {
			t.Errorf("job %s still scheduled at %v, want %v", j.ID, j.ScheduledAt, now)
		}
		distinct[j.ID]++
	}
	if len(distinct) != 100 {
		t.Fatalf("saw %d distinct jobs, want 100", len(distinct))
	}
	// No row may be counted twice: each id moves once, because it is a single
	// UPDATE and not a row walk that can revisit a moved row.
	for _, id := range []string{"A-0", "B-0"} {
		if distinct[id] != 1 {
			t.Fatalf("job %s appears %d times, want exactly 1", id, distinct[id])
		}
	}
	// The store itself must be asked once, never paged: a per-row loop would
	// show up as many RescheduleActionJob calls.
	if moved := len(store.rescheduled); moved != 100 {
		t.Fatalf("the sweep recorded %d reschedules, want 100 (one per row)", moved)
	}
}

// Only PENDING rows move: a RUNNING or terminal job is not part of "the
// pending queue", and pulling one forward would re-dispatch work in flight.
func TestRunPendingNowTouchesOnlyPendingRows(t *testing.T) {
	store := newFakeActionStore()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	running := now.Add(5 * time.Minute)
	store.pending = []domain.ActionJob{
		{ID: "p-1", Status: domain.JobStatusPending, ScheduledAt: now.Add(time.Minute)},
		{ID: "r-1", Status: domain.JobStatusRunning, ScheduledAt: running},
	}

	n, err := newRetryService(store, now).RunPendingNow(context.Background())
	if err != nil {
		t.Fatalf("RunPendingNow: %v", err)
	}
	if n != 1 {
		t.Fatalf("moved %d jobs, want 1 (the PENDING one)", n)
	}
	for _, j := range store.pending {
		if j.ID == "r-1" && !j.ScheduledAt.Equal(running) {
			t.Fatalf("a RUNNING job was rescheduled to %v", j.ScheduledAt)
		}
	}
}
