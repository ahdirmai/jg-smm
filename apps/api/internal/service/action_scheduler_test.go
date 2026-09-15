package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
)

// P3 action scheduler tests. The scheduler is policy: which jobs survive the
// gates and reach a worker. Every store/gate is a scripted fake because the SQL
// and the Redis atomicity are proven in their own packages; what is proven here
// is the decision table.

// --- scripted fakes -------------------------------------------------------

type schedTargets struct{ url string }

func (s *schedTargets) GetTarget(_ context.Context, id string) (domain.Target, error) {
	return domain.Target{ID: id, URL: s.url, Platform: domain.PlatformInstagram}, nil
}

type schedAccounts struct {
	acc      domain.Account
	workerID string
}

func (a *schedAccounts) GetByID(_ context.Context, id string) (domain.Account, error) {
	if a.acc.ID == "" {
		a.acc.ID = id
	}
	if a.acc.Platform == "" {
		a.acc.Platform = domain.PlatformInstagram
	}
	if a.acc.WorkerID == nil {
		a.acc.WorkerID = &a.workerID
	}
	return a.acc, nil
}

type schedCooldown struct {
	allow bool
	err   error
	calls int
}

func (c *schedCooldown) Acquire(_ context.Context, accountID, targetKey string, window time.Duration) (bool, error) {
	c.calls++
	if c.err != nil {
		return false, c.err
	}
	return c.allow, nil
}

type schedLimits struct {
	allow      bool
	retryAfter time.Duration
	err        error
	platforms  []string
}

func (l *schedLimits) Allow(_ context.Context, platform string, limit int, window time.Duration) (bool, time.Duration, error) {
	l.platforms = append(l.platforms, platform)
	if l.err != nil {
		return false, 0, l.err
	}
	return l.allow, l.retryAfter, nil
}

type schedTransport struct {
	jobs []workerActionJob
	err  error
}

func (t *schedTransport) Enqueue(_ context.Context, workerID string, job []byte) error {
	if t.err != nil {
		return t.err
	}
	var w workerActionJob
	if err := json.Unmarshal(job, &w); err != nil {
		return err
	}
	t.jobs = append(t.jobs, w)
	return nil
}
func (t *schedTransport) PublishControl(_ context.Context, workerID string, msg []byte) error {
	return nil
}

// newSched builds a scheduler over the scripted fakes with deterministic time.
func newSched(t *testing.T, actions *fakeActionStore, cd *schedCooldown, lm *schedLimits, tr *schedTransport) *ActionScheduler {
	t.Helper()
	now := time.Unix(2_000_000, 0).UTC()
	s := NewActionScheduler(
		actions,
		&schedAccounts{workerID: "worker-1"},
		&schedTargets{url: "https://instagram.com/p/x"},
		cd, lm, tr,
		ActionSchedulerConfig{
			TickBudget: 5,
			Cooldown:   60 * time.Second,
			RateWindow: time.Hour,
			RateLimits: map[string]int{"instagram": 30, "threads": 15},
			Clock:      func() time.Time { return now },
		},
	)
	return s
}

// aDueAction inserts a claimable job directly in PENDING, due now.
func aDueAction(actions *fakeActionStore, id string) domain.ActionJob {
	return domain.ActionJob{
		ID:          id,
		Type:        domain.JobTypeActionComment,
		TargetID:    "tgt-1",
		AccountID:   "acct-1",
		WorkerID:    "worker-1",
		Status:      domain.JobStatusPending,
		ScheduledAt: time.Unix(1_000_000, 0).UTC(), // before the fixed clock
	}
}

// --- decision table -------------------------------------------------------

// The happy path: both gates pass and the job reaches the worker's queue with
// the wire shape the worker BLPOPs.
func TestActionSchedulerPublishes(t *testing.T) {
	store := newFakeActionStore()
	store.logByAttempt = map[string]domain.ActionLog{}
	cd := &schedCooldown{allow: true}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{}
	s := newSched(t, store, cd, lm, tr)

	// Seed a PENDING job the claim query will return.
	seedPending(store, "job-1")

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 1 {
		t.Fatalf("one job must reach the transport, got %d", len(tr.jobs))
	}
	got := tr.jobs[0]
	if got.ID != "job-1" || got.Action != "comment" || got.Platform != "instagram" {
		t.Fatalf("wire shape mismatch: %+v", got)
	}
	if got.TargetURL != "https://instagram.com/p/x" {
		t.Fatalf("the target URL must be resolved, got %q", got.TargetURL)
	}
}

// Cooldown refuses: the job goes back with a delay, never to the worker.
func TestActionSchedulerCooldownBlocks(t *testing.T) {
	store := newFakeActionStore()
	cd := &schedCooldown{allow: false}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{}
	s := newSched(t, store, cd, lm, tr)
	seedPending(store, "job-c")

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 0 {
		t.Fatal("a cooled-down job must not be published")
	}
	if len(store.rescheduled) != 1 {
		t.Fatalf("a blocked job must be rescheduled, got %d", len(store.rescheduled))
	}
}

