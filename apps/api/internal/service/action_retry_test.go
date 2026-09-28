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

// A queue larger than MaxActionBatch is swept in pages, not capped. The button
// promises "run pending now"; a cap would move the first 50 and report
// "moved: 50" while the tail kept waiting on its backoff timer.
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