// A rate-limit refusal reschedules for the reported reset, not a fixed delay.
func TestActionSchedulerRateLimitBlocks(t *testing.T) {
	store := newFakeActionStore()
	cd := &schedCooldown{allow: true}
	lm := &schedLimits{allow: false, retryAfter: 30 * time.Minute}
	tr := &schedTransport{}
	s := newSched(t, store, cd, lm, tr)
	seedPending(store, "job-r")
	base := s.clock()

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 0 {
		t.Fatal("a rate-limited job must not be published")
	}
	if len(store.rescheduled) != 1 {
		t.Fatalf("expected one reschedule, got %d", len(store.rescheduled))
	}
	if got := store.rescheduled[0].at.Sub(base); got != 30*time.Minute {
		t.Fatalf("reschedule must wait for the reset, got %v", got)
	}
}

// A gate error is a requeue, not a drop: the job is too valuable to lose to a
// transient Redis blip.
func TestActionSchedulerGateErrorRequeues(t *testing.T) {
	store := newFakeActionStore()
	cd := &schedCooldown{err: errors.New("redis down")}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{}
	s := newSched(t, store, cd, lm, tr)
	seedPending(store, "job-e")

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 0 {
		t.Fatal("a gate error must not publish")
	}
	if len(store.rescheduled) != 1 {
		t.Fatalf("a gate error must requeue, got %d", len(store.rescheduled))
	}
}

// No worker assigned: the job waits for the fleet, it is not failed.
func TestActionSchedulerNoWorkerRequeues(t *testing.T) {
	store := newFakeActionStore()
	cd := &schedCooldown{allow: true}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{}
	s := newSched(t, store, cd, lm, tr)
	s.accounts = &schedAccounts{workerID: "", acc: domain.Account{ID: "acct-1"}}
	seedPending(store, "job-nw")

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 0 {
		t.Fatal("a job with no worker must not be published")
	}
	if len(store.rescheduled) != 1 {
		t.Fatalf("a workerless job must be requeued, got %d", len(store.rescheduled))
	}
}

// A transport failure after the gates passed must not lose the job.
func TestActionSchedulerTransportFailureRequeues(t *testing.T) {
	store := newFakeActionStore()
	cd := &schedCooldown{allow: true}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{err: errors.New("connection refused")}
	s := newSched(t, store, cd, lm, tr)
	seedPending(store, "job-t")

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(store.rescheduled) != 1 {
		t.Fatalf("an undelivered job must be requeued, got %d", len(store.rescheduled))
	}
}

// A target that cannot be resolved fails the job fast: there is nothing to
// retry that could fix a missing target.
func TestActionSchedulerMissingTargetFails(t *testing.T) {
	store := newFakeActionStore()
	cd := &schedCooldown{allow: true}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{}
	s := newSched(t, store, cd, lm, tr)
	// Seed a PENDING job the claim query will return.
	seedPending(store, "job-mt")
	// A target store that always misses.
	s.scrapes = missingTargets{}

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 0 {
		t.Fatal("a job with no target must not be published")
	}
	if len(store.completed) != 1 || store.completed[0].status != domain.JobStatusFailed {
		t.Fatalf("a missing target must fail the job, got %+v", store.completed)
	}
}

// Nothing due anywhere: an empty tick is success, not an error.
func TestActionSchedulerEmptyTick(t *testing.T) {
	store := newFakeActionStore()
	s := newSched(t, store, &schedCooldown{allow: true}, &schedLimits{allow: true}, &schedTransport{})

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("an empty tick must not error, got %v", err)
	}
}

// A future job (inside its backoff window) is not claimed.
func TestActionSchedulerSkipsFutureJob(t *testing.T) {
	store := newFakeActionStore()
	cd := &schedCooldown{allow: true}
	lm := &schedLimits{allow: true}
	tr := &schedTransport{}
	s := newSched(t, store, cd, lm, tr)
	// A job scheduled AFTER the fixed clock — not due.
	store.pending = append(store.pending, domain.ActionJob{
		ID:          "job-future",
		Type:        domain.JobTypeActionComment,
		AccountID:   "acct-1",
		Status:      domain.JobStatusPending,
		ScheduledAt: time.Unix(3_000_000, 0).UTC(),
	})

	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(tr.jobs) != 0 {
		t.Fatal("a future job must not be published")
	}
}

// actionName maps the queue's enum to the worker's dispatch verb.
func TestActionName(t *testing.T) {
	if actionName(domain.JobTypeActionComment) != "comment" {
		t.Fatal("comment must map to the worker's comment verb")
	}
	if actionName(domain.JobTypeActionLike) != "like" {
		t.Fatal("like must map to the worker's like verb")
	}
}

// --- helpers --------------------------------------------------------------

type missingTargets struct{}

func (missingTargets) GetTarget(_ context.Context, id string) (domain.Target, error) {
	return domain.Target{}, domain.ErrNotFound
}

// seedPending puts a due job in the fake action store's pending list, which the
// scheduler's ListActionJobsByStatus returns.
func seedPending(store *fakeActionStore, id string) {
	store.pending = append(store.pending, aDueAction(store, id))
}
